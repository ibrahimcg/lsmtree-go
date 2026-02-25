package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// helper: create an SSTable with the given key-value pairs and register in level manager.
func createSSTable(t *testing.T, dir string, w *SSTableWriter, lm *LevelManager, level int, kvs map[string][]byte) *SSTableMeta {
	t.Helper()
	sl := NewSkipList(0)
	for k, v := range kvs {
		sl.Insert(k, v)
	}
	sl.MarkImmutable()

	info, err := w.WriteFromIterator(sl.NewIterator(), nil)
	if err != nil {
		t.Fatalf("WriteFromIterator: %v", err)
	}

	seq := lm.NextSeqNum()
	meta := &SSTableMeta{
		Path:     info.Path,
		Level:    level,
		MinKey:   info.MinKey,
		MaxKey:   info.MaxKey,
		FileSize: info.FileSize,
		SeqNum:   seq,
	}
	lm.AddSSTable(meta)
	return meta
}

func TestCompactionL0ToL1(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 2
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	c := NewCompactor(lm, w, nil, dir)

	// Create two overlapping L0 SSTables.
	createSSTable(t, dir, w, lm, 0, map[string][]byte{
		"a": []byte("old-a"),
		"b": []byte("old-b"),
	})
	createSSTable(t, dir, w, lm, 0, map[string][]byte{
		"a": []byte("new-a"), // overwrites old-a
		"c": []byte("new-c"),
	})

	if lm.LevelCount(0) != 2 {
		t.Fatalf("expected 2 L0 files, got %d", lm.LevelCount(0))
	}

	err := c.CompactLevel(0)
	if err != nil {
		t.Fatalf("CompactLevel: %v", err)
	}

	// L0 should be empty, L1 should have files.
	if lm.LevelCount(0) != 0 {
		t.Fatalf("expected 0 L0 files after compaction, got %d", lm.LevelCount(0))
	}
	if lm.LevelCount(1) == 0 {
		t.Fatal("expected L1 files after compaction")
	}

	// Verify "a" has the new value (from higher SeqNum SSTable).
	l1Files := lm.GetLevel(1)
	found := false
	for _, meta := range l1Files {
		r, err := OpenSSTable(meta.Path)
		if err != nil {
			t.Fatalf("OpenSSTable: %v", err)
		}
		val, ok, err := r.Search("a")
		r.Close()
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if ok {
			found = true
			if string(val) != "new-a" {
				t.Fatalf("key 'a': got %q, want 'new-a'", val)
			}
		}
	}
	if !found {
		t.Fatal("key 'a' not found in L1 after compaction")
	}
}

func TestCompactionL1ToL2(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L1MaxBytes = 100 // very small to trigger L1 compaction
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	c := NewCompactor(lm, w, nil, dir)

	// Create L1 SSTable.
	createSSTable(t, dir, w, lm, 1, map[string][]byte{
		"x": []byte("x-val"),
		"y": []byte("y-val"),
	})

	if lm.NeedsCompaction() != 1 {
		t.Fatalf("expected L1 to need compaction, got %d", lm.NeedsCompaction())
	}

	err := c.CompactLevel(1)
	if err != nil {
		t.Fatalf("CompactLevel: %v", err)
	}

	if lm.LevelCount(1) != 0 {
		t.Fatalf("expected 0 L1 files, got %d", lm.LevelCount(1))
	}
	if lm.LevelCount(2) == 0 {
		t.Fatal("expected L2 files after compaction")
	}
}

func TestCompactionTombstoneRetainedNonBottom(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 1
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	c := NewCompactor(lm, w, nil, dir)

	// Create L0 SSTable with tombstone.
	createSSTable(t, dir, w, lm, 0, map[string][]byte{
		"del": nil, // tombstone
		"keep": []byte("val"),
	})

	// Create an L2 SSTable so L1 is NOT bottommost.
	createSSTable(t, dir, w, lm, 2, map[string][]byte{
		"deep": []byte("deep-val"),
	})

	err := c.CompactLevel(0)
	if err != nil {
		t.Fatalf("CompactLevel: %v", err)
	}

	// Tombstone should be retained in L1 (not bottommost).
	l1Files := lm.GetLevel(1)
	tombstoneFound := false
	for _, meta := range l1Files {
		r, err := OpenSSTable(meta.Path)
		if err != nil {
			t.Fatalf("OpenSSTable: %v", err)
		}
		val, found, err := r.Search("del")
		r.Close()
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if found && val == nil {
			tombstoneFound = true
		}
	}
	if !tombstoneFound {
		t.Fatal("tombstone should be retained at non-bottommost level")
	}
}

func TestCompactionTombstoneDroppedAtBottom(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 1
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	c := NewCompactor(lm, w, nil, dir)

	// Create L0 SSTable with tombstone. No deeper levels → L1 is bottommost.
	createSSTable(t, dir, w, lm, 0, map[string][]byte{
		"del":  nil, // tombstone
		"keep": []byte("val"),
	})

	err := c.CompactLevel(0)
	if err != nil {
		t.Fatalf("CompactLevel: %v", err)
	}

	// Tombstone should be dropped in L1 (bottommost).
	l1Files := lm.GetLevel(1)
	for _, meta := range l1Files {
		r, err := OpenSSTable(meta.Path)
		if err != nil {
			t.Fatalf("OpenSSTable: %v", err)
		}
		_, found, err := r.Search("del")
		r.Close()
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if found {
			t.Fatal("tombstone should be dropped at bottommost level")
		}
	}
}

