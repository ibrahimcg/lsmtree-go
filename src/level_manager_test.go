package main

import (
	"fmt"
	"sync"
	"testing"
)

func TestLevelManagerAddAndGet(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	m1 := &SSTableMeta{Path: "a.sst", Level: 0, MinKey: "a", MaxKey: "c", FileSize: 100, SeqNum: 0}
	m2 := &SSTableMeta{Path: "b.sst", Level: 0, MinKey: "d", MaxKey: "f", FileSize: 200, SeqNum: 1}
	lm.AddSSTable(m1)
	lm.AddSSTable(m2)

	files := lm.GetLevel(0)
	if len(files) != 2 {
		t.Fatalf("expected 2 L0 files, got %d", len(files))
	}
}

func TestLevelManagerRemove(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	m := &SSTableMeta{Path: "a.sst", Level: 1, MinKey: "a", MaxKey: "z", FileSize: 100, SeqNum: 0}
	lm.AddSSTable(m)
	lm.RemoveSSTable(m)

	files := lm.GetLevel(1)
	if len(files) != 0 {
		t.Fatalf("expected 0 files after remove, got %d", len(files))
	}
}

func TestLevelManagerL1Sorted(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	// Add in reverse order.
	lm.AddSSTable(&SSTableMeta{Path: "c.sst", Level: 1, MinKey: "m", MaxKey: "z", SeqNum: 2})
	lm.AddSSTable(&SSTableMeta{Path: "a.sst", Level: 1, MinKey: "a", MaxKey: "d", SeqNum: 0})
	lm.AddSSTable(&SSTableMeta{Path: "b.sst", Level: 1, MinKey: "e", MaxKey: "l", SeqNum: 1})

	files := lm.GetLevel(1)
	for i := 1; i < len(files); i++ {
		if files[i].MinKey < files[i-1].MinKey {
			t.Fatalf("L1 not sorted: %q after %q", files[i].MinKey, files[i-1].MinKey)
		}
	}
}

func TestLevelManagerOverlap(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	lm.AddSSTable(&SSTableMeta{Path: "a.sst", Level: 1, MinKey: "a", MaxKey: "d"})
	lm.AddSSTable(&SSTableMeta{Path: "b.sst", Level: 1, MinKey: "e", MaxKey: "h"})
	lm.AddSSTable(&SSTableMeta{Path: "c.sst", Level: 1, MinKey: "m", MaxKey: "z"})

	overlap := lm.GetOverlapping(1, "c", "f")
	if len(overlap) != 2 {
		t.Fatalf("expected 2 overlapping files, got %d", len(overlap))
	}

	// No overlap.
	overlap = lm.GetOverlapping(1, "i", "l")
	if len(overlap) != 0 {
		t.Fatalf("expected 0 overlapping files, got %d", len(overlap))
	}
}

func TestLevelManagerSize(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	lm.AddSSTable(&SSTableMeta{Path: "a.sst", Level: 1, MinKey: "a", MaxKey: "z", FileSize: 100})
	lm.AddSSTable(&SSTableMeta{Path: "b.sst", Level: 1, MinKey: "a", MaxKey: "z", FileSize: 200})

	if got := lm.LevelSize(1); got != 300 {
		t.Fatalf("expected size 300, got %d", got)
	}
}

func TestLevelManagerMaxLevelSize(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cfg.L1MaxBytes = 100
	cfg.LevelSizeMultiplier = 10
	lm := NewLevelManager(cfg)

	if got := lm.MaxLevelSize(0); got != 0 {
		t.Fatalf("L0 max size should be 0, got %d", got)
	}
	if got := lm.MaxLevelSize(1); got != 100 {
		t.Fatalf("L1 max size: got %d, want 100", got)
	}
	if got := lm.MaxLevelSize(2); got != 1000 {
		t.Fatalf("L2 max size: got %d, want 1000", got)
	}
	if got := lm.MaxLevelSize(3); got != 10000 {
		t.Fatalf("L3 max size: got %d, want 10000", got)
	}
}

