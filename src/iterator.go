package main

// Iterator is the common interface for sequential scans over sorted key-value data.
// Both in-memory (SkipList) and on-disk (SSTable) sources implement this interface.
type Iterator interface {
	// Valid reports whether the iterator is positioned at a valid entry.
	Valid() bool
	// Key returns the key of the current entry.
	Key() string
	// Value returns the value of the current entry (nil for tombstones).
	Value() []byte
	// IsTombstone reports whether the current entry is a deletion marker.
	IsTombstone() bool
	// Next advances the iterator to the next entry.
	Next()
}
