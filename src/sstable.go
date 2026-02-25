package main

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	dataBlockTargetSize = 4096
	footerSize          = 48
	magicNumber         = uint64(0x0B1EC7AB1E55)
)

var (
	ErrInvalidMagic  = errors.New("sstable: invalid magic number")
	ErrCorruptFooter = errors.New("sstable: corrupt footer")
)

// indexEntry records the first key and location of a data block.
type indexEntry struct {
	FirstKey string
	Offset   uint64
	Size     uint32
}

// footer is the fixed-size trailer at the end of an SSTable file.
type footer struct {
	IndexOffset uint64
	IndexSize   uint32
	BloomOffset uint64
	BloomSize   uint32
	Magic       uint64
}

// --- Entry encoding ---

const (
	flagTombstone byte = 1 << 0
)

// encodeEntry packs a key/value pair as [4B key_len][key][1B flags][4B val_len][value].
// A nil value is encoded as a tombstone (flags bit 0 set, val_len=0).
func encodeEntry(key string, value []byte) []byte {
	kl := uint32(len(key))
	var flags byte
	var vl uint32
	if value == nil {
		flags = flagTombstone
	} else {
		vl = uint32(len(value))
	}
	buf := make([]byte, 4+kl+1+4+vl)
	binary.LittleEndian.PutUint32(buf[0:4], kl)
	copy(buf[4:4+kl], key)
	buf[4+kl] = flags
	binary.LittleEndian.PutUint32(buf[5+kl:9+kl], vl)
	copy(buf[9+kl:], value)
	return buf
}

// decodeEntry reads one entry from buf and returns the key, value, bytes consumed,
// tombstone flag, and any error.
func decodeEntry(buf []byte) (string, []byte, int, bool, error) {
	if len(buf) < 4 {
		return "", nil, 0, false, fmt.Errorf("sstable: entry too short for key length")
	}
	kl := binary.LittleEndian.Uint32(buf[0:4])
	need := 4 + int(kl) + 1 + 4 // key_len + key + flags + val_len
	if len(buf) < need {
		return "", nil, 0, false, fmt.Errorf("sstable: entry too short for key+flags")
	}
	key := string(buf[4 : 4+kl])
	flags := buf[4+kl]
	isTombstone := flags&flagTombstone != 0
	vl := binary.LittleEndian.Uint32(buf[5+kl : 9+kl])
	total := 9 + int(kl) + int(vl)
	if len(buf) < total {
		return "", nil, 0, false, fmt.Errorf("sstable: entry too short for value")
	}
	if isTombstone {
		return key, nil, total, true, nil
	}
	value := make([]byte, vl)
	copy(value, buf[9+kl:total])
	return key, value, total, false, nil
}

// --- Index encoding ---

// encodeIndexEntry packs one index entry as [4B key_len][first_key][8B offset][4B size].
func encodeIndexEntry(e indexEntry) []byte {
	kl := uint32(len(e.FirstKey))
	buf := make([]byte, 4+kl+8+4)
	binary.LittleEndian.PutUint32(buf[0:4], kl)
	copy(buf[4:4+kl], e.FirstKey)
	binary.LittleEndian.PutUint64(buf[4+kl:12+kl], e.Offset)
	binary.LittleEndian.PutUint32(buf[12+kl:16+kl], e.Size)
	return buf
}

// decodeIndexBlock decodes a full index block into a slice of indexEntry.
func decodeIndexBlock(buf []byte) ([]indexEntry, error) {
	var entries []indexEntry
	pos := 0
	for pos < len(buf) {
		if pos+4 > len(buf) {
			return nil, fmt.Errorf("sstable: index block truncated at key length")
		}
		kl := int(binary.LittleEndian.Uint32(buf[pos : pos+4]))
		need := 4 + kl + 8 + 4
		if pos+need > len(buf) {
			return nil, fmt.Errorf("sstable: index block truncated at entry")
		}
		key := string(buf[pos+4 : pos+4+kl])
		offset := binary.LittleEndian.Uint64(buf[pos+4+kl : pos+12+kl])
		size := binary.LittleEndian.Uint32(buf[pos+12+kl : pos+16+kl])
		entries = append(entries, indexEntry{FirstKey: key, Offset: offset, Size: size})
		pos += need
	}
	return entries, nil
}

// --- Bloom filter encoding ---

// encodeBloomFilter serialises a bloom filter as [4B numBits][4B numHash][N×8B words].
func encodeBloomFilter(bf *BloomFilter) []byte {
	nWords := len(bf.bits)
	buf := make([]byte, 4+4+nWords*8)
	binary.LittleEndian.PutUint32(buf[0:4], bf.numBits)
	binary.LittleEndian.PutUint32(buf[4:8], bf.numHash)
	for i, w := range bf.bits {
		binary.LittleEndian.PutUint64(buf[8+i*8:16+i*8], w)
	}
	return buf
}

// decodeBloomFilter reconstructs a BloomFilter from bytes written by encodeBloomFilter.
func decodeBloomFilter(buf []byte) (*BloomFilter, error) {
	if len(buf) < 8 {
		return nil, fmt.Errorf("sstable: bloom filter block too short")
	}
	numBits := binary.LittleEndian.Uint32(buf[0:4])
	numHash := binary.LittleEndian.Uint32(buf[4:8])
	nWords := (numBits + 63) / 64
	if len(buf) < 8+int(nWords)*8 {
		return nil, fmt.Errorf("sstable: bloom filter block truncated")
	}
	bits := make([]uint64, nWords)
	for i := range bits {
		bits[i] = binary.LittleEndian.Uint64(buf[8+i*8 : 16+i*8])
	}
	return &BloomFilter{bits: bits, numBits: numBits, numHash: numHash}, nil
}

// --- Footer encoding ---

// encodeFooter writes a 48-byte footer.
func encodeFooter(f footer) []byte {
	buf := make([]byte, footerSize)
	binary.LittleEndian.PutUint64(buf[0:8], f.IndexOffset)
	binary.LittleEndian.PutUint32(buf[8:12], f.IndexSize)
	binary.LittleEndian.PutUint64(buf[12:20], f.BloomOffset)
	binary.LittleEndian.PutUint32(buf[20:24], f.BloomSize)
	binary.LittleEndian.PutUint64(buf[24:32], f.Magic)
	// bytes 32..47 are reserved (zero)
	return buf
}

// decodeFooter reads a 48-byte footer and validates the magic number.
func decodeFooter(buf []byte) (footer, error) {
	if len(buf) != footerSize {
		return footer{}, ErrCorruptFooter
	}
	f := footer{
		IndexOffset: binary.LittleEndian.Uint64(buf[0:8]),
		IndexSize:   binary.LittleEndian.Uint32(buf[8:12]),
		BloomOffset: binary.LittleEndian.Uint64(buf[12:20]),
		BloomSize:   binary.LittleEndian.Uint32(buf[20:24]),
		Magic:       binary.LittleEndian.Uint64(buf[24:32]),
	}
	if f.Magic != magicNumber {
		return footer{}, ErrInvalidMagic
	}
	return f, nil
}
