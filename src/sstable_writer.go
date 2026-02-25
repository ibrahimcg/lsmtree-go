package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

var sstableSeq atomic.Uint64

// SSTableWriter writes immutable skip lists to SSTable files.
type SSTableWriter struct {
	Dir string
}

// WriteSSTable flushes a skip list to a new SSTable file and returns its path.
func (w *SSTableWriter) WriteSSTable(sl *SkipList) (string, error) {
	seq := sstableSeq.Add(1)
	name := fmt.Sprintf("sstable_%06d.sst", seq)
	finalPath := filepath.Join(w.Dir, name)
	tmpPath := finalPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("sstable: create tmp: %w", err)
	}
	defer func() {
		// Clean up tmp file on failure.
		if f != nil {
			f.Close()
			os.Remove(tmpPath)
		}
	}()

	// --- Write data blocks, collect index entries ---
	var (
		index   []indexEntry
		block   []byte
		firstKey string
		offset  uint64
	)

	it := sl.NewIterator()
	for it.Valid() {
		entry := encodeEntry(it.Key(), it.Value())

		// Start a new block if needed.
		if len(block) == 0 {
			firstKey = it.Key()
		}

		block = append(block, entry...)

		if len(block) >= dataBlockTargetSize {
			n, err := f.Write(block)
			if err != nil {
				return "", fmt.Errorf("sstable: write data block: %w", err)
			}
			index = append(index, indexEntry{FirstKey: firstKey, Offset: offset, Size: uint32(n)})
			offset += uint64(n)
			block = block[:0]
		}

		it.Next()
	}

	// Flush remaining partial block.
	if len(block) > 0 {
		n, err := f.Write(block)
		if err != nil {
			return "", fmt.Errorf("sstable: write data block: %w", err)
		}
		index = append(index, indexEntry{FirstKey: firstKey, Offset: offset, Size: uint32(n)})
		offset += uint64(n)
	}

	// --- Write index block ---
	indexOffset := offset
	var indexBuf []byte
	for _, ie := range index {
		indexBuf = append(indexBuf, encodeIndexEntry(ie)...)
	}
	if _, err := f.Write(indexBuf); err != nil {
		return "", fmt.Errorf("sstable: write index block: %w", err)
	}
	indexSize := uint32(len(indexBuf))
	offset += uint64(indexSize)

	// --- Write bloom filter block ---
	bloomOffset := offset
	bloomBuf := encodeBloomFilter(sl.bloom)
	if _, err := f.Write(bloomBuf); err != nil {
		return "", fmt.Errorf("sstable: write bloom filter: %w", err)
	}
	bloomSize := uint32(len(bloomBuf))

	// --- Write footer ---
	ft := footer{
		IndexOffset: indexOffset,
		IndexSize:   indexSize,
		BloomOffset: bloomOffset,
		BloomSize:   bloomSize,
		Magic:       magicNumber,
	}
	if _, err := f.Write(encodeFooter(ft)); err != nil {
		return "", fmt.Errorf("sstable: write footer: %w", err)
	}

	// Sync and atomically rename.
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sstable: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("sstable: close: %w", err)
	}
	f = nil // Prevent deferred cleanup.

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("sstable: rename: %w", err)
	}

	return finalPath, nil
}

// FlushImmutable flushes the oldest immutable skip list from the MemTable to an SSTable.
// On success it removes the flushed skip list from the MemTable's immutable queue.
func FlushImmutable(mt *MemTable, w *SSTableWriter) (string, error) {
	imm := mt.GetImmutables()
	if len(imm) == 0 {
		return "", fmt.Errorf("sstable: no immutable memtables to flush")
	}

	// Oldest is last in the slice (prepend order in manager.go).
	oldest := imm[len(imm)-1]

	path, err := w.WriteSSTable(oldest)
	if err != nil {
		return "", err
	}

	mt.RemoveImmutable(oldest)
	return path, nil
}
