package main

import (
	"fmt"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

// sliceIterator is a test helper that wraps a pre-built sorted slice.
type sliceIterator struct {
	entries []sliceEntry
	pos     int
}

type sliceEntry struct {
	key       string
	value     []byte
	tombstone bool
}

func newSliceIterator(entries []sliceEntry) *sliceIterator {
	return &sliceIterator{entries: entries}
}

func (si *sliceIterator) Valid() bool       { return si.pos < len(si.entries) }
func (si *sliceIterator) Key() string       { return si.entries[si.pos].key }
func (si *sliceIterator) Value() []byte     { return si.entries[si.pos].value }
func (si *sliceIterator) IsTombstone() bool { return si.entries[si.pos].tombstone }
func (si *sliceIterator) Next()             { si.pos++ }

var _ Iterator = (*sliceIterator)(nil)

func TestMergeIteratorDisjoint(t *testing.T) {
	s1 := newSliceIterator([]sliceEntry{
		{"a", []byte("1"), false},
		{"c", []byte("3"), false},
	})
	s2 := newSliceIterator([]sliceEntry{
		{"b", []byte("2"), false},
		{"d", []byte("4"), false},
	})

	mi := NewMergeIterator([]Iterator{s1, s2})

	expected := []string{"a", "b", "c", "d"}
	for i, want := range expected {
		if !mi.Valid() {
			t.Fatalf("expected valid at position %d", i)
		}
		if mi.Key() != want {
			t.Fatalf("position %d: got %q, want %q", i, mi.Key(), want)
		}
		mi.Next()
	}
	if mi.Valid() {
		t.Fatal("expected iterator exhausted")
	}
}

func TestMergeIteratorOverlappingPriority(t *testing.T) {
	// s1 (index 0) has higher priority than s2 (index 1).
	s1 := newSliceIterator([]sliceEntry{
		{"a", []byte("new"), false},
		{"c", []byte("new-c"), false},
	})
	s2 := newSliceIterator([]sliceEntry{
		{"a", []byte("old"), false},
		{"b", []byte("b-val"), false},
		{"c", []byte("old-c"), false},
	})

	mi := NewMergeIterator([]Iterator{s1, s2})

	type kv struct {
		key string
		val string
	}
	expected := []kv{
		{"a", "new"},   // s1 wins
		{"b", "b-val"}, // only in s2
		{"c", "new-c"}, // s1 wins
	}

	for i, want := range expected {
		if !mi.Valid() {
			t.Fatalf("expected valid at position %d", i)
		}
		if mi.Key() != want.key {
			t.Fatalf("position %d key: got %q, want %q", i, mi.Key(), want.key)
		}
		if string(mi.Value()) != want.val {
			t.Fatalf("position %d value: got %q, want %q", i, string(mi.Value()), want.val)
		}
		mi.Next()
	}
	if mi.Valid() {
		t.Fatal("expected iterator exhausted")
	}
}

func TestMergeIteratorTombstone(t *testing.T) {
	s1 := newSliceIterator([]sliceEntry{
		{"a", nil, true}, // tombstone in higher-priority source
	})
	s2 := newSliceIterator([]sliceEntry{
		{"a", []byte("old"), false},
	})

	mi := NewMergeIterator([]Iterator{s1, s2})

	if !mi.Valid() {
		t.Fatal("expected valid")
	}
	if mi.Key() != "a" {
		t.Fatalf("got key %q, want 'a'", mi.Key())
	}
	if !mi.IsTombstone() {
		t.Fatal("expected tombstone from higher-priority source")
	}
	mi.Next()
	if mi.Valid() {
		t.Fatal("expected exhausted")
	}
}

func TestMergeIteratorEmpty(t *testing.T) {
	mi := NewMergeIterator(nil)
	if mi.Valid() {
		t.Fatal("expected empty merge iterator to be invalid")
	}
}

func TestMergeIteratorSingleSource(t *testing.T) {
	s := newSliceIterator([]sliceEntry{
		{"x", []byte("1"), false},
		{"y", []byte("2"), false},
	})
	mi := NewMergeIterator([]Iterator{s})

	var keys []string
	for mi.Valid() {
		keys = append(keys, mi.Key())
		mi.Next()
	}
	if len(keys) != 2 || keys[0] != "x" || keys[1] != "y" {
		t.Fatalf("unexpected keys: %v", keys)
	}
}

func TestPropertyMergeIteratorSorted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nSources := rapid.IntRange(1, 5).Draw(t, "nSources")
		sources := make([]Iterator, nSources)
		allKeys := make(map[string]string) // key → expected value (from source 0 priority)

		for i := 0; i < nSources; i++ {
			nEntries := rapid.IntRange(0, 20).Draw(t, fmt.Sprintf("nEntries_%d", i))
			var entries []sliceEntry
			seen := make(map[string]bool)
			for j := 0; j < nEntries; j++ {
				k := rapid.StringMatching(`[a-e]{1,3}`).Draw(t, fmt.Sprintf("key_%d_%d", i, j))
				if seen[k] {
					continue
				}
				seen[k] = true
				v := rapid.StringMatching(`[a-z]{1,5}`).Draw(t, fmt.Sprintf("val_%d_%d", i, j))
				entries = append(entries, sliceEntry{k, []byte(v), false})
				// First source to have this key wins.
				if _, exists := allKeys[k]; !exists {
					allKeys[k] = v
				}
			}
			// Sort entries for the iterator.
			sort.Slice(entries, func(a, b int) bool {
				return entries[a].key < entries[b].key
			})
			sources[i] = newSliceIterator(entries)
		}

		mi := NewMergeIterator(sources)

		var prev string
		count := 0
		for mi.Valid() {
			k := mi.Key()
			// Keys must be in sorted order.
			if k < prev {
				t.Fatalf("out of order: %q after %q", k, prev)
			}
			// No duplicates.
			if k == prev {
				t.Fatalf("duplicate key: %q", k)
			}
			// Value should match highest-priority source.
			if want, ok := allKeys[k]; ok {
				if string(mi.Value()) != want {
					t.Fatalf("key %q: got %q, want %q", k, string(mi.Value()), want)
				}
			}
			prev = k
			count++
			mi.Next()
		}

		if count != len(allKeys) {
			t.Fatalf("got %d keys, expected %d", count, len(allKeys))
		}
	})
}
