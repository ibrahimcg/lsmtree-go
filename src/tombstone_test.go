package main

import (
	"testing"
)

func TestTombstoneEncodeDecodeRoundTrip(t *testing.T) {
	buf := encodeEntry("deleted-key", nil)
	key, val, n, tombstone, err := decodeEntry(buf)
	if err != nil {
		t.Fatalf("decodeEntry error: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("consumed %d bytes, expected %d", n, len(buf))
	}
	if key != "deleted-key" {
		t.Fatalf("key: got %q, want %q", key, "deleted-key")
	}
	if val != nil {
		t.Fatalf("value: expected nil for tombstone, got %v", val)
	}
	if !tombstone {
		t.Fatal("expected tombstone flag to be true")
	}
}

func TestTombstoneInSSTable(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	sl.Insert("alive", []byte("value"))
	sl.Insert("dead", nil) // tombstone
	sl.MarkImmutable()

	w := &SSTableWriter{Dir: dir}
	path, err := w.WriteSSTable(sl)
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}

	r, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable: %v", err)
	}
	defer r.Close()

	// "alive" should be found with its value.
	val, found, err := r.Search("alive")
	if err != nil {
		t.Fatalf("Search(alive) error: %v", err)
	}
	if !found || string(val) != "value" {
		t.Fatalf("alive: found=%v, val=%q", found, val)
	}

	// "dead" should be found as tombstone (found=true, val=nil).
	val, found, err = r.Search("dead")
	if err != nil {
		t.Fatalf("Search(dead) error: %v", err)
	}
	if !found {
		t.Fatal("expected tombstone to be found")
	}
	if val != nil {
		t.Fatalf("expected nil value for tombstone, got %v", val)
	}
}

func TestMemTableTombstoneStopsSearch(t *testing.T) {
	// Fill first memtable with a value, rotate, then delete in the new active.
	mt := NewMemTable(4)
	mt.Insert("a", []byte("1")) // 2 bytes
	mt.Insert("b", []byte("2")) // 2 bytes → full

	mt.Insert("c", []byte("3")) // triggers rotation; "a","b" now in immutable

	// Delete "a" in the active memtable (inserts tombstone).
	mt.Delete("a")

	// Search should find the tombstone in active and return (nil, true),
	// not fall through to the immutable where "a" has a value.
	val, ok := mt.Search("a")
	if !ok {
		t.Fatal("expected tombstone to be found in active memtable")
	}
	if val != nil {
		t.Fatalf("expected nil value for tombstone, got %v", val)
	}
}

func TestTombstonePreservedAcrossFlush(t *testing.T) {
	dir := t.TempDir()
	mt := NewMemTable(0)
	mt.Insert("x", []byte("val"))
	mt.Delete("x") // insert tombstone

	// Manually rotate to create an immutable.
	mt.Insert("force-rotate-sentinel", []byte("v"))
	// Directly flush the active as if it were immutable for simplicity.
	sl := NewSkipList(0)
	sl.Insert("x", nil) // tombstone
	sl.Insert("y", []byte("yval"))
	sl.MarkImmutable()

	w := &SSTableWriter{Dir: dir}
	path, err := w.WriteSSTable(sl)
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}

	r, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable: %v", err)
	}
	defer r.Close()

	// Tombstone should survive the flush.
	val, found, err := r.Search("x")
	if err != nil {
		t.Fatalf("Search(x) error: %v", err)
	}
	if !found {
		t.Fatal("tombstone not preserved in SSTable")
	}
	if val != nil {
		t.Fatal("expected nil value for tombstone in SSTable")
	}

	// Normal entry should also be present.
	val, found, err = r.Search("y")
	if err != nil {
		t.Fatalf("Search(y) error: %v", err)
	}
	if !found || string(val) != "yval" {
		t.Fatalf("y: found=%v, val=%q", found, val)
	}
}
