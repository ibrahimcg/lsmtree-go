package main

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

func TestSSTableIteratorAllEntries(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	n := 100
	for i := range n {
		sl.Insert(fmt.Sprintf("key-%05d", i), []byte(fmt.Sprintf("val-%05d", i)))
	}
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

	it := NewSSTableIterator(r)
	count := 0
	var prev string
	for it.Valid() {
		if it.Key() < prev {
			t.Fatalf("out of order: %q after %q", it.Key(), prev)
		}
		expected := fmt.Sprintf("val-%05d", count)
		if string(it.Value()) != expected {
			t.Fatalf("key %q: got value %q, want %q", it.Key(), it.Value(), expected)
		}
		prev = it.Key()
		count++
		it.Next()
	}
	if it.Err() != nil {
		t.Fatalf("iterator error: %v", it.Err())
	}
	if count != n {
		t.Fatalf("iterated %d entries, expected %d", count, n)
	}
}

func TestSSTableIteratorSortedOrder(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	// Insert keys that will span multiple data blocks.
	for i := range 500 {
		sl.Insert(fmt.Sprintf("k%06d", i), []byte(fmt.Sprintf("v%06d", i)))
	}
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

	it := NewSSTableIterator(r)
	var keys []string
	for it.Valid() {
		keys = append(keys, it.Key())
		it.Next()
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatal("SSTable iterator did not produce sorted keys")
	}
	if len(keys) != 500 {
		t.Fatalf("got %d keys, expected 500", len(keys))
	}
}

func TestSSTableIteratorTombstones(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	sl.Insert("alive", []byte("val"))
	sl.Insert("dead", nil) // tombstone
	sl.Insert("zombie", []byte("brains"))
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

	it := NewSSTableIterator(r)

	// "alive"
	if !it.Valid() || it.Key() != "alive" || it.IsTombstone() {
		t.Fatalf("expected alive entry, got key=%q tombstone=%v valid=%v", it.Key(), it.IsTombstone(), it.Valid())
	}
	it.Next()

	// "dead" (tombstone)
	if !it.Valid() || it.Key() != "dead" || !it.IsTombstone() {
		t.Fatalf("expected dead tombstone, got key=%q tombstone=%v valid=%v", it.Key(), it.IsTombstone(), it.Valid())
	}
	it.Next()

	// "zombie"
	if !it.Valid() || it.Key() != "zombie" || it.IsTombstone() {
		t.Fatalf("expected zombie entry, got key=%q tombstone=%v valid=%v", it.Key(), it.IsTombstone(), it.Valid())
	}
	it.Next()

	if it.Valid() {
		t.Fatal("expected iterator exhausted")
	}
}

func TestSSTableIteratorEmpty(t *testing.T) {
	// Create an SSTable with one entry (can't have zero-entry SSTable easily).
	dir := t.TempDir()
	sl := NewSkipList(0)
	sl.Insert("only", []byte("one"))
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

	it := NewSSTableIterator(r)
	if !it.Valid() {
		t.Fatal("expected valid at first entry")
	}
	if it.Key() != "only" {
		t.Fatalf("got %q, want 'only'", it.Key())
	}
	it.Next()
	if it.Valid() {
		t.Fatal("expected exhausted after single entry")
	}
}

func TestPropertySSTableIteratorMatchesSkipList(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp("", "sstiter-test-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)

		sl := NewSkipList(0)
		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,10}`), 1, 100).Draw(t, "keys")
		for _, k := range keys {
			val := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "val")
			sl.Insert(k, []byte(val))
		}
		sl.MarkImmutable()

		// Collect expected entries from SkipList iterator.
		var expected []struct{ key, val string }
		slit := sl.NewIterator()
		for slit.Valid() {
			expected = append(expected, struct{ key, val string }{slit.Key(), string(slit.Value())})
			slit.Next()
		}

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

		ssit := NewSSTableIterator(r)
		idx := 0
		for ssit.Valid() {
			if idx >= len(expected) {
				t.Fatalf("SSTable iterator produced more entries than SkipList")
			}
			if ssit.Key() != expected[idx].key {
				t.Fatalf("entry %d: key %q != %q", idx, ssit.Key(), expected[idx].key)
			}
			if string(ssit.Value()) != expected[idx].val {
				t.Fatalf("entry %d: val %q != %q", idx, string(ssit.Value()), expected[idx].val)
			}
			idx++
			ssit.Next()
		}
		if idx != len(expected) {
			t.Fatalf("SSTable iterator produced %d entries, expected %d", idx, len(expected))
		}
	})
}
