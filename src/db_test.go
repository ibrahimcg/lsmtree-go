package main

import (
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDBPutGetDelete(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir

	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	// Put.
	if err := db.Put("hello", []byte("world")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get.
	val, ok := db.Get("hello")
	if !ok || string(val) != "world" {
		t.Fatalf("Get: got (%q, %v), want ('world', true)", val, ok)
	}

	// Missing key.
	_, ok = db.Get("missing")
	if ok {
		t.Fatal("expected missing key to return false")
	}

	// Delete.
	if err := db.Delete("hello"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok = db.Get("hello")
	if ok {
		t.Fatal("expected deleted key to return false")
	}

	db.Close()
}

func TestDBPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir
	cfg.MaxMemBytes = 100 // small to force flushes

	// First session: write data.
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	for i := range 50 {
		key := fmt.Sprintf("key-%05d", i)
		val := fmt.Sprintf("val-%05d", i)
		if err := db.Put(key, []byte(val)); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
	}

	// Delete some keys.
	for i := range 10 {
		key := fmt.Sprintf("key-%05d", i)
		if err := db.Delete(key); err != nil {
			t.Fatalf("Delete(%q): %v", key, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second session: reopen and verify.
	db2, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB (reopen): %v", err)
	}
	defer db2.Close()

	// Deleted keys should not be found.
	for i := range 10 {
		key := fmt.Sprintf("key-%05d", i)
		if _, ok := db2.Get(key); ok {
			t.Fatalf("expected deleted key %q to not be found after reopen", key)
		}
	}

	// Remaining keys should be found.
	for i := 10; i < 50; i++ {
		key := fmt.Sprintf("key-%05d", i)
		val, ok := db2.Get(key)
		if !ok {
			t.Fatalf("expected key %q to be found after reopen", key)
		}
		want := fmt.Sprintf("val-%05d", i)
		if string(val) != want {
			t.Fatalf("key %q: got %q, want %q", key, val, want)
		}
	}
}

func TestDBCompactionIntegration(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir
	cfg.MaxMemBytes = 200
	cfg.Compaction.L0CompactionThreshold = 4
	cfg.Compaction.MaxOutputFileSize = 2048

	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	// Insert many keys to trigger multiple flushes and compactions.
	expected := make(map[string]string)
	for i := range 500 {
		key := fmt.Sprintf("key-%06d", i)
		val := fmt.Sprintf("val-%06d", i)
		if err := db.Put(key, []byte(val)); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
		expected[key] = val
	}

	// Delete some keys.
	deleted := make(map[string]bool)
	for i := 0; i < 500; i += 5 {
		key := fmt.Sprintf("key-%06d", i)
		if err := db.Delete(key); err != nil {
			t.Fatalf("Delete(%q): %v", key, err)
		}
		deleted[key] = true
		delete(expected, key)
	}

	// Run compaction deterministically.
	if err := db.RunCompaction(); err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	// Verify all remaining keys.
	for key, want := range expected {
		val, ok := db.Get(key)
		if !ok {
			t.Fatalf("expected key %q to be found", key)
		}
		if string(val) != want {
			t.Fatalf("key %q: got %q, want %q", key, val, want)
		}
	}

	// Verify deleted keys.
	for key := range deleted {
		if _, ok := db.Get(key); ok {
			t.Fatalf("expected deleted key %q to not be found", key)
		}
	}

	db.Close()
}

func TestDBEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir
	cfg.MaxMemBytes = 500
	cfg.Compaction.L0CompactionThreshold = 3
	cfg.Compaction.MaxOutputFileSize = 4096

	// 1. Open DB, insert 10,000 random key-value pairs.
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	expected := make(map[string]string)
	keys := make([]string, 0, 10000)

	for i := range 10000 {
		key := fmt.Sprintf("k%08d", i)
		val := fmt.Sprintf("v%08d", rng.Intn(100000))
		if err := db.Put(key, []byte(val)); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
		expected[key] = val
		keys = append(keys, key)
	}

	// 2. Delete 1,000 of them.
	rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	deletedKeys := keys[:1000]
	for _, key := range deletedKeys {
		if err := db.Delete(key); err != nil {
			t.Fatalf("Delete(%q): %v", key, err)
		}
		delete(expected, key)
	}

	// 3. Verify all remaining keys readable, deleted keys return not-found.
	for key, want := range expected {
		val, ok := db.Get(key)
		if !ok {
			t.Fatalf("expected key %q to be found", key)
		}
		if string(val) != want {
			t.Fatalf("key %q: got %q, want %q", key, val, want)
		}
	}
	for _, key := range deletedKeys {
		if _, ok := db.Get(key); ok {
			t.Fatalf("expected deleted key %q to not be found", key)
		}
	}

	// 4. Close and reopen DB, verify same results.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB (reopen): %v", err)
	}

	for key, want := range expected {
		val, ok := db2.Get(key)
		if !ok {
			t.Fatalf("[reopen] expected key %q to be found", key)
		}
		if string(val) != want {
			t.Fatalf("[reopen] key %q: got %q, want %q", key, val, want)
		}
	}
	for _, key := range deletedKeys {
		if _, ok := db2.Get(key); ok {
			t.Fatalf("[reopen] expected deleted key %q to not be found", key)
		}
	}

	// 5. Trigger compaction rounds and verify data integrity.
	if err := db2.RunCompaction(); err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	for key, want := range expected {
		val, ok := db2.Get(key)
		if !ok {
			t.Fatalf("[post-compact] expected key %q to be found", key)
		}
		if string(val) != want {
			t.Fatalf("[post-compact] key %q: got %q, want %q", key, val, want)
		}
	}

	if err := db2.Close(); err != nil {
		t.Fatalf("Close (2): %v", err)
	}
}

func TestDBReopenNoFileCollision(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir
	cfg.MaxMemBytes = 100 // small to force SSTables

	// Session 1: write data, creating SSTables.
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	for i := range 50 {
		if err := db.Put(fmt.Sprintf("k%04d", i), []byte("v1")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Collect existing SSTable filenames.
	existing, _ := filepath.Glob(filepath.Join(dir, "sstable_*.sst"))
	existingSet := make(map[string]bool)
	for _, p := range existing {
		existingSet[filepath.Base(p)] = true
	}

	// Session 2: reopen, write more data.
	db2, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB (reopen): %v", err)
	}
	for i := 50; i < 100; i++ {
		if err := db2.Put(fmt.Sprintf("k%04d", i), []byte("v2")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := db2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// All new SSTables should have unique names.
	allFiles, _ := filepath.Glob(filepath.Join(dir, "sstable_*.sst"))
	seen := make(map[string]bool)
	for _, p := range allFiles {
		base := filepath.Base(p)
		if seen[base] {
			t.Fatalf("duplicate SSTable filename: %s", base)
		}
		seen[base] = true
	}

	// Verify all data is readable.
	db3, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB (verify): %v", err)
	}
	defer db3.Close()
	for i := range 100 {
		key := fmt.Sprintf("k%04d", i)
		if _, ok := db3.Get(key); !ok {
			t.Fatalf("expected key %q to be found after reopen", key)
		}
	}
}

func TestSSTableWriterSetSeq(t *testing.T) {
	dir := t.TempDir()
	w := &SSTableWriter{Dir: dir}
	w.SetSeq(100)

	sl := NewSkipList(4096)
	sl.Insert("a", []byte("1"))

	info, err := w.WriteFromIterator(sl.NewIterator(), sl.bloom)
	if err != nil {
		t.Fatalf("WriteFromIterator: %v", err)
	}

	base := filepath.Base(info.Path)
	if !strings.HasPrefix(base, "sstable_000101") {
		t.Fatalf("expected file starting with sstable_000101, got %s", base)
	}
}

func TestDBPutAfterClose(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir

	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	db.Close()

	err = db.Put("key", []byte("val"))
	if !errors.Is(err, ErrDBClosed) {
		t.Fatalf("expected ErrDBClosed, got %v", err)
	}
}

func TestDBDeleteAfterClose(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir

	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	db.Close()

	err = db.Delete("key")
	if !errors.Is(err, ErrDBClosed) {
		t.Fatalf("expected ErrDBClosed, got %v", err)
	}
}

func TestDBPutNilValueReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir

	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// nil value should be rejected.
	err = db.Put("key", nil)
	if !errors.Is(err, ErrNilValue) {
		t.Fatalf("expected ErrNilValue, got %v", err)
	}

	// Empty value (not nil) should succeed.
	if err := db.Put("key", []byte{}); err != nil {
		t.Fatalf("Put(empty): %v", err)
	}

	val, ok := db.Get("key")
	if !ok {
		t.Fatal("expected key to be found")
	}
	if len(val) != 0 {
		t.Fatalf("expected empty value, got %q", val)
	}
}

func TestDBConcurrentPutFlush(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultDBConfig()
	cfg.Dir = dir
	cfg.MaxMemBytes = 100 // small to force many rotations

	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	const goroutines = 8
	const keysPerGoroutine = 100
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for g := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range keysPerGoroutine {
				key := fmt.Sprintf("g%02d-k%04d", id, i)
				if err := db.Put(key, []byte("val")); err != nil {
					errCh <- fmt.Errorf("goroutine %d: Put(%q): %w", id, key, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	// Verify no duplicate SSTable paths in L0.
	l0 := db.levels.GetLevel(0)
	paths := make(map[string]bool)
	for _, meta := range l0 {
		if paths[meta.Path] {
			t.Fatalf("duplicate L0 SSTable path: %s", meta.Path)
		}
		paths[meta.Path] = true
	}

	// Verify all keys are readable.
	for g := range goroutines {
		for i := range keysPerGoroutine {
			key := fmt.Sprintf("g%02d-k%04d", g, i)
			if _, ok := db.Get(key); !ok {
				t.Fatalf("expected key %q to be found", key)
			}
		}
	}

	db.Close()
}
