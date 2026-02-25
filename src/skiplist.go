package main

import (
	"math/rand"
	"sync"
)

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
	header *node
	level  int
	size   int
	mu     sync.RWMutex
}

func NewSkipList() *SkipList {
	return &SkipList{
		header: &node{forward: make([]*node, maxLevel)},
		level:  0,
	}
}

func (sl *SkipList) randomLevel() int {
	lvl := 0
	for lvl < maxLevel-1 && rand.Float64() < probability {
		lvl++
	}
	return lvl
}

func (sl *SkipList) Insert(key string, value []byte) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

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
		next.value = value
		return
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
	sl.size++
}

func (sl *SkipList) Search(key string) ([]byte, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

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

func (sl *SkipList) Delete(key string) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()

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
		return false
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
	sl.size--
	return true
}

func (sl *SkipList) Len() int {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.size
}
