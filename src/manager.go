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

func (m *MemTable) Search(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if val, ok := m.active.Search(key); ok {
		return val, true
	}
	for _, sl := range m.immutables {
		if val, ok := sl.Search(key); ok {
			return val, true
		}
	}
	return nil, false
}

func (m *MemTable) Delete(key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active.Delete(key)
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
