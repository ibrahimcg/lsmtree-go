package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

// --- Encode/decode round-trip tests ---

func TestEncodeDecodeEntry(t *testing.T) {
	tests := []struct {
		key string
		val []byte
	}{
		{"hello", []byte("world")},
		{"", []byte("")},
		{"k", []byte("longvalue1234567890")},
	}
	for _, tt := range tests {
		buf := encodeEntry(tt.key, tt.val)
		k, v, n, err := decodeEntry(buf)
		if err != nil {
			t.Fatalf("decodeEntry error: %v", err)
		}
		if n != len(buf) {
			t.Fatalf("consumed %d bytes, expected %d", n, len(buf))
		}
		if k != tt.key {
			t.Fatalf("key: got %q, want %q", k, tt.key)
		}
		if string(v) != string(tt.val) {
			t.Fatalf("value: got %q, want %q", v, tt.val)
		}
	}
}

func TestEncodeDecodeIndexBlock(t *testing.T) {
	entries := []indexEntry{
		{FirstKey: "aaa", Offset: 0, Size: 4096},
		{FirstKey: "mmm", Offset: 4096, Size: 3000},
		{FirstKey: "zzz", Offset: 7096, Size: 500},
	}
	var buf []byte
	for _, e := range entries {
		buf = append(buf, encodeIndexEntry(e)...)
	}

	decoded, err := decodeIndexBlock(buf)
	if err != nil {
		t.Fatalf("decodeIndexBlock error: %v", err)
	}
	if len(decoded) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(decoded), len(entries))
	}
	for i, e := range entries {
		if decoded[i] != e {
			t.Fatalf("entry %d: got %+v, want %+v", i, decoded[i], e)
		}
	}
}

func TestEncodeDecodeBloomFilter(t *testing.T) {
	bf := OptimalBloomFilter(100, 0.01)
	for i := 0; i < 100; i++ {
		bf.Add(fmt.Sprintf("key-%d", i))
	}

	buf := encodeBloomFilter(bf)
	restored, err := decodeBloomFilter(buf)
	if err != nil {
		t.Fatalf("decodeBloomFilter error: %v", err)
	}

	// All keys must still be present.
	for i := 0; i < 100; i++ {
		if !restored.MayContain(fmt.Sprintf("key-%d", i)) {
			t.Fatalf("false negative for key-%d after decode", i)
		}
	}
	if restored.NumBits() != bf.NumBits() {
		t.Fatalf("numBits: got %d, want %d", restored.NumBits(), bf.NumBits())
	}
	if restored.NumHash() != bf.NumHash() {
		t.Fatalf("numHash: got %d, want %d", restored.NumHash(), bf.NumHash())
	}
}

func TestEncodeDecodeFooter(t *testing.T) {
	ft := footer{
		IndexOffset: 12345,
		IndexSize:   678,
		BloomOffset: 13023,
		BloomSize:   200,
		Magic:       magicNumber,
	}
	buf := encodeFooter(ft)
	if len(buf) != footerSize {
		t.Fatalf("footer size: got %d, want %d", len(buf), footerSize)
	}
	decoded, err := decodeFooter(buf)
	if err != nil {
		t.Fatalf("decodeFooter error: %v", err)
	}
	if decoded != ft {
		t.Fatalf("got %+v, want %+v", decoded, ft)
	}
}

func TestDecodeFooterBadMagic(t *testing.T) {
	ft := footer{Magic: 0xDEAD}
	buf := encodeFooter(ft)
	// Manually overwrite magic to bad value (encodeFooter wrote 0xDEAD).
	_, err := decodeFooter(buf)
	if err != ErrInvalidMagic {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}
}

// --- Write + Read integration tests ---

func TestSSTableWriteReadSingleEntry(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	sl.Insert("hello", []byte("world"))
	sl.MarkImmutable()

	w := &SSTableWriter{Dir: dir}
	path, err := w.WriteSSTable(sl)
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}

	r, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable: %v", err)
	}
	defer r.Close()

	val, found, err := r.Search("hello")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if !found {
		t.Fatal("expected to find key 'hello'")
	}
	if string(val) != "world" {
		t.Fatalf("got %q, want %q", val, "world")
	}
}

func TestSSTableWriteReadManyEntries(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	n := 1000
	for i := 0; i < n; i++ {
		sl.Insert(fmt.Sprintf("key-%05d", i), []byte(fmt.Sprintf("val-%05d", i)))
	}
	sl.MarkImmutable()

	w := &SSTableWriter{Dir: dir}
	path, err := w.WriteSSTable(sl)
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}

	r, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable: %v", err)
	}
	defer r.Close()

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key-%05d", i)
		want := fmt.Sprintf("val-%05d", i)
		val, found, err := r.Search(key)
		if err != nil {
			t.Fatalf("Search(%q) error: %v", key, err)
		}
		if !found {
			t.Fatalf("expected to find %q", key)
		}
		if string(val) != want {
			t.Fatalf("key %q: got %q, want %q", key, val, want)
		}
	}
}