func TestCompactionOutputSplitting(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 1
	cfg.MaxOutputFileSize = 500 // very small to force splitting
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	c := NewCompactor(lm, w, nil, dir)

	// Create L0 SSTable with many entries.
	kvs := make(map[string][]byte)
	for i := range 100 {
		kvs[fmt.Sprintf("key-%05d", i)] = []byte(fmt.Sprintf("val-%05d-with-extra-padding", i))
	}
	createSSTable(t, dir, w, lm, 0, kvs)

	err := c.CompactLevel(0)
	if err != nil {
		t.Fatalf("CompactLevel: %v", err)
	}

	l1Count := lm.LevelCount(1)
	if l1Count < 2 {
		t.Fatalf("expected multiple L1 output files, got %d", l1Count)
	}
}

func TestCompactionSupersededValuesDropped(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 2
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	c := NewCompactor(lm, w, nil, dir)

	// Two L0 SSTables with same keys, different values.
	createSSTable(t, dir, w, lm, 0, map[string][]byte{
		"k": []byte("old"),
	})
	createSSTable(t, dir, w, lm, 0, map[string][]byte{
		"k": []byte("new"),
	})

	err := c.CompactLevel(0)
	if err != nil {
		t.Fatalf("CompactLevel: %v", err)
	}

	// Only "new" should survive.
	l1Files := lm.GetLevel(1)
	for _, meta := range l1Files {
		r, err := OpenSSTable(meta.Path)
		if err != nil {
			t.Fatalf("OpenSSTable: %v", err)
		}
		it := NewSSTableIterator(r)
		for it.Valid() {
			if it.Key() == "k" && string(it.Value()) == "old" {
				r.Close()
				t.Fatal("superseded value 'old' should have been dropped")
			}
			it.Next()
		}
		r.Close()
	}
}

func TestCompactionOldFilesDeleted(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 2
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	c := NewCompactor(lm, w, nil, dir)

	m1 := createSSTable(t, dir, w, lm, 0, map[string][]byte{"a": []byte("1")})
	m2 := createSSTable(t, dir, w, lm, 0, map[string][]byte{"b": []byte("2")})

	// Files should exist before compaction.
	for _, m := range []*SSTableMeta{m1, m2} {
		if _, err := os.Stat(m.Path); os.IsNotExist(err) {
			t.Fatalf("expected file %q to exist before compaction", m.Path)
		}
	}

	err := c.CompactLevel(0)
	if err != nil {
		t.Fatalf("CompactLevel: %v", err)
	}

	// Old files should be deleted.
	for _, m := range []*SSTableMeta{m1, m2} {
		if _, err := os.Stat(m.Path); !os.IsNotExist(err) {
			t.Fatalf("expected file %q to be deleted after compaction", m.Path)
		}
	}
}

func TestCompactionIntegration(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 4
	cfg.MaxOutputFileSize = 4096
	lm := NewLevelManager(cfg)
	w := &SSTableWriter{Dir: dir}
	mf, err := OpenManifest(filepath.Join(dir, "MANIFEST"))
	if err != nil {
		t.Fatalf("OpenManifest: %v", err)
	}
	defer mf.Close()
	c := NewCompactor(lm, w, mf, dir)

	// Insert many keys across multiple L0 SSTables.
	expected := make(map[string]string)
	for batch := range 6 {
		kvs := make(map[string][]byte)
		for i := range 50 {
			key := fmt.Sprintf("key-%03d-%03d", batch, i)
			val := fmt.Sprintf("val-%03d-%03d", batch, i)
			kvs[key] = []byte(val)
			expected[key] = val
		}
		meta := createSSTable(t, dir, w, lm, 0, kvs)

		// Also record in manifest.
		mf.AppendEdit(VersionEdit{
			AddedFiles: []ManifestFileEntry{{
				Path:     meta.Path,
				Level:    0,
				MinKey:   meta.MinKey,
				MaxKey:   meta.MaxKey,
				FileSize: meta.FileSize,
				SeqNum:   meta.SeqNum,
			}},
		})
	}

	// Compact until no more compaction needed.
	for {
		level := lm.NeedsCompaction()
		if level < 0 {
			break
		}
		if err := c.CompactLevel(level); err != nil {
			t.Fatalf("CompactLevel(%d): %v", level, err)
		}
	}

	// Verify all keys are readable from the resulting SSTables.
	found := make(map[string]string)
	for level := 0; level < cfg.MaxLevels; level++ {
		files := lm.GetLevel(level)
		for _, meta := range files {
			r, err := OpenSSTable(meta.Path)
			if err != nil {
				t.Fatalf("OpenSSTable(%q): %v", meta.Path, err)
			}
			it := NewSSTableIterator(r)
			for it.Valid() {
				found[it.Key()] = string(it.Value())
				it.Next()
			}
			r.Close()
		}
	}

	for k, want := range expected {
		got, ok := found[k]
		if !ok {
			t.Fatalf("key %q missing after compaction", k)
		}
		if got != want {
			t.Fatalf("key %q: got %q, want %q", k, got, want)
		}
	}
}
