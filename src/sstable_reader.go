package main

import (
	"fmt"
	"os"
)

// SSTableReader provides point lookups against an SSTable file.
type SSTableReader struct {
	f     *os.File
	index []indexEntry
	bloom *BloomFilter
}

// OpenSSTable opens an SSTable file and loads its index and bloom filter into memory.
func OpenSSTable(path string) (*SSTableReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sstable: open: %w", err)
	}

	// Read footer.
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("sstable: stat: %w", err)
	}
	if fi.Size() < int64(footerSize) {
		f.Close()
		return nil, fmt.Errorf("sstable: file too small for footer")
	}

	footerBuf := make([]byte, footerSize)
	if _, err := f.ReadAt(footerBuf, fi.Size()-int64(footerSize)); err != nil {
		f.Close()
		return nil, fmt.Errorf("sstable: read footer: %w", err)
	}
	ft, err := decodeFooter(footerBuf)
	if err != nil {
		f.Close()
		return nil, err
	}

	// Read index block.
	indexBuf := make([]byte, ft.IndexSize)
	if _, err := f.ReadAt(indexBuf, int64(ft.IndexOffset)); err != nil {
		f.Close()
		return nil, fmt.Errorf("sstable: read index: %w", err)
	}
	index, err := decodeIndexBlock(indexBuf)
	if err != nil {
		f.Close()
		return nil, err
	}

	// Read bloom filter block.
	bloomBuf := make([]byte, ft.BloomSize)
	if _, err := f.ReadAt(bloomBuf, int64(ft.BloomOffset)); err != nil {
		f.Close()
		return nil, fmt.Errorf("sstable: read bloom: %w", err)
	}
	bloom, err := decodeBloomFilter(bloomBuf)
	if err != nil {
		f.Close()
		return nil, err
	}

	return &SSTableReader{f: f, index: index, bloom: bloom}, nil
}

// Search looks up a key in the SSTable.
// Returns (value, true, nil) on hit, (nil, false, nil) on miss.
func (r *SSTableReader) Search(key string) ([]byte, bool, error) {
	// Bloom filter check.
	if !r.bloom.MayContain(key) {
		return nil, false, nil
	}

	// Binary search the index to find the candidate data block.
	// We want the last block whose FirstKey <= key.
	lo, hi := 0, len(r.index)-1
	blockIdx := -1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if r.index[mid].FirstKey <= key {
			blockIdx = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if blockIdx < 0 {
		return nil, false, nil
	}

	// Read the data block.
	ie := r.index[blockIdx]
	blockBuf := make([]byte, ie.Size)
	if _, err := r.f.ReadAt(blockBuf, int64(ie.Offset)); err != nil {
		return nil, false, fmt.Errorf("sstable: read data block: %w", err)
	}

	// Linear scan within the block.
	pos := 0
	for pos < len(blockBuf) {
		k, v, n, err := decodeEntry(blockBuf[pos:])
		if err != nil {
			return nil, false, err
		}
		if k == key {
			return v, true, nil
		}
		if k > key {
			break // Keys are sorted; no point continuing.
		}
		pos += n
	}
	return nil, false, nil
}

// Close releases the underlying file handle.
func (r *SSTableReader) Close() error {
	return r.f.Close()
}
