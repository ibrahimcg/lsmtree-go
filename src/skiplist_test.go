package main

import (
	"flag"
	"os"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

func TestMain(m *testing.M) {
	flag.Set("rapid.checks", "1000")
	flag.Parse()
	os.Exit(m.Run())
}

func TestInsertAndSearch(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("apple", []byte("red"))
	sl.Insert("banana", []byte("yellow"))
	sl.Insert("cherry", []byte("dark red"))

	tests := []struct {
		key  string
		want string
	}{
		{"apple", "red"},
		{"banana", "yellow"},
		{"cherry", "dark red"},
	}

	for _, tt := range tests {
		val, ok := sl.Search(tt.key)
		if !ok {
			t.Fatalf("expected key %q to exist", tt.key)
		}
		if string(val) != tt.want {
			t.Fatalf("key %q: got %q, want %q", tt.key, val, tt.want)
		}
	}
}

func TestSearchMissing(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("a", []byte("1"))

	_, ok := sl.Search("z")
	if ok {
		t.Fatal("expected key 'z' to not exist")
	}
}

func TestUpdateExistingKey(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("key", []byte("old"))
	sl.Insert("key", []byte("new"))

	val, ok := sl.Search("key")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if string(val) != "new" {
		t.Fatalf("got %q, want %q", val, "new")
	}
	if sl.Len() != 1 {
		t.Fatalf("expected size 1, got %d", sl.Len())
	}
}

func TestDelete(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("a", []byte("1"))
	sl.Insert("b", []byte("2"))
	sl.Insert("c", []byte("3"))

	deleted, err := sl.Delete("b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("expected delete to return true")
	}
	if sl.Len() != 2 {
		t.Fatalf("expected size 2, got %d", sl.Len())
	}
	if _, ok := sl.Search("b"); ok {
		t.Fatal("expected key 'b' to be deleted")
	}

	// remaining keys still accessible
	if _, ok := sl.Search("a"); !ok {
		t.Fatal("expected key 'a' to exist")
	}
	if _, ok := sl.Search("c"); !ok {
		t.Fatal("expected key 'c' to exist")
	}
}

func TestDeleteMissing(t *testing.T) {
	sl := NewSkipList(0)
	deleted, err := sl.Delete("nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Fatal("expected delete of missing key to return false")
	}
}

func TestLen(t *testing.T) {
	sl := NewSkipList(0)
	if sl.Len() != 0 {
		t.Fatalf("expected empty list, got %d", sl.Len())
	}

	sl.Insert("x", []byte("1"))
	sl.Insert("y", []byte("2"))
	if sl.Len() != 2 {
		t.Fatalf("expected size 2, got %d", sl.Len())
	}

	if _, err := sl.Delete("x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sl.Len() != 1 {
		t.Fatalf("expected size 1, got %d", sl.Len())
	}
}

// Property-based tests using rapid

// Every inserted key must be retrievable with the correct value.
func TestPropertyInsertThenSearch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList(0)
		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,20}`), 1, 100).Draw(t, "keys")

		for _, k := range keys {
			sl.Insert(k, []byte(k))
		}

		for _, k := range keys {
			val, ok := sl.Search(k)
			if !ok {
				t.Fatalf("key %q not found after insert", k)
			}
			if string(val) != k {
				t.Fatalf("key %q: got %q, want %q", k, val, k)
			}
		}
	})
}

// Inserting the same key twice should keep only the latest value, and Len stays correct.
func TestPropertyUpdateOverwrites(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList(0)
		ref := make(map[string]string)

		ops := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,10}`), 1, 200).Draw(t, "ops")
		for i, k := range ops {
			v := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "val"+string(rune(i)))
			sl.Insert(k, []byte(v))
			ref[k] = v
		}

		if sl.Len() != len(ref) {
			t.Fatalf("len mismatch: skiplist=%d, map=%d", sl.Len(), len(ref))
		}

		for k, want := range ref {
			got, ok := sl.Search(k)
			if !ok {
				t.Fatalf("key %q missing", k)
			}
			if string(got) != want {
				t.Fatalf("key %q: got %q, want %q", k, got, want)
			}
		}
	})
}

