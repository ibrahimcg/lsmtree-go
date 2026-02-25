package main

import "sync"

type MemTable struct {
	mu         sync.Mutex
	active     *SkipList
	immutables []*SkipList
	maxBytes   int64
}

func NewMemTable(maxBytes int64) *MemTable {
	return &MemTable{
		active:   NewSkipList(maxBytes),
		maxBytes: maxBytes,
	}
}

func (m *MemTable) Insert(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.active.Insert(key, value)
	if err == ErrSkipListFull {
		m.active.MarkImmutable()
		m.immutables = append([]*SkipList{m.active}, m.immutables...)
		m.active = NewSkipList(m.maxBytes)
		return m.active.Insert(key, value)
	}
	return err
}

// Search looks up a key in the active memtable and immutables.
// Returns (value, true) if found, (nil, true) if tombstone found, (nil, false) if not found.
func (m *MemTable) Search(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if val, ok := m.active.Search(key); ok {
		return val, true // val==nil means tombstone
	}
	for _, sl := range m.immutables {
		if val, ok := sl.Search(key); ok {
			return val, true // val==nil means tombstone
		}
	}
	return nil, false
}

func (m *MemTable) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.active.Insert(key, nil) // tombstone
	if err == ErrSkipListFull {
		m.active.MarkImmutable()
		m.immutables = append([]*SkipList{m.active}, m.immutables...)
		m.active = NewSkipList(m.maxBytes)
		return m.active.Insert(key, nil)
	}
	return err
}

// RotateActive marks the current active memtable as immutable and creates a new one.
// This is used during shutdown to ensure the active memtable is flushed.
func (m *MemTable) RotateActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active.Len() == 0 {
		return // nothing to flush
	}
	m.active.MarkImmutable()
	m.immutables = append([]*SkipList{m.active}, m.immutables...)
	m.active = NewSkipList(m.maxBytes)
}

func (m *MemTable) GetImmutables() []*SkipList {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*SkipList, len(m.immutables))
	copy(result, m.immutables)
	return result
}

func (m *MemTable) RemoveImmutable(sl *SkipList) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.immutables {
		if s == sl {
			m.immutables = append(m.immutables[:i], m.immutables[i+1:]...)
			return
		}
	}
}