func TestLevelManagerNeedsCompaction(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cfg.L0CompactionThreshold = 2
	cfg.L1MaxBytes = 100
	lm := NewLevelManager(cfg)

	// No compaction needed initially.
	if got := lm.NeedsCompaction(); got != -1 {
		t.Fatalf("expected -1, got %d", got)
	}

	// Add 2 L0 files → triggers L0 compaction.
	lm.AddSSTable(&SSTableMeta{Path: "a.sst", Level: 0, MinKey: "a", MaxKey: "z", FileSize: 50})
	lm.AddSSTable(&SSTableMeta{Path: "b.sst", Level: 0, MinKey: "a", MaxKey: "z", FileSize: 50})

	if got := lm.NeedsCompaction(); got != 0 {
		t.Fatalf("expected L0 compaction (0), got %d", got)
	}

	// Clear L0, overfill L1.
	lm.RemoveSSTable(&SSTableMeta{Path: "a.sst", Level: 0})
	lm.RemoveSSTable(&SSTableMeta{Path: "b.sst", Level: 0})
	lm.AddSSTable(&SSTableMeta{Path: "c.sst", Level: 1, MinKey: "a", MaxKey: "m", FileSize: 60})
	lm.AddSSTable(&SSTableMeta{Path: "d.sst", Level: 1, MinKey: "n", MaxKey: "z", FileSize: 60})

	if got := lm.NeedsCompaction(); got != 1 {
		t.Fatalf("expected L1 compaction (1), got %d", got)
	}
}

func TestLevelManagerPickCompactionInput(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	// L0: picks all files.
	lm.AddSSTable(&SSTableMeta{Path: "a.sst", Level: 0, MinKey: "a", MaxKey: "z", SeqNum: 0})
	lm.AddSSTable(&SSTableMeta{Path: "b.sst", Level: 0, MinKey: "a", MaxKey: "m", SeqNum: 1})

	input := lm.PickCompactionInput(0)
	if len(input) != 2 {
		t.Fatalf("L0: expected 2 files, got %d", len(input))
	}

	// L1: picks oldest by SeqNum.
	lm.AddSSTable(&SSTableMeta{Path: "c.sst", Level: 1, MinKey: "a", MaxKey: "d", SeqNum: 10})
	lm.AddSSTable(&SSTableMeta{Path: "d.sst", Level: 1, MinKey: "e", MaxKey: "z", SeqNum: 5})

	input = lm.PickCompactionInput(1)
	if len(input) != 1 {
		t.Fatalf("L1: expected 1 file, got %d", len(input))
	}
	if input[0].Path != "d.sst" {
		t.Fatalf("L1: expected oldest (d.sst), got %q", input[0].Path)
	}
}

func TestLevelManagerHasSStablesBelow(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	if lm.HasSStablesBelow(0) {
		t.Fatal("expected no SSTables below L0")
	}

	lm.AddSSTable(&SSTableMeta{Path: "a.sst", Level: 2, MinKey: "a", MaxKey: "z"})

	if !lm.HasSStablesBelow(1) {
		t.Fatal("expected SSTables below L1")
	}
	if lm.HasSStablesBelow(2) {
		t.Fatal("expected no SSTables below L2")
	}
}

func TestLevelManagerThreadSafety(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())
	var wg sync.WaitGroup

	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			meta := &SSTableMeta{
				Path:     fmt.Sprintf("file_%d.sst", i),
				Level:    i % 3,
				MinKey:   "a",
				MaxKey:   "z",
				FileSize: 100,
				SeqNum:   uint64(i),
			}
			lm.AddSSTable(meta)
			lm.GetLevel(i % 3)
			lm.GetOverlapping(i%3, "a", "m")
			lm.LevelSize(i % 3)
			lm.NeedsCompaction()
		}(i)
	}
	wg.Wait()
}

func TestLevelManagerSeqNum(t *testing.T) {
	lm := NewLevelManager(DefaultCompactionConfig())

	s1 := lm.NextSeqNum()
	s2 := lm.NextSeqNum()
	if s1 != 0 || s2 != 1 {
		t.Fatalf("expected seq 0, 1; got %d, %d", s1, s2)
	}

	lm.SetNextSeqNum(10)
	s3 := lm.NextSeqNum()
	if s3 != 10 {
		t.Fatalf("expected seq 10 after SetNextSeqNum, got %d", s3)
	}
}