// Deleted keys must not be found; non-deleted keys must still be found.
func TestPropertyDeleteRemovesKey(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList(0)
		ref := make(map[string]string)

		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,10}`), 1, 100).Draw(t, "keys")
		for _, k := range keys {
			sl.Insert(k, []byte(k))
			ref[k] = k
		}

		toDelete := rapid.SliceOfN(rapid.SampledFrom(keys), 0, len(keys)).Draw(t, "deletes")
		for _, k := range toDelete {
			if _, err := sl.Delete(k); err != nil {
				t.Fatalf("unexpected error deleting %q: %v", k, err)
			}
			delete(ref, k)
		}

		if sl.Len() != len(ref) {
			t.Fatalf("len mismatch: skiplist=%d, map=%d", sl.Len(), len(ref))
		}

		for _, k := range keys {
			_, ok := sl.Search(k)
			_, inRef := ref[k]
			if ok != inRef {
				t.Fatalf("key %q: found=%v, expected=%v", k, ok, inRef)
			}
		}
	})
}

// The skip list should behave identically to a reference map under random insert/delete/search ops.
func TestPropertyStateMachine(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList(0)
		ref := make(map[string]string)

		nOps := rapid.IntRange(1, 300).Draw(t, "nOps")
		for i := 0; i < nOps; i++ {
			op := rapid.IntRange(0, 2).Draw(t, "op")
			key := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "key")

			switch op {
			case 0: // insert
				val := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "val")
				sl.Insert(key, []byte(val))
				ref[key] = val
			case 1: // delete
				if _, err := sl.Delete(key); err != nil {
					t.Fatalf("unexpected error deleting %q: %v", key, err)
				}
				delete(ref, key)
			case 2: // search
				got, ok := sl.Search(key)
				want, inRef := ref[key]
				if ok != inRef {
					t.Fatalf("search %q: found=%v, expected=%v", key, ok, inRef)
				}
				if ok && string(got) != want {
					t.Fatalf("search %q: got %q, want %q", key, got, want)
				}
			}
		}

		if sl.Len() != len(ref) {
			t.Fatalf("final len mismatch: skiplist=%d, map=%d", sl.Len(), len(ref))
		}
	})
}

// Skip list iteration at level 0 should yield keys in sorted order.
func TestPropertySortedOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList(0)
		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,10}`), 1, 100).Draw(t, "keys")

		seen := make(map[string]bool)
		for _, k := range keys {
			sl.Insert(k, []byte(k))
			seen[k] = true
		}

		// collect keys by walking level 0
		var got []string
		cur := sl.header.forward[0]
		for cur != nil {
			got = append(got, cur.key)
			cur = cur.forward[0]
		}

		expected := make([]string, 0, len(seen))
		for k := range seen {
			expected = append(expected, k)
		}
		sort.Strings(expected)

		if len(got) != len(expected) {
			t.Fatalf("length mismatch: got %d, want %d", len(got), len(expected))
		}
		for i := range got {
			if got[i] != expected[i] {
				t.Fatalf("index %d: got %q, want %q", i, got[i], expected[i])
			}
		}
	})
}

func TestInsertBlocksWhenFull(t *testing.T) {
	// "a"+"1" = 2, "b"+"2" = 2, "c"+"3" = 2 → total 6 bytes
	sl := NewSkipList(6)
	sl.Insert("a", []byte("1"))
	sl.Insert("b", []byte("2"))
	sl.Insert("c", []byte("3"))

	// "d"+"4" = 2 bytes, would push to 8 > 6
	err := sl.Insert("d", []byte("4"))
	if err != ErrSkipListFull {
		t.Fatalf("expected ErrSkipListFull, got %v", err)
	}
	if sl.Len() != 3 {
		t.Fatalf("expected size 3, got %d", sl.Len())
	}
	if _, ok := sl.Search("d"); ok {
		t.Fatal("expected key 'd' to not exist")
	}
}

func TestUpdateAllowedWhenFull(t *testing.T) {
	// "a"+"1" = 2, "b"+"2" = 2 → total 4 bytes
	sl := NewSkipList(4)
	sl.Insert("a", []byte("1"))
	sl.Insert("b", []byte("2"))

	// same-size update should succeed
	err := sl.Insert("a", []byte("x"))
	if err != nil {
		t.Fatalf("expected same-size update to succeed, got %v", err)
	}
	val, _ := sl.Search("a")
	if string(val) != "x" {
		t.Fatalf("expected 'x', got %q", val)
	}

	// shrinking update should succeed
	err = sl.Insert("b", []byte(""))
	if err != nil {
		t.Fatalf("expected shrinking update to succeed, got %v", err)
	}

	// growing update that exceeds limit should fail
	err = sl.Insert("a", []byte("toolong"))
	if err != ErrSkipListFull {
		t.Fatalf("expected ErrSkipListFull for oversized update, got %v", err)
	}
	// value should remain unchanged
	val, _ = sl.Search("a")
	if string(val) != "x" {
		t.Fatalf("expected 'x' after rejected update, got %q", val)
	}
}

