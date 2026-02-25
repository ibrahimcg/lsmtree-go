package main

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

func TestBloomFilterAddAndMayContain(t *testing.T) {
	bf := NewBloomFilter(1024, 4)
	keys := []string{"apple", "banana", "cherry", "date", "elderberry"}

	for _, k := range keys {
		bf.Add(k)
	}
	for _, k := range keys {
		if !bf.MayContain(k) {
			t.Fatalf("expected bloom filter to contain %q", k)
		}
	}
}

func TestBloomFilterNoFalseNegatives(t *testing.T) {
	bf := OptimalBloomFilter(1000, 0.01)
	keys := make([]string, 1000)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
		bf.Add(keys[i])
	}
	for _, k := range keys {
		if !bf.MayContain(k) {
			t.Fatalf("false negative for %q", k)
		}
	}
}

func TestBloomFilterFalsePositiveRate(t *testing.T) {
	n := 1000
	bf := OptimalBloomFilter(n, 0.01)

	for i := 0; i < n; i++ {
		bf.Add(fmt.Sprintf("present-%d", i))
	}

	probes := 100_000
	fp := 0
	for i := 0; i < probes; i++ {
		if bf.MayContain(fmt.Sprintf("absent-%d", i)) {
			fp++
		}
	}

	rate := float64(fp) / float64(probes)
	if rate > 0.02 {
		t.Fatalf("false positive rate %.4f exceeds 2%%", rate)
	}
	t.Logf("false positive rate: %.4f (%d/%d)", rate, fp, probes)
}

func TestBloomFilterEmpty(t *testing.T) {
	bf := NewBloomFilter(512, 3)
	probes := []string{"a", "b", "anything", ""}
	for _, p := range probes {
		if bf.MayContain(p) {
			t.Fatalf("empty filter should not contain %q", p)
		}
	}
}

func TestBloomFilterReset(t *testing.T) {
	bf := NewBloomFilter(512, 3)
	bf.Add("hello")
	if !bf.MayContain("hello") {
		t.Fatal("expected to contain 'hello' before reset")
	}

	bf.Reset()
	if bf.MayContain("hello") {
		t.Fatal("expected reset filter to not contain 'hello'")
	}
}

func TestOptimalBloomFilterSizing(t *testing.T) {
	bf := OptimalBloomFilter(1000, 0.01)

	// For n=1000, p=0.01: m ≈ 9585, k ≈ 7
	if bf.NumBits() < 9000 || bf.NumBits() > 11000 {
		t.Fatalf("unexpected numBits: %d (expected ~9585)", bf.NumBits())
	}
	if bf.NumHash() < 6 || bf.NumHash() > 8 {
		t.Fatalf("unexpected numHash: %d (expected ~7)", bf.NumHash())
	}
}

func TestNewBloomFilterEdgeCases(t *testing.T) {
	// Zero params should not panic; values get clamped.
	bf := NewBloomFilter(0, 0)
	if bf.NumBits() < 64 {
		t.Fatalf("expected minimum 64 bits, got %d", bf.NumBits())
	}
	if bf.NumHash() < 1 {
		t.Fatalf("expected minimum 1 hash, got %d", bf.NumHash())
	}

	// Degenerate OptimalBloomFilter params.
	bf2 := OptimalBloomFilter(0, 0)
	bf2.Add("test")
	if !bf2.MayContain("test") {
		t.Fatal("degenerate bloom filter should still work")
	}
}

func TestSkipListSearchUsesBloomFilter(t *testing.T) {
	sl := NewSkipList(0)
	for i := 0; i < 100; i++ {
		sl.Insert(fmt.Sprintf("key-%d", i), []byte(fmt.Sprintf("val-%d", i)))
	}
	sl.MarkImmutable()

	// All inserted keys must be found (bloom must not cause false negatives).
	for i := 0; i < 100; i++ {
		val, ok := sl.Search(fmt.Sprintf("key-%d", i))
		if !ok {
			t.Fatalf("expected key-%d to be found in immutable skip list", i)
		}
		if string(val) != fmt.Sprintf("val-%d", i) {
			t.Fatalf("wrong value for key-%d: got %q", i, val)
		}
	}

	// Missing keys should return not found (may go through bloom or skip list).
	for i := 100; i < 200; i++ {
		_, ok := sl.Search(fmt.Sprintf("key-%d", i))
		if ok {
			t.Fatalf("did not expect key-%d to be found", i)
		}
	}
}

// Property: no false negatives — every inserted key must be found.
func TestPropertyNoFalseNegatives(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numBits := uint32(rapid.IntRange(64, 10000).Draw(t, "numBits"))
		numHash := uint32(rapid.IntRange(1, 20).Draw(t, "numHash"))
		bf := NewBloomFilter(numBits, numHash)

		keys := rapid.SliceOfN(rapid.StringMatching(`[a-zA-Z0-9]{1,30}`), 1, 200).Draw(t, "keys")
		for _, k := range keys {
			bf.Add(k)
		}
		for _, k := range keys {
			if !bf.MayContain(k) {
				t.Fatalf("false negative for %q (numBits=%d, numHash=%d)", k, numBits, numHash)
			}
		}
	})
}

// Property: adding the same key multiple times has the same effect as adding it once.
func TestPropertyBloomFilterIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bf := OptimalBloomFilter(100, 0.01)

		key := rapid.StringMatching(`[a-z]{1,20}`).Draw(t, "key")
		bf.Add(key)

		// Snapshot bits after first add.
		snapshot := make([]uint64, len(bf.bits))
		copy(snapshot, bf.bits)

		n := rapid.IntRange(1, 10).Draw(t, "repeats")
		for i := 0; i < n; i++ {
			bf.Add(key)
		}

		for i, w := range bf.bits {
			if w != snapshot[i] {
				t.Fatalf("bits changed after re-adding same key at word %d", i)
			}
		}
	})
}

// Property: insert random keys into skip list, mark immutable, verify all found + missing keys not found.
func TestPropertySkipListBloomIntegration(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sl := NewSkipList(0)
		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,15}`), 1, 100).Draw(t, "keys")
		seen := make(map[string]bool)

		for _, k := range keys {
			sl.Insert(k, []byte(k))
			seen[k] = true
		}
		sl.MarkImmutable()

		// All inserted keys must be found.
		for k := range seen {
			val, ok := sl.Search(k)
			if !ok {
				t.Fatalf("inserted key %q not found after marking immutable", k)
			}
			if string(val) != k {
				t.Fatalf("key %q: got %q, want %q", k, val, k)
			}
		}

		// Probe keys that were never inserted.
		absent := rapid.SliceOfN(rapid.StringMatching(`[A-Z]{1,15}`), 1, 50).Draw(t, "absent")
		for _, k := range absent {
			if seen[k] {
				continue
			}
			_, ok := sl.Search(k)
			if ok {
				t.Fatalf("absent key %q found in immutable skip list", k)
			}
		}
	})
}
