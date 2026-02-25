package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// DBConfig holds configuration for the database.
type DBConfig struct {
	Dir         string
	MaxMemBytes int64 // max size per memtable (default: 4MB)
	Compaction  CompactionConfig
}

// DefaultDBConfig returns sensible defaults. Dir must be set by the caller.
func DefaultDBConfig() DBConfig {
	return DBConfig{
		MaxMemBytes: 4 * 1024 * 1024,
		Compaction:  DefaultCompactionConfig(),
	}
}

// DB is the top-level database struct tying together memtable, levels, compaction, and manifest.
type DB struct {
	mu       sync.RWMutex // protects level state during reads vs compaction
	memtable *MemTable
	levels   *LevelManager
	writer   *SSTableWriter
	compact  *Compactor
	manifest *Manifest
	dir      string
	config   DBConfig
}

// OpenDB opens (or creates) a database at the configured directory.
// It replays the manifest to restore level state, then starts the background compactor.
func OpenDB(config DBConfig) (*DB, error) {
	if config.Dir == "" {
		return nil, fmt.Errorf("db: Dir must be set")
	}
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("db: mkdir: %w", err)
	}
	if config.MaxMemBytes == 0 {
		config.MaxMemBytes = 4 * 1024 * 1024
	}

	manifestPath := filepath.Join(config.Dir, "MANIFEST")
	mf, err := OpenManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("db: open manifest: %w", err)
	}

	levels, err := mf.Replay(config.Compaction)
	if err != nil {
		mf.Close()
		return nil, fmt.Errorf("db: replay manifest: %w", err)
	}

	writer := &SSTableWriter{Dir: config.Dir}

	db := &DB{
		memtable: NewMemTable(config.MaxMemBytes),
		levels:   levels,
		writer:   writer,
		manifest: mf,
		dir:      config.Dir,
		config:   config,
	}

	compactor := NewCompactor(levels, writer, mf, config.Dir)
	compactor.dbMu = &db.mu
	compactor.Start()
	db.compact = compactor

	return db, nil
}

// Put inserts or updates a key-value pair.
// If the memtable rotates, the immutable is flushed to L0.
func (db *DB) Put(key string, value []byte) error {
	if err := db.memtable.Insert(key, value); err != nil {
		return fmt.Errorf("db: put: %w", err)
	}
	return db.flushImmutables()
}

// Get retrieves the value for a key.
// Search order: active memtable → immutable memtables → L0 (newest first) → L1 → L2 → ...
// Returns (nil, false) if the key is not found or was deleted.
func (db *DB) Get(key string) ([]byte, bool) {
	// 1. Check memtable (active + immutables).
	// Returns (val, true) for live entries, (nil, true) for tombstones, (nil, false) for not found.
	if val, ok := db.memtable.Search(key); ok {
		if val == nil {
			return nil, false // tombstone — key is deleted
		}
		return val, true
	}

	// 2. Check SSTables level by level.
	// Hold the read lock to prevent compaction from deleting files mid-search.
	db.mu.RLock()
	defer db.mu.RUnlock()

	for level := 0; level < db.config.Compaction.MaxLevels; level++ {
		files := db.levels.GetLevel(level)
		if len(files) == 0 {
			continue
		}

		if level == 0 {
			// L0 files can overlap; search newest first.
			sort.Slice(files, func(i, j int) bool {
				return files[i].SeqNum > files[j].SeqNum
			})
			for _, meta := range files {
				val, found, err := searchSSTable(meta, key)
				if err != nil {
					continue
				}
				if found {
					if val == nil {
						return nil, false // tombstone
					}
					return val, true
				}
			}
		} else {
			// L1+ files are sorted and non-overlapping. Binary search.
			idx := sort.Search(len(files), func(i int) bool {
				return files[i].MaxKey >= key
			})
			if idx < len(files) && files[idx].MinKey <= key {
				val, found, err := searchSSTable(files[idx], key)
				if err != nil {
					continue
				}
				if found {
					if val == nil {
						return nil, false // tombstone
					}
					return val, true
				}
			}
		}
	}

	return nil, false
}

// Delete marks a key as deleted by inserting a tombstone.
func (db *DB) Delete(key string) error {
	if err := db.memtable.Delete(key); err != nil {
		return fmt.Errorf("db: delete: %w", err)
	}
	return db.flushImmutables()
}

// Close stops the compactor, flushes remaining data, and closes the manifest.
func (db *DB) Close() error {
	db.compact.Stop()

	// Rotate the active memtable to immutable so it gets flushed.
	db.memtable.RotateActive()

	// Flush any remaining immutable memtables.
	if err := db.flushImmutables(); err != nil {
		db.manifest.Close()
		return fmt.Errorf("db: final flush: %w", err)
	}

	return db.manifest.Close()
}

// flushImmutables flushes all pending immutable memtables to L0 SSTables.
func (db *DB) flushImmutables() error {
	for {
		imm := db.memtable.GetImmutables()
		if len(imm) == 0 {
			return nil
		}

		// Flush the oldest immutable.
		oldest := imm[len(imm)-1]
		info, err := db.writer.WriteFromIterator(oldest.NewIterator(), oldest.bloom)
		if err != nil {
			return fmt.Errorf("db: flush: %w", err)
		}

		seq := db.levels.NextSeqNum()
		meta := &SSTableMeta{
			Path:     info.Path,
			Level:    0,
			MinKey:   info.MinKey,
			MaxKey:   info.MaxKey,
			FileSize: info.FileSize,
			SeqNum:   seq,
		}

		// Record in manifest.
		if err := db.manifest.AppendEdit(VersionEdit{
			AddedFiles: []ManifestFileEntry{{
				Path:     meta.Path,
				Level:    0,
				MinKey:   meta.MinKey,
				MaxKey:   meta.MaxKey,
				FileSize: meta.FileSize,
				SeqNum:   meta.SeqNum,
			}},
		}); err != nil {
			return fmt.Errorf("db: manifest flush: %w", err)
		}

		db.levels.AddSSTable(meta)
		db.memtable.RemoveImmutable(oldest)
	}
}

// searchSSTable opens an SSTable, searches for a key, and closes it.
// Returns (nil, false, nil) if the file no longer exists (deleted by compaction).
func searchSSTable(meta *SSTableMeta, key string) ([]byte, bool, error) {
	r, err := OpenSSTable(meta.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer r.Close()
	return r.Search(key)
}