func TestInsertAfterDeleteWhenFull(t *testing.T) {
	// "a"+"1" = 2, "b"+"2" = 2 → total 4 bytes
	sl := NewSkipList(4)
	sl.Insert("a", []byte("1"))
	sl.Insert("b", []byte("2"))

	if _, err := sl.Delete("a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	} // frees 2 bytes → 2 used
	err := sl.Insert("c", []byte("3")) // +2 = 4, fits
	if err != nil {
		t.Fatalf("expected insert to succeed after delete, got %v", err)
	}
	if sl.Len() != 2 {
		t.Fatalf("expected size 2, got %d", sl.Len())
	}
}

func TestIsFull(t *testing.T) {
	sl := NewSkipList(4)
	if sl.IsFull() {
		t.Fatal("expected not full")
	}
	sl.Insert("a", []byte("1")) // 2 bytes
	sl.Insert("b", []byte("2")) // 2 bytes → 4 = maxBytes
	if !sl.IsFull() {
		t.Fatal("expected full")
	}
}

func TestSizeBytes(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("key", []byte("value")) // 3 + 5 = 8
	if sl.SizeBytes() != 8 {
		t.Fatalf("expected 8 bytes, got %d", sl.SizeBytes())
	}

	sl.Insert("key", []byte("v")) // update: 3 + 1 = 4
	if sl.SizeBytes() != 4 {
		t.Fatalf("expected 4 bytes after update, got %d", sl.SizeBytes())
	}

	if _, err := sl.Delete("key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sl.SizeBytes() != 0 {
		t.Fatalf("expected 0 bytes after delete, got %d", sl.SizeBytes())
	}
}

func TestZeroMaxBytesIsUnlimited(t *testing.T) {
	sl := NewSkipList(0)
	for i := 0; i < 1000; i++ {
		err := sl.Insert(string(rune(i)), []byte("v"))
		if err != nil {
			t.Fatalf("expected no limit, got error at %d: %v", i, err)
		}
	}
}

// Property: sizeBytes always equals the sum of key+value lengths of all entries.
func TestPropertySizeBytesAccurate(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList(0)
		ref := make(map[string]string)

		nOps := rapid.IntRange(1, 200).Draw(t, "nOps")
		for i := 0; i < nOps; i++ {
			op := rapid.IntRange(0, 1).Draw(t, "op")
			key := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "key")

			switch op {
			case 0:
				val := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "val")
				sl.Insert(key, []byte(val))
				ref[key] = val
			case 1:
				if _, err := sl.Delete(key); err != nil {
					t.Fatalf("unexpected error deleting %q: %v", key, err)
				}
				delete(ref, key)
			}
		}

		var expectedBytes int64
		for k, v := range ref {
			expectedBytes += int64(len(k)) + int64(len(v))
		}
		if sl.SizeBytes() != expectedBytes {
			t.Fatalf("sizeBytes mismatch: skiplist=%d, expected=%d", sl.SizeBytes(), expectedBytes)
		}
	})
}

// Property: a skip list with maxBytes=N never exceeds N bytes of data.
func TestPropertyByteLimitRespected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxBytes := int64(rapid.IntRange(10, 200).Draw(t, "maxBytes"))
		sl := NewSkipList(maxBytes)
		ref := make(map[string]string)

		ops := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,8}`), 1, 200).Draw(t, "keys")
		for _, k := range ops {
			v := rapid.StringMatching(`[a-z]{1,4}`).Draw(t, "val")
			err := sl.Insert(k, []byte(v))

			if err == ErrSkipListFull {
				// rejected — ref unchanged
			} else if err != nil {
				t.Fatalf("insert %q failed unexpectedly: %v", k, err)
			} else {
				ref[k] = v
			}
		}

		if sl.SizeBytes() > maxBytes {
			t.Fatalf("sizeBytes %d exceeds maxBytes %d", sl.SizeBytes(), maxBytes)
		}
		if sl.Len() != len(ref) {
			t.Fatalf("len mismatch: skiplist=%d, map=%d", sl.Len(), len(ref))
		}
	})
}

func TestImmutableRejectsInsert(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("a", []byte("1"))
	sl.MarkImmutable()

	err := sl.Insert("b", []byte("2"))
	if err != ErrImmutable {
		t.Fatalf("expected ErrImmutable, got %v", err)
	}
}

func TestImmutableRejectsDelete(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("a", []byte("1"))
	sl.MarkImmutable()

	deleted, err := sl.Delete("a")
	if err != ErrImmutable {
		t.Fatalf("expected ErrImmutable, got %v", err)
	}
	if deleted {
		t.Fatal("expected deleted to be false")
	}
}

func TestImmutableAllowsSearch(t *testing.T) {
	sl := NewSkipList(0)
	sl.Insert("a", []byte("1"))
	sl.MarkImmutable()

	val, ok := sl.Search("a")
	if !ok {
		t.Fatal("expected key 'a' to exist")
	}
	if string(val) != "1" {
		t.Fatalf("expected '1', got %q", val)
	}
}

func TestIsImmutable(t *testing.T) {
	sl := NewSkipList(0)
	if sl.IsImmutable() {
		t.Fatal("expected not immutable")
	}
	sl.MarkImmutable()
	if !sl.IsImmutable() {
		t.Fatal("expected immutable")
	}
}
