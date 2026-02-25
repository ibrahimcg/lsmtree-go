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

// SSTableInfo describes a written SSTable file.
type SSTableInfo struct {
	Path     string
	MinKey   string
	MaxKey   string
	FileSize int64
}

// WriteSSTable flushes a skip list to a new SSTable file and returns its path.
func (w *SSTableWriter) WriteSSTable(sl *SkipList) (string, error) {
	info, err := w.WriteFromIterator(sl.NewIterator(), sl.bloom)
	if err != nil {
		return "", err
	}
	return info.Path, nil
}

// WriteFromIterator writes all entries from an Iterator into a new SSTable file.
// The provided bloom filter is written into the file; if nil, a new one is built.
func (w *SSTableWriter) WriteFromIterator(iter Iterator, bloom *BloomFilter) (SSTableInfo, error) {
	seq := sstableSeq.Add(1)
	name := fmt.Sprintf("sstable_%06d.sst", seq)
	finalPath := filepath.Join(w.Dir, name)
	tmpPath := finalPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return SSTableInfo{}, fmt.Errorf("sstable: create tmp: %w", err)
	}
	defer func() {
		if f != nil {
			f.Close()
			os.Remove(tmpPath)
		}
	}()

	// Build bloom filter if not provided.
	buildBloom := bloom == nil
	if buildBloom {
		bloom = OptimalBloomFilter(10000, 0.01)
	}

	var (
		index    []indexEntry
		block    []byte
		firstKey string
		offset   uint64
		minKey   string
		maxKey   string
		first    = true
	)

	for iter.Valid() {
		key := iter.Key()
		value := iter.Value()

		if first {
			minKey = key
			first = false
		}
		maxKey = key

		if buildBloom {
			bloom.Add(key)
		}

		entry := encodeEntry(key, value)

		if len(block) == 0 {
			firstKey = key
		}

		block = append(block, entry...)

		if len(block) >= dataBlockTargetSize {
			n, err := f.Write(block)
			if err != nil {
				return SSTableInfo{}, fmt.Errorf("sstable: write data block: %w", err)
			}
			index = append(index, indexEntry{FirstKey: firstKey, Offset: offset, Size: uint32(n)})
			offset += uint64(n)
			block = block[:0]
		}

		iter.Next()
	}

	if first {
		// No entries written — clean up and return empty info.
		f.Close()
		os.Remove(tmpPath)
		f = nil
		return SSTableInfo{}, fmt.Errorf("sstable: no entries to write")
	}

	// Flush remaining partial block.
	if len(block) > 0 {
		n, err := f.Write(block)
		if err != nil {
			return SSTableInfo{}, fmt.Errorf("sstable: write data block: %w", err)
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
		return SSTableInfo{}, fmt.Errorf("sstable: write index block: %w", err)
	}
	indexSize := uint32(len(indexBuf))
	offset += uint64(indexSize)

	// --- Write bloom filter block ---
	bloomOffset := offset
	bloomBuf := encodeBloomFilter(bloom)
	if _, err := f.Write(bloomBuf); err != nil {
		return SSTableInfo{}, fmt.Errorf("sstable: write bloom filter: %w", err)
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
		return SSTableInfo{}, fmt.Errorf("sstable: write footer: %w", err)
	}

	// Sync and atomically rename.
	if err := f.Sync(); err != nil {
		return SSTableInfo{}, fmt.Errorf("sstable: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return SSTableInfo{}, fmt.Errorf("sstable: close: %w", err)
	}
	f = nil

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return SSTableInfo{}, fmt.Errorf("sstable: rename: %w", err)
	}

	fi, err := os.Stat(finalPath)
	if err != nil {
		return SSTableInfo{}, fmt.Errorf("sstable: stat: %w", err)
	}

	return SSTableInfo{
		Path:     finalPath,
		MinKey:   minKey,
		MaxKey:   maxKey,
		FileSize: fi.Size(),
	}, nil
}

// WriteLimitedFromIterator writes entries from an iterator into one SSTable,
// up to maxBytes of data block content. Returns the info, whether the iterator
// has more entries remaining, and any error.
// When skipTombstones is true, tombstone entries are omitted (used at the
// bottommost level where tombstones can be safely dropped).
func (w *SSTableWriter) WriteLimitedFromIterator(iter Iterator, maxBytes int64, skipTombstones bool) (SSTableInfo, bool, error) {
	seq := sstableSeq.Add(1)
	name := fmt.Sprintf("sstable_%06d.sst", seq)
	finalPath := filepath.Join(w.Dir, name)
	tmpPath := finalPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return SSTableInfo{}, false, fmt.Errorf("sstable: create tmp: %w", err)
	}
	defer func() {
		if f != nil {
			f.Close()
			os.Remove(tmpPath)
		}
	}()

	bloom := OptimalBloomFilter(10000, 0.01)

	var (
		index       []indexEntry
		block       []byte
		firstKey    string
		offset      uint64
		minKey      string
		maxKey      string
		first       = true
		dataWritten int64
	)

	for iter.Valid() {
		if skipTombstones && iter.IsTombstone() {
			iter.Next()
			continue
		}

		key := iter.Key()
		value := iter.Value()
		entry := encodeEntry(key, value)

		// Check if adding this entry would exceed maxBytes.
		if !first && maxBytes > 0 && dataWritten+int64(len(block))+int64(len(entry)) > maxBytes {
			break
		}

		if first {
			minKey = key
			first = false
		}
		maxKey = key
		bloom.Add(key)

		if len(block) == 0 {
			firstKey = key
		}

		block = append(block, entry...)

		if len(block) >= dataBlockTargetSize {
			n, err := f.Write(block)
			if err != nil {
				return SSTableInfo{}, false, fmt.Errorf("sstable: write data block: %w", err)
			}
			index = append(index, indexEntry{FirstKey: firstKey, Offset: offset, Size: uint32(n)})
			dataWritten += int64(n)
			offset += uint64(n)
			block = block[:0]
		}

		iter.Next()
	}

	if first {
		// No entries written (e.g., all tombstones skipped).
		f.Close()
		os.Remove(tmpPath)
		f = nil
		return SSTableInfo{}, iter.Valid(), nil
	}

	// Flush remaining partial block.
	if len(block) > 0 {
		n, err := f.Write(block)
		if err != nil {
			return SSTableInfo{}, false, fmt.Errorf("sstable: write data block: %w", err)
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
		return SSTableInfo{}, false, fmt.Errorf("sstable: write index block: %w", err)
	}
	indexSize := uint32(len(indexBuf))
	offset += uint64(indexSize)

	// --- Write bloom filter block ---
	bloomOffset := offset
	bloomBuf := encodeBloomFilter(bloom)
	if _, err := f.Write(bloomBuf); err != nil {
		return SSTableInfo{}, false, fmt.Errorf("sstable: write bloom filter: %w", err)
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
		return SSTableInfo{}, false, fmt.Errorf("sstable: write footer: %w", err)
	}

	if err := f.Sync(); err != nil {
		return SSTableInfo{}, false, fmt.Errorf("sstable: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return SSTableInfo{}, false, fmt.Errorf("sstable: close: %w", err)
	}
	f = nil

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return SSTableInfo{}, false, fmt.Errorf("sstable: rename: %w", err)
	}

	fi, err := os.Stat(finalPath)
	if err != nil {
		return SSTableInfo{}, false, fmt.Errorf("sstable: stat: %w", err)
	}

	return SSTableInfo{
		Path:     finalPath,
		MinKey:   minKey,
		MaxKey:   maxKey,
		FileSize: fi.Size(),
	}, iter.Valid(), nil
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
