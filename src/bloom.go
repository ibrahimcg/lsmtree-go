package main

import (
	"hash/fnv"
	"math"
)

// BloomFilter is a space-efficient probabilistic data structure that tests
// whether an element is a member of a set. False positives are possible,
// but false negatives are not.
//
// Thread safety: none internal — callers must synchronize access externally.
type BloomFilter struct {
	bits    []uint64
	numBits uint32
	numHash uint32
}

// NewBloomFilter creates a bloom filter with the given number of bits and hash functions.
// numBits is clamped to a minimum of 64; numHash is clamped to a minimum of 1.
func NewBloomFilter(numBits, numHash uint32) *BloomFilter {
	if numBits < 64 {
		numBits = 64
	}
	if numHash < 1 {
		numHash = 1
	}
	nWords := (numBits + 63) / 64
	// Round numBits up to a multiple of 64.
	numBits = nWords * 64
	return &BloomFilter{
		bits:    make([]uint64, nWords),
		numBits: numBits,
		numHash: numHash,
	}
}

// OptimalBloomFilter creates a bloom filter sized optimally for the expected
// number of items and desired false-positive rate.
//   - m = -n*ln(p) / (ln2)^2
//   - k = (m/n) * ln2
func OptimalBloomFilter(expectedItems int, fpRate float64) *BloomFilter {
	if expectedItems < 1 {
		expectedItems = 1
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}

	n := float64(expectedItems)
	ln2 := math.Ln2
	m := -n * math.Log(fpRate) / (ln2 * ln2)
	k := (m / n) * ln2

	numBits := uint32(math.Ceil(m))
	numHash := uint32(math.Ceil(k))
	if numHash < 1 {
		numHash = 1
	}

	return NewBloomFilter(numBits, numHash)
}

// hashes computes two base hashes for double-hashing by splitting a 64-bit
// FNV-1a hash into upper and lower 32-bit halves.
func hashes(key string) (uint32, uint32) {
	h := fnv.New64a()
	h.Write([]byte(key))
	sum := h.Sum64()
	return uint32(sum), uint32(sum >> 32)
}

// Add inserts a key into the bloom filter.
func (bf *BloomFilter) Add(key string) {
	h1, h2 := hashes(key)
	for i := uint32(0); i < bf.numHash; i++ {
		idx := (h1 + i*h2) % bf.numBits
		bf.bits[idx/64] |= 1 << (idx % 64)
	}
}

// MayContain returns false if the key is definitely not in the set.
// A true return means the key is probably in the set (may be a false positive).
func (bf *BloomFilter) MayContain(key string) bool {
	h1, h2 := hashes(key)
	for i := uint32(0); i < bf.numHash; i++ {
		idx := (h1 + i*h2) % bf.numBits
		if bf.bits[idx/64]&(1<<(idx%64)) == 0 {
			return false
		}
	}
	return true
}

// Reset clears all bits in the filter.
func (bf *BloomFilter) Reset() {
	for i := range bf.bits {
		bf.bits[i] = 0
	}
}

// NumBits returns the number of bits in the filter.
func (bf *BloomFilter) NumBits() uint32 {
	return bf.numBits
}

// NumHash returns the number of hash functions used by the filter.
func (bf *BloomFilter) NumHash() uint32 {
	return bf.numHash
}
