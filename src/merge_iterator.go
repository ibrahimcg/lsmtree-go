package main

import "container/heap"

// MergeIterator merges N sorted Iterator sources into a single sorted stream.
// Sources with a lower index have higher priority: when the same key appears
// in multiple sources, only the entry from the lowest-index source is emitted.
type MergeIterator struct {
	h         mergeHeap
	key       string
	value     []byte
	tombstone bool
	valid     bool
}

type heapItem struct {
	key       string
	value     []byte
	tombstone bool
	srcIdx    int // lower = higher priority
	iter      Iterator
}

type mergeHeap []heapItem

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	if h[i].key != h[j].key {
		return h[i].key < h[j].key
	}
	return h[i].srcIdx < h[j].srcIdx // lower index = higher priority
}
func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(x any) {
	*h = append(*h, x.(heapItem))
}

func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// NewMergeIterator creates a merge iterator over the given sources.
// Sources at lower indices have higher priority (newer data).
func NewMergeIterator(sources []Iterator) *MergeIterator {
	mi := &MergeIterator{}
	mi.h = make(mergeHeap, 0, len(sources))

	for idx, it := range sources {
		if it.Valid() {
			mi.h = append(mi.h, heapItem{
				key:       it.Key(),
				value:     it.Value(),
				tombstone: it.IsTombstone(),
				srcIdx:    idx,
				iter:      it,
			})
		}
	}

	heap.Init(&mi.h)
	mi.advance()
	return mi
}

// advance pops the smallest key from the heap, skipping duplicate keys
// from lower-priority sources.
func (mi *MergeIterator) advance() {
	if mi.h.Len() == 0 {
		mi.valid = false
		return
	}

	item := heap.Pop(&mi.h).(heapItem)
	mi.key = item.key
	mi.value = item.value
	mi.tombstone = item.tombstone
	mi.valid = true

	// Re-insert this source's next entry.
	item.iter.Next()
	if item.iter.Valid() {
		heap.Push(&mi.h, heapItem{
			key:       item.iter.Key(),
			value:     item.iter.Value(),
			tombstone: item.iter.IsTombstone(),
			srcIdx:    item.srcIdx,
			iter:      item.iter,
		})
	}

	// Skip entries with the same key from lower-priority sources.
	for mi.h.Len() > 0 && mi.h[0].key == mi.key {
		dup := heap.Pop(&mi.h).(heapItem)
		dup.iter.Next()
		if dup.iter.Valid() {
			heap.Push(&mi.h, heapItem{
				key:       dup.iter.Key(),
				value:     dup.iter.Value(),
				tombstone: dup.iter.IsTombstone(),
				srcIdx:    dup.srcIdx,
				iter:      dup.iter,
			})
		}
	}
}

func (mi *MergeIterator) Valid() bool      { return mi.valid }
func (mi *MergeIterator) Key() string      { return mi.key }
func (mi *MergeIterator) Value() []byte    { return mi.value }
func (mi *MergeIterator) IsTombstone() bool { return mi.tombstone }
func (mi *MergeIterator) Next()             { mi.advance() }

// Compile-time check.
var _ Iterator = (*MergeIterator)(nil)
