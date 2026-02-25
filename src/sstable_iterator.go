package main

// SSTableIterator sequentially iterates over all entries in an SSTable file.
// It reads data blocks one by one using the in-memory index from SSTableReader.
type SSTableIterator struct {
	reader    *SSTableReader
	blockIdx  int    // current index block position
	blockBuf  []byte // current data block bytes
	blockPos  int    // position within current block
	key       string
	value     []byte
	tombstone bool
	valid     bool
	err       error
}

// NewSSTableIterator creates an iterator over all entries in the given SSTable.
func NewSSTableIterator(reader *SSTableReader) *SSTableIterator {
	it := &SSTableIterator{reader: reader}
	if len(reader.index) == 0 {
		return it
	}
	it.loadBlock(0)
	if it.err == nil {
		it.readEntry()
	}
	return it
}

// loadBlock reads the data block at the given index position.
func (it *SSTableIterator) loadBlock(idx int) {
	if idx >= len(it.reader.index) {
		it.valid = false
		return
	}
	ie := it.reader.index[idx]
	buf := make([]byte, ie.Size)
	if _, err := it.reader.f.ReadAt(buf, int64(ie.Offset)); err != nil {
		it.err = err
		it.valid = false
		return
	}
	it.blockIdx = idx
	it.blockBuf = buf
	it.blockPos = 0
}

// readEntry decodes the entry at the current block position.
func (it *SSTableIterator) readEntry() {
	if it.blockPos >= len(it.blockBuf) {
		// Move to the next block.
		it.loadBlock(it.blockIdx + 1)
		if !it.valid && it.err == nil {
			// No more blocks.
			return
		}
		if it.err != nil {
			return
		}
	}
	key, value, n, tombstone, err := decodeEntry(it.blockBuf[it.blockPos:])
	if err != nil {
		it.err = err
		it.valid = false
		return
	}
	it.key = key
	it.value = value
	it.tombstone = tombstone
	it.blockPos += n
	it.valid = true
}

func (it *SSTableIterator) Valid() bool       { return it.valid }
func (it *SSTableIterator) Key() string       { return it.key }
func (it *SSTableIterator) Value() []byte     { return it.value }
func (it *SSTableIterator) IsTombstone() bool { return it.tombstone }
func (it *SSTableIterator) Err() error        { return it.err }

func (it *SSTableIterator) Next() {
	it.readEntry()
}

// Compile-time check.
var _ Iterator = (*SSTableIterator)(nil)
