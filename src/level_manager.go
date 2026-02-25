package main

import (
	"sort"
	"sync"
)

// SSTableMeta holds metadata for a single SSTable file managed by the LevelManager.
type SSTableMeta struct {
	Path     string
	Level    int
	MinKey   string
	MaxKey   string
	FileSize int64
	SeqNum   uint64 // monotonic sequence number for ordering
}

// CompactionConfig controls leveled compaction behaviour.
type CompactionConfig struct {
	L0CompactionThreshold int   // number of L0 files that triggers compaction (default: 4)
	LevelSizeMultiplier   int   // size ratio between adjacent levels (default: 10)
	L1MaxBytes            int64 // max total bytes for L1 (default: 10MB)
	MaxOutputFileSize     int64 // max size of a single compaction output file (default: 2MB)
	MaxLevels             int   // maximum number of levels (default: 7)
}

// DefaultCompactionConfig returns sensible defaults.
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		L0CompactionThreshold: 4,
		LevelSizeMultiplier:   10,
		L1MaxBytes:            10 * 1024 * 1024,
		MaxOutputFileSize:     2 * 1024 * 1024,
		MaxLevels:             7,
	}
}

// LevelManager tracks which SSTables belong to which level.
type LevelManager struct {
	mu     sync.RWMutex
	levels map[int][]*SSTableMeta
	config CompactionConfig
	nextSeq uint64 // next sequence number to assign
}

// NewLevelManager creates an empty level manager with the given config.
func NewLevelManager(cfg CompactionConfig) *LevelManager {
	return &LevelManager{
		levels: make(map[int][]*SSTableMeta),
		config: cfg,
	}
}

// NextSeqNum returns and increments the sequence number counter.
func (lm *LevelManager) NextSeqNum() uint64 {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	seq := lm.nextSeq
	lm.nextSeq++
	return seq
}

// SetNextSeqNum sets the next sequence number (used during manifest replay).
func (lm *LevelManager) SetNextSeqNum(seq uint64) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if seq > lm.nextSeq {
		lm.nextSeq = seq
	}
}

// AddSSTable registers an SSTable in the given level.
// L1+ SSTables are kept sorted by MinKey.
func (lm *LevelManager) AddSSTable(meta *SSTableMeta) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.levels[meta.Level] = append(lm.levels[meta.Level], meta)
	if meta.Level > 0 {
		sort.Slice(lm.levels[meta.Level], func(i, j int) bool {
			return lm.levels[meta.Level][i].MinKey < lm.levels[meta.Level][j].MinKey
		})
	}
}

// RemoveSSTable removes an SSTable from its level.
func (lm *LevelManager) RemoveSSTable(meta *SSTableMeta) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	files := lm.levels[meta.Level]
	for i, f := range files {
		if f.Path == meta.Path {
			lm.levels[meta.Level] = append(files[:i], files[i+1:]...)
			return
		}
	}
}

// GetLevel returns a copy of all SSTables at the given level.
func (lm *LevelManager) GetLevel(n int) []*SSTableMeta {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	files := lm.levels[n]
	result := make([]*SSTableMeta, len(files))
	copy(result, files)
	return result
}

// GetOverlapping returns SSTables at the given level whose key range
// overlaps [minKey, maxKey].
func (lm *LevelManager) GetOverlapping(level int, minKey, maxKey string) []*SSTableMeta {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	var result []*SSTableMeta
	for _, f := range lm.levels[level] {
		// Two ranges overlap unless one ends before the other starts.
		if f.MinKey > maxKey || f.MaxKey < minKey {
			continue
		}
		result = append(result, f)
	}
	return result
}

// LevelSize returns the total file size in bytes of all SSTables at a level.
func (lm *LevelManager) LevelSize(level int) int64 {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	var total int64
	for _, f := range lm.levels[level] {
		total += f.FileSize
	}
	return total
}

// MaxLevelSize returns the maximum allowed total size for a level.
// L0 uses file count, not size, so this returns 0 for L0.
func (lm *LevelManager) MaxLevelSize(level int) int64 {
	if level == 0 {
		return 0
	}
	size := lm.config.L1MaxBytes
	for i := 1; i < level; i++ {
		size *= int64(lm.config.LevelSizeMultiplier)
	}
	return size
}

// NeedsCompaction returns the level that needs compaction, or -1 if none.
// Checks L0 file count threshold first, then L1+ size thresholds.
func (lm *LevelManager) NeedsCompaction() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	// Check L0 file count.
	if len(lm.levels[0]) >= lm.config.L0CompactionThreshold {
		return 0
	}

	// Check L1+ size thresholds.
	for level := 1; level < lm.config.MaxLevels-1; level++ {
		var totalSize int64
		for _, f := range lm.levels[level] {
			totalSize += f.FileSize
		}
		maxSize := lm.config.L1MaxBytes
		for i := 1; i < level; i++ {
			maxSize *= int64(lm.config.LevelSizeMultiplier)
		}
		if totalSize > maxSize {
			return level
		}
	}

	return -1
}

// PickCompactionInput selects SSTables for compaction from the given level.
// L0: all files. Ln: the oldest SSTable by SeqNum.
func (lm *LevelManager) PickCompactionInput(level int) []*SSTableMeta {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	files := lm.levels[level]
	if len(files) == 0 {
		return nil
	}

	if level == 0 {
		result := make([]*SSTableMeta, len(files))
		copy(result, files)
		return result
	}

	// Pick the oldest SSTable by SeqNum.
	oldest := files[0]
	for _, f := range files[1:] {
		if f.SeqNum < oldest.SeqNum {
			oldest = f
		}
	}
	return []*SSTableMeta{oldest}
}

// HasSStablesBelow reports whether any levels below the given level have SSTables.
func (lm *LevelManager) HasSStablesBelow(level int) bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	for l := level + 1; l < lm.config.MaxLevels; l++ {
		if len(lm.levels[l]) > 0 {
			return true
		}
	}
	return false
}

// LevelCount returns the number of SSTables at a level.
func (lm *LevelManager) LevelCount(level int) int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return len(lm.levels[level])
}
