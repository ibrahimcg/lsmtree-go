package main

import (
	"path/filepath"
	"testing"
)

func TestManifestAppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MANIFEST")

	m, err := OpenManifest(path)
	if err != nil {
		t.Fatalf("OpenManifest: %v", err)
	}

	edit := VersionEdit{
		AddedFiles: []ManifestFileEntry{
			{Path: "sst_001.sst", Level: 0, MinKey: "a", MaxKey: "d", FileSize: 1000, SeqNum: 0},
			{Path: "sst_002.sst", Level: 0, MinKey: "e", MaxKey: "h", FileSize: 2000, SeqNum: 1},
		},
	}
	if err := m.AppendEdit(edit); err != nil {
		t.Fatalf("AppendEdit: %v", err)
	}

	cfg := DefaultCompactionConfig()
	lm, _, err := m.Replay(cfg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	files := lm.GetLevel(0)
	if len(files) != 2 {
		t.Fatalf("expected 2 L0 files, got %d", len(files))
	}

	m.Close()
}

func TestManifestReopenAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MANIFEST")

	// First session: write edits.
	m, err := OpenManifest(path)
	if err != nil {
		t.Fatalf("OpenManifest: %v", err)
	}
	m.AppendEdit(VersionEdit{
		AddedFiles: []ManifestFileEntry{
			{Path: "sst_001.sst", Level: 0, MinKey: "a", MaxKey: "z", FileSize: 500, SeqNum: 0},
		},
	})
	m.Close()

	// Second session: reopen and replay.
	m2, err := OpenManifest(path)
	if err != nil {
		t.Fatalf("OpenManifest (reopen): %v", err)
	}
	defer m2.Close()

	lm, _, err := m2.Replay(DefaultCompactionConfig())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	files := lm.GetLevel(0)
	if len(files) != 1 {
		t.Fatalf("expected 1 L0 file after reopen, got %d", len(files))
	}
	if files[0].Path != "sst_001.sst" {
		t.Fatalf("unexpected path: %q", files[0].Path)
	}
}

func TestManifestAddRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MANIFEST")

	m, err := OpenManifest(path)
	if err != nil {
		t.Fatalf("OpenManifest: %v", err)
	}

	// Add two files.
	m.AppendEdit(VersionEdit{
		AddedFiles: []ManifestFileEntry{
			{Path: "sst_001.sst", Level: 1, MinKey: "a", MaxKey: "m", FileSize: 100, SeqNum: 0},
			{Path: "sst_002.sst", Level: 1, MinKey: "n", MaxKey: "z", FileSize: 200, SeqNum: 1},
		},
	})

	// Remove one, add a replacement.
	m.AppendEdit(VersionEdit{
		RemovedFiles: []ManifestFileEntry{
			{Path: "sst_001.sst", Level: 1},
		},
		AddedFiles: []ManifestFileEntry{
			{Path: "sst_003.sst", Level: 2, MinKey: "a", MaxKey: "m", FileSize: 150, SeqNum: 2},
		},
	})

	cfg := DefaultCompactionConfig()
	lm, _, err := m.Replay(cfg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	m.Close()

	// L1 should have 1 file (sst_002.sst).
	l1 := lm.GetLevel(1)
	if len(l1) != 1 {
		t.Fatalf("expected 1 L1 file, got %d", len(l1))
	}
	if l1[0].Path != "sst_002.sst" {
		t.Fatalf("L1 file: got %q, want sst_002.sst", l1[0].Path)
	}

	// L2 should have 1 file (sst_003.sst).
	l2 := lm.GetLevel(2)
	if len(l2) != 1 {
		t.Fatalf("expected 1 L2 file, got %d", len(l2))
	}
	if l2[0].Path != "sst_003.sst" {
		t.Fatalf("L2 file: got %q, want sst_003.sst", l2[0].Path)
	}

	// SeqNum should be set past the max used.
	nextSeq := lm.NextSeqNum()
	if nextSeq < 3 {
		t.Fatalf("expected nextSeq >= 3, got %d", nextSeq)
	}
}
