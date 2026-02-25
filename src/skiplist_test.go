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
	sl := NewSkipList()
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
	sl := NewSkipList()
	sl.Insert("a", []byte("1"))

	_, ok := sl.Search("z")
	if ok {
		t.Fatal("expected key 'z' to not exist")
	}
}

func TestUpdateExistingKey(t *testing.T) {
	sl := NewSkipList()
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
	sl := NewSkipList()
	sl.Insert("a", []byte("1"))
	sl.Insert("b", []byte("2"))
	sl.Insert("c", []byte("3"))

	if !sl.Delete("b") {
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
	sl := NewSkipList()
	if sl.Delete("nope") {
		t.Fatal("expected delete of missing key to return false")
	}
}

func TestLen(t *testing.T) {
	sl := NewSkipList()
	if sl.Len() != 0 {
		t.Fatalf("expected empty list, got %d", sl.Len())
	}

	sl.Insert("x", []byte("1"))
	sl.Insert("y", []byte("2"))
	if sl.Len() != 2 {
		t.Fatalf("expected size 2, got %d", sl.Len())
	}

	sl.Delete("x")
	if sl.Len() != 1 {
		t.Fatalf("expected size 1, got %d", sl.Len())
	}
}

// Property-based tests using rapid

// Every inserted key must be retrievable with the correct value.
func TestPropertyInsertThenSearch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList()
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
		sl := NewSkipList()
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
		sl := NewSkipList()
		ref := make(map[string]string)

		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,10}`), 1, 100).Draw(t, "keys")
		for _, k := range keys {
			sl.Insert(k, []byte(k))
			ref[k] = k
		}

		toDelete := rapid.SliceOfN(rapid.SampledFrom(keys), 0, len(keys)).Draw(t, "deletes")
		for _, k := range toDelete {
			sl.Delete(k)
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
		sl := NewSkipList()
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
				sl.Delete(key)
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
		sl := NewSkipList()
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
