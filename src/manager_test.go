package main

import "testing"

func TestMemTableInsertAndSearch(t *testing.T) {
	mt := NewMemTable(0)
	if err := mt.Insert("a", []byte("1")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, ok := mt.Search("a")
	if !ok {
		t.Fatal("expected key 'a' to exist")
	}
	if string(val) != "1" {
		t.Fatalf("expected '1', got %q", val)
	}
}

func TestMemTableRotatesOnOverflow(t *testing.T) {
	// maxBytes=4: "a"+"1"=2, "b"+"2"=2 fills it, "c"+"3"=2 triggers rotation
	mt := NewMemTable(4)
	mt.Insert("a", []byte("1"))
	mt.Insert("b", []byte("2"))

	// This should trigger rotation
	err := mt.Insert("c", []byte("3"))
	if err != nil {
		t.Fatalf("expected rotation to handle overflow, got %v", err)
	}

	// All keys should be searchable
	for _, key := range []string{"a", "b", "c"} {
		if _, ok := mt.Search(key); !ok {
			t.Fatalf("expected key %q to be found after rotation", key)
		}
	}

	// Should have exactly one immutable skip list
	imm := mt.GetImmutables()
	if len(imm) != 1 {
		t.Fatalf("expected 1 immutable, got %d", len(imm))
	}
	if !imm[0].IsImmutable() {
		t.Fatal("expected frozen skip list to be immutable")
	}
}

func TestMemTableSearchFallsThrough(t *testing.T) {
	// Fill and rotate twice to have two immutables
	mt := NewMemTable(4)
	mt.Insert("a", []byte("1")) // 2 bytes
	mt.Insert("b", []byte("2")) // 2 bytes → full, next insert rotates

	mt.Insert("c", []byte("3")) // rotation 1
	mt.Insert("d", []byte("4")) // 2+2=4 → full

	mt.Insert("e", []byte("5")) // rotation 2

	// Keys from all generations should be found
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		val, ok := mt.Search(key)
		if !ok {
			t.Fatalf("expected key %q to be found", key)
		}
		expected := string(key[0] - 'a' + '1')
		if string(val) != expected {
			t.Fatalf("key %q: expected %q, got %q", key, expected, val)
		}
	}

	// Missing key should not be found
	if _, ok := mt.Search("z"); ok {
		t.Fatal("expected key 'z' to not exist")
	}

	imm := mt.GetImmutables()
	if len(imm) != 2 {
		t.Fatalf("expected 2 immutables, got %d", len(imm))
	}
}

func TestMemTableOnlyOneMutable(t *testing.T) {
	mt := NewMemTable(4)
	mt.Insert("a", []byte("1"))
	mt.Insert("b", []byte("2"))
	mt.Insert("c", []byte("3")) // triggers rotation

	// All immutables must be immutable
	for i, sl := range mt.GetImmutables() {
		if !sl.IsImmutable() {
			t.Fatalf("immutable[%d] is not marked immutable", i)
		}
	}
}

func TestMemTableDelete(t *testing.T) {
	mt := NewMemTable(0)
	mt.Insert("a", []byte("1"))

	if err := mt.Delete("a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After delete, Search returns (nil, true) — tombstone found.
	val, ok := mt.Search("a")
	if !ok {
		t.Fatal("expected tombstone to be found")
	}
	if val != nil {
		t.Fatalf("expected nil value for tombstone, got %v", val)
	}
}

func TestMemTableDeleteMissing(t *testing.T) {
	mt := NewMemTable(0)
	// Deleting a non-existent key inserts a tombstone — no error expected.
	if err := mt.Delete("nope"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Searching for the tombstoned key returns (nil, true) — tombstone found.
	val, ok := mt.Search("nope")
	if !ok {
		t.Fatal("expected tombstone to be found")
	}
	if val != nil {
		t.Fatalf("expected nil value for tombstone, got %v", val)
	}
}

func TestMemTableRemoveImmutable(t *testing.T) {
	mt := NewMemTable(4)
	mt.Insert("a", []byte("1"))
	mt.Insert("b", []byte("2"))
	mt.Insert("c", []byte("3")) // rotation

	imm := mt.GetImmutables()
	if len(imm) != 1 {
		t.Fatalf("expected 1 immutable, got %d", len(imm))
	}

	mt.RemoveImmutable(imm[0])

	imm = mt.GetImmutables()
	if len(imm) != 0 {
		t.Fatalf("expected 0 immutables after removal, got %d", len(imm))
	}
}

func TestMemTableRemoveImmutableSearchStillWorks(t *testing.T) {
	mt := NewMemTable(4)
	mt.Insert("a", []byte("1"))
	mt.Insert("b", []byte("2"))
	mt.Insert("c", []byte("3")) // rotation

	// "c" is in active, "a"/"b" are in immutable
	// After removing the immutable, "a" and "b" are gone
	imm := mt.GetImmutables()
	mt.RemoveImmutable(imm[0])

	// "c" should still be found
	if _, ok := mt.Search("c"); !ok {
		t.Fatal("expected key 'c' to still exist in active")
	}
	// "a" should be gone (was in the removed immutable)
	if _, ok := mt.Search("a"); ok {
		t.Fatal("expected key 'a' to be gone after removing immutable")
	}
}
