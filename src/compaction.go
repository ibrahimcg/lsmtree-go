package main

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// Compactor performs leveled compaction on SSTables.
type Compactor struct {
	levels   *LevelManager
	writer   *SSTableWriter
	manifest *Manifest
	dir      string
	dbMu     *sync.RWMutex // optional: held as write lock during level state + file deletion
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewCompactor creates a new compactor.
func NewCompactor(levels *LevelManager, writer *SSTableWriter, manifest *Manifest, dir string) *Compactor {
	return &Compactor{
		levels:   levels,
		writer:   writer,
		manifest: manifest,
		dir:      dir,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// CompactLevel performs one compaction from the given source level to level+1.
func (c *Compactor) CompactLevel(level int) error {
	// 1. Pick input SSTables from source level.
	inputs := c.levels.PickCompactionInput(level)
	if len(inputs) == 0 {
		return nil
	}

	// 2. Compute union key range of all inputs.
	minKey := inputs[0].MinKey
	maxKey := inputs[0].MaxKey
	for _, m := range inputs[1:] {
		if m.MinKey < minKey {
			minKey = m.MinKey
		}
		if m.MaxKey > maxKey {
			maxKey = m.MaxKey
		}
	}

	// 3. Find overlapping SSTables in target level.
	targetLevel := level + 1
	overlapping := c.levels.GetOverlapping(targetLevel, minKey, maxKey)

	// 4. Open all input + overlapping SSTables, create iterators.
	var readers []*SSTableReader
	var iters []Iterator
	defer func() {
		for _, r := range readers {
			r.Close()
		}
	}()

	// 5. Source level iterators first.
	// For L0, sort by SeqNum descending (newest first = highest priority).
	if level == 0 {
		sort.Slice(inputs, func(i, j int) bool {
			return inputs[i].SeqNum > inputs[j].SeqNum
		})
	}

	for _, meta := range inputs {
		r, err := OpenSSTable(meta.Path)
		if err != nil {
			return fmt.Errorf("compaction: open source %q: %w", meta.Path, err)
		}
		readers = append(readers, r)
		iters = append(iters, NewSSTableIterator(r))
	}

	// Then target level iterators (lower priority).
	for _, meta := range overlapping {
		r, err := OpenSSTable(meta.Path)
		if err != nil {
			return fmt.Errorf("compaction: open target %q: %w", meta.Path, err)
		}
		readers = append(readers, r)
		iters = append(iters, NewSSTableIterator(r))
	}

	// 6. Create merge iterator.
	merged := NewMergeIterator(iters)

	// 7. Determine if target level is bottommost.
	isBottommost := !c.levels.HasSStablesBelow(targetLevel)

	// 8. Write output SSTables, splitting at MaxOutputFileSize.
	maxOutputSize := c.levels.config.MaxOutputFileSize
	var outputInfos []SSTableInfo
	var outputMetas []*SSTableMeta

	for merged.Valid() {
		info, more, err := c.writer.WriteLimitedFromIterator(merged, maxOutputSize, isBottommost)
		if err != nil {
			return fmt.Errorf("compaction: write output: %w", err)
		}
		if info.Path == "" {
			// No entries written (all filtered out).
			if !more {
				break
			}
			continue
		}
		outputInfos = append(outputInfos, info)
		seq := c.levels.NextSeqNum()
		meta := &SSTableMeta{
			Path:     info.Path,
			Level:    targetLevel,
			MinKey:   info.MinKey,
			MaxKey:   info.MaxKey,
			FileSize: info.FileSize,
			SeqNum:   seq,
		}
		outputMetas = append(outputMetas, meta)
		if !more {
			break
		}
	}

	// 9. Build manifest edit.
	edit := VersionEdit{}
	for _, m := range inputs {
		edit.RemovedFiles = append(edit.RemovedFiles, ManifestFileEntry{
			Path:  m.Path,
			Level: m.Level,
		})
	}
	for _, m := range overlapping {
		edit.RemovedFiles = append(edit.RemovedFiles, ManifestFileEntry{
			Path:  m.Path,
			Level: m.Level,
		})
	}
	for _, m := range outputMetas {
		edit.AddedFiles = append(edit.AddedFiles, ManifestFileEntry{
			Path:     m.Path,
			Level:    m.Level,
			MinKey:   m.MinKey,
			MaxKey:   m.MaxKey,
			FileSize: m.FileSize,
			SeqNum:   m.SeqNum,
		})
	}

	// Write manifest edit.
	if c.manifest != nil {
		if err := c.manifest.AppendEdit(edit); err != nil {
			return fmt.Errorf("compaction: manifest: %w", err)
		}
	}

	// 10. Update LevelManager and delete old files atomically under write lock.
	// This prevents concurrent reads from seeing stale file references.
	if c.dbMu != nil {
		c.dbMu.Lock()
	}
	for _, m := range inputs {
		c.levels.RemoveSSTable(m)
	}
	for _, m := range overlapping {
		c.levels.RemoveSSTable(m)
	}
	for _, m := range outputMetas {
		c.levels.AddSSTable(m)
	}
	for _, m := range inputs {
		os.Remove(m.Path)
	}
	for _, m := range overlapping {
		os.Remove(m.Path)
	}
	if c.dbMu != nil {
		c.dbMu.Unlock()
	}

	return nil
}

// Start launches a background goroutine that checks for and performs compaction.
func (c *Compactor) Start() {
	go func() {
		defer close(c.doneCh)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				for {
					level := c.levels.NeedsCompaction()
					if level < 0 {
						break
					}
					if err := c.CompactLevel(level); err != nil {
						// In production, we'd log this. For now, just break.
						break
					}
				}
			}
		}
	}()
}

// Stop signals the compactor to stop and waits for it to finish.
func (c *Compactor) Stop() {
	close(c.stopCh)
	<-c.doneCh
}
