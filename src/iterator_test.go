package main

import "testing"

// Compile-time check that SkipListIterator satisfies Iterator.
var _ Iterator = (*SkipListIterator)(nil)

func TestSkipListIteratorIsTombstone(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("alive", []byte("value"))
	sl.Insert("dead", nil) // tombstone

	it := sl.NewIterator()

	// First entry should be "alive" (sorted order).
	if !it.Valid() {
		t.Fatal("expected valid iterator")
	}
	if it.Key() != "alive" {
		t.Fatalf("expected key 'alive', got %q", it.Key())
	}
	if it.IsTombstone() {
		t.Fatal("expected 'alive' to not be a tombstone")
	}

	it.Next()

	// Second entry should be "dead" (tombstone).
	if !it.Valid() {
		t.Fatal("expected valid iterator at second entry")
	}
	if it.Key() != "dead" {
		t.Fatalf("expected key 'dead', got %q", it.Key())
	}
	if !it.IsTombstone() {
		t.Fatal("expected 'dead' to be a tombstone")
	}

	it.Next()
	if it.Valid() {
		t.Fatal("expected iterator to be exhausted")
	}
}