func TestSSTableSearchMiss(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	sl.Insert("aaa", []byte("1"))
	sl.Insert("zzz", []byte("2"))
	sl.MarkImmutable()

	w := &SSTableWriter{Dir: dir}
	path, err := w.WriteSSTable(sl)
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}

	r, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable: %v", err)
	}
	defer r.Close()

	_, found, err := r.Search("mmm")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if found {
		t.Fatal("expected key 'mmm' to not be found")
	}
}

func TestSSTableSearchBeforeFirstKey(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	sl.Insert("bbb", []byte("1"))
	sl.Insert("ccc", []byte("2"))
	sl.MarkImmutable()

	w := &SSTableWriter{Dir: dir}
	path, err := w.WriteSSTable(sl)
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}

	r, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable: %v", err)
	}
	defer r.Close()

	_, found, err := r.Search("aaa")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if found {
		t.Fatal("expected key 'aaa' (before first key) to not be found")
	}
}

func TestSSTableFileNaming(t *testing.T) {
	dir := t.TempDir()
	sl := NewSkipList(0)
	sl.Insert("k", []byte("v"))
	sl.MarkImmutable()

	w := &SSTableWriter{Dir: dir}
	path, err := w.WriteSSTable(sl)
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}

	base := filepath.Base(path)
	if filepath.Ext(base) != ".sst" {
		t.Fatalf("expected .sst extension, got %q", base)
	}

	// No .tmp file should remain.
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) > 0 {
		t.Fatalf("expected no .tmp files, found %v", matches)
	}
}

// --- Property-based test: random skip list → flush → verify ---

func TestPropertySSTableRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir := os.TempDir()
		tmpDir, err := os.MkdirTemp(dir, "sstable-test-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		sl := NewSkipList(0)
		ref := make(map[string]string)

		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,20}`), 1, 200).Draw(t, "keys")
		for _, k := range keys {
			val := rapid.StringMatching(`[a-z]{1,20}`).Draw(t, "val")
			sl.Insert(k, []byte(val))
			ref[k] = val
		}
		sl.MarkImmutable()

		w := &SSTableWriter{Dir: tmpDir}
		path, err := w.WriteSSTable(sl)
		if err != nil {
			t.Fatalf("WriteSSTable: %v", err)
		}

		r, err := OpenSSTable(path)
		if err != nil {
			t.Fatalf("OpenSSTable: %v", err)
		}
		defer r.Close()

		for k, want := range ref {
			val, found, err := r.Search(k)
			if err != nil {
				t.Fatalf("Search(%q) error: %v", k, err)
			}
			if !found {
				t.Fatalf("key %q not found in SSTable", k)
			}
			if string(val) != want {
				t.Fatalf("key %q: got %q, want %q", k, val, want)
			}
		}

		// Absent keys should not be found.
		absent := rapid.SliceOfN(rapid.StringMatching(`[A-Z]{1,10}`), 1, 20).Draw(t, "absent")
		for _, k := range absent {
			if _, ok := ref[k]; ok {
				continue
			}
			_, found, err := r.Search(k)
			if err != nil {
				t.Fatalf("Search(%q) error: %v", k, err)
			}
			if found {
				t.Fatalf("absent key %q found in SSTable", k)
			}
		}
	})
}

// --- Flush integration test via MemTable manager ---

func TestFlushImmutableIntegration(t *testing.T) {
	dir := t.TempDir()
	// maxBytes=4: "a"+"1"=2, "b"+"2"=2 fills it, "c"+"3" triggers rotation.
	mt := NewMemTable(4)
	mt.Insert("a", []byte("1"))
	mt.Insert("b", []byte("2"))
	mt.Insert("c", []byte("3")) // rotation

	imm := mt.GetImmutables()
	if len(imm) != 1 {
		t.Fatalf("expected 1 immutable, got %d", len(imm))
	}

	w := &SSTableWriter{Dir: dir}
	path, err := FlushImmutable(mt, w)
	if err != nil {
		t.Fatalf("FlushImmutable: %v", err)
	}

	// Immutable should be removed.
	imm = mt.GetImmutables()
	if len(imm) != 0 {
		t.Fatalf("expected 0 immutables after flush, got %d", len(imm))
	}

	// The flushed SSTable should contain "a" and "b".
	r, err := OpenSSTable(path)
	if err != nil {
		t.Fatalf("OpenSSTable: %v", err)
	}
	defer r.Close()

	for _, key := range []string{"a", "b"} {
		val, found, err := r.Search(key)
		if err != nil {
			t.Fatalf("Search(%q) error: %v", key, err)
		}
		if !found {
			t.Fatalf("expected key %q in flushed SSTable", key)
		}
		expected := string(key[0] - 'a' + '1')
		if string(val) != expected {
			t.Fatalf("key %q: got %q, want %q", key, val, expected)
		}
	}

	// "c" should NOT be in the flushed SSTable (it's in the active memtable).
	_, found, err := r.Search("c")
	if err != nil {
		t.Fatalf("Search('c') error: %v", err)
	}
	if found {
		t.Fatal("expected key 'c' to not be in the flushed SSTable")
	}
}

func TestFlushImmutableNoImmutables(t *testing.T) {
	mt := NewMemTable(0)
	w := &SSTableWriter{Dir: t.TempDir()}
	_, err := FlushImmutable(mt, w)
	if err == nil {
		t.Fatal("expected error when no immutables to flush")
	}
}
