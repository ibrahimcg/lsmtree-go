package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ManifestFileEntry describes an SSTable file entry in the manifest.
type ManifestFileEntry struct {
	Path     string `json:"path"`
	Level    int    `json:"level"`
	MinKey   string `json:"min_key"`
	MaxKey   string `json:"max_key"`
	FileSize int64  `json:"file_size"`
	SeqNum   uint64 `json:"seq_num"`
}

// VersionEdit records a single atomic change to the set of SSTables.
type VersionEdit struct {
	AddedFiles   []ManifestFileEntry `json:"added,omitempty"`
	RemovedFiles []ManifestFileEntry `json:"removed,omitempty"`
}

// Manifest is an append-only JSON-lines log of version edits.
type Manifest struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

// OpenManifest opens (or creates) a manifest file at the given path.
func OpenManifest(path string) (*Manifest, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("manifest: open: %w", err)
	}
	return &Manifest{f: f, path: path}, nil
}

// AppendEdit writes a single version edit as one JSON line and syncs.
func (m *Manifest) AppendEdit(edit VersionEdit) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := json.Marshal(edit)
	if err != nil {
		return fmt.Errorf("manifest: marshal: %w", err)
	}
	data = append(data, '\n')
	if _, err := m.f.Write(data); err != nil {
		return fmt.Errorf("manifest: write: %w", err)
	}
	return m.f.Sync()
}

// Replay reads the manifest from disk and reconstructs a LevelManager
// reflecting the current state of all SSTables.
func (m *Manifest) Replay(cfg CompactionConfig) (*LevelManager, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Seek to beginning for replay.
	if _, err := m.f.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("manifest: seek: %w", err)
	}

	lm := NewLevelManager(cfg)
	// Track active files by path for removal support.
	active := make(map[string]*SSTableMeta)
	var maxSeq uint64

	scanner := bufio.NewScanner(m.f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var edit VersionEdit
		if err := json.Unmarshal(line, &edit); err != nil {
			return nil, fmt.Errorf("manifest: unmarshal: %w", err)
		}
		for _, entry := range edit.RemovedFiles {
			if meta, ok := active[entry.Path]; ok {
				lm.RemoveSSTable(meta)
				delete(active, entry.Path)
			}
		}
		for _, entry := range edit.AddedFiles {
			meta := &SSTableMeta{
				Path:     entry.Path,
				Level:    entry.Level,
				MinKey:   entry.MinKey,
				MaxKey:   entry.MaxKey,
				FileSize: entry.FileSize,
				SeqNum:   entry.SeqNum,
			}
			lm.AddSSTable(meta)
			active[entry.Path] = meta
			if entry.SeqNum >= maxSeq {
				maxSeq = entry.SeqNum + 1
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("manifest: scan: %w", err)
	}

	lm.SetNextSeqNum(maxSeq)

	// Seek back to end for future appends.
	if _, err := m.f.Seek(0, 2); err != nil {
		return nil, fmt.Errorf("manifest: seek end: %w", err)
	}

	return lm, nil
}

// Close closes the manifest file.
func (m *Manifest) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.f.Close()
}
