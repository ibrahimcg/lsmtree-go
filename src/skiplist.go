package main

import (
	"errors"
	"math/rand"
	"sync"
)

var ErrSkipListFull = errors.New("skiplist is full")
var ErrImmutable = errors.New("skiplist is immutable")

const (
	maxLevel    = 16
	probability = 0.5
)

type node struct {
	key     string
	value   []byte
	forward []*node
}

type SkipList struct {
	header    *node
	level     int
	size      int
	sizeBytes int64
	maxBytes  int64
	immutable bool
	bloom     *BloomFilter
	mu        sync.RWMutex
}

// NewSkipList creates a new skip list with a maximum byte capacity.
// Pass 0 for unlimited size.
func NewSkipList(maxBytes int64) *SkipList {
	var expectedItems int
	if maxBytes > 0 {
		expectedItems = int(maxBytes / 64)
		if expectedItems < 16 {
			expectedItems = 16
		}
	} else {
		expectedItems = 10000
	}

	return &SkipList{
		header:   &node{forward: make([]*node, maxLevel)},
		level:    0,
		maxBytes: maxBytes,
		bloom:    OptimalBloomFilter(expectedItems, 0.01),
	}
}

func entrySize(key string, value []byte) int64 {
	return int64(len(key)) + int64(len(value))
}

func (sl *SkipList) randomLevel() int {
	lvl := 0
	for lvl < maxLevel-1 && rand.Float64() < probability {
		lvl++
	}
	return lvl
}

func (sl *SkipList) Insert(key string, value []byte) error {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if sl.immutable {
		return ErrImmutable
	}

	update := make([]*node, maxLevel)
	current := sl.header

	for i := sl.level; i >= 0; i-- {
		for current.forward[i] != nil && current.forward[i].key < key {
			current = current.forward[i]
		}
		update[i] = current
	}

	next := current.forward[0]
	if next != nil && next.key == key {
		oldSize := entrySize(key, next.value)
		newSize := entrySize(key, value)
		diff := newSize - oldSize
		if diff > 0 && sl.maxBytes > 0 && sl.sizeBytes+diff > sl.maxBytes {
			return ErrSkipListFull
		}
		sl.sizeBytes += diff
		next.value = value
		sl.bloom.Add(key)
		return nil
	}

	newEntrySize := entrySize(key, value)
	if sl.maxBytes > 0 && sl.sizeBytes+newEntrySize > sl.maxBytes {
		return ErrSkipListFull
	}

	newLevel := sl.randomLevel()
	if newLevel > sl.level {
		for i := sl.level + 1; i <= newLevel; i++ {
			update[i] = sl.header
		}
		sl.level = newLevel
	}

	n := &node{
		key:     key,
		value:   value,
		forward: make([]*node, newLevel+1),
	}
	for i := 0; i <= newLevel; i++ {
		n.forward[i] = update[i].forward[i]
		update[i].forward[i] = n
	}
	sl.sizeBytes += newEntrySize
	sl.size++
	sl.bloom.Add(key)
	return nil
}

func (sl *SkipList) IsFull() bool {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.maxBytes > 0 && sl.sizeBytes >= sl.maxBytes
}

func (sl *SkipList) SizeBytes() int64 {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.sizeBytes
}

func (sl *SkipList) Search(key string) ([]byte, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	if sl.immutable && !sl.bloom.MayContain(key) {
		return nil, false
	}

	current := sl.header
	for i := sl.level; i >= 0; i-- {
		for current.forward[i] != nil && current.forward[i].key < key {
			current = current.forward[i]
		}
	}

	next := current.forward[0]
	if next != nil && next.key == key {
		return next.value, true
	}
	return nil, false
}

func (sl *SkipList) Delete(key string) (bool, error) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if sl.immutable {
		return false, ErrImmutable
	}

	update := make([]*node, maxLevel)
	current := sl.header

	for i := sl.level; i >= 0; i-- {
		for current.forward[i] != nil && current.forward[i].key < key {
			current = current.forward[i]
		}
		update[i] = current
	}

	target := current.forward[0]
	if target == nil || target.key != key {
		return false, nil
	}

	for i := 0; i <= sl.level; i++ {
		if update[i].forward[i] != target {
			break
		}
		update[i].forward[i] = target.forward[i]
	}

	for sl.level > 0 && sl.header.forward[sl.level] == nil {
		sl.level--
	}
	sl.sizeBytes -= entrySize(target.key, target.value)
	sl.size--
	return true, nil
}

func (sl *SkipList) Len() int {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.size
}

func (sl *SkipList) MarkImmutable() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.immutable = true
}

func (sl *SkipList) IsImmutable() bool {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.immutable
}
