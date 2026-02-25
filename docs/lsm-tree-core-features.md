# LSM-Tree Core Features

## The Big Idea

An LSM-tree turns random writes into sequential writes by buffering them in memory and flushing sorted batches to disk. Reads check memory first, then search through sorted on-disk files. A background compaction process merges and cleans up these files over time.

Everything in an LSM-tree follows from this one principle: **writes are fast because they're sequential; reads pay the cost of checking multiple sorted files.**

---

## 1. Memtable

The memtable is the entry point for all writes. It is an in-memory sorted data structure — typically a skip list or a red-black tree.

- Every PUT and DELETE goes into the memtable first
- Keys are kept in sorted order at all times
- Reads check the memtable before anything on disk, so recent writes are fast to read back
- When the memtable reaches a configured size threshold (e.g. 4MB), it becomes **immutable** — no more writes go into it
- A new, empty memtable is created to accept incoming writes
- The immutable memtable is scheduled for flushing to disk as an SSTable
- There is at most one immutable memtable at a time (some implementations allow a queue of them)

The memtable is entirely in memory and is lost on crash. That's why the WAL exists.

---

## 2. Write-Ahead Log (WAL)

The WAL guarantees durability for data that is in the memtable but not yet flushed to disk.

- Every write is appended to the WAL **before** it is inserted into the memtable
- The WAL is a simple sequential append-only file — no sorting, no indexing
- On crash, the memtable is rebuilt by replaying the WAL from start to end
- Each memtable has its own WAL file
- When a memtable is successfully flushed to an SSTable, its corresponding WAL is deleted
- The WAL does not need to be read during normal operation — only during recovery

The WAL is conceptually similar to your Bitcask data files: an append-only sequence of records. The difference is that it's temporary — it only exists until the memtable is flushed.

---

## 3. SSTables (Sorted String Tables)

SSTables are the on-disk representation of data. Each SSTable is an immutable, sorted file created by flushing a memtable or by compaction.

### Structure

An SSTable is divided into fixed-size **blocks** (typically 4KB each):

- **Data blocks** — contain sorted key-value pairs, packed sequentially. Each block covers a contiguous range of keys.
- **Index block** — a sparse index mapping the first key of each data block to its offset. Used to locate which data block contains a given key.
- **Bloom filter block** — a bloom filter covering all keys in the file. Used to skip the file entirely if the key is definitely not present.
- **Footer** — fixed-size trailer at the end of the file that points to the index block and bloom filter block offsets.

### Properties

- Once written, an SSTable is **never modified** — immutability simplifies concurrency and crash safety
- Keys are sorted, so binary search works within the index block
- Range scans are efficient because keys are physically adjacent
- Each SSTable covers a key range — the index block tells you the min and max key

### Reads from an SSTable

1. Check the bloom filter — if it says "no", skip this file entirely
2. Binary search the index block to find the right data block
3. Scan the data block to find the exact key
4. Return the value (or "not found" if the key isn't in the block)

---

## 4. Levels

SSTables are organized into levels (L0, L1, L2, ...). Each level has different properties.

### Level 0 (L0)

- SSTables in L0 come directly from memtable flushes
- L0 SSTables **can have overlapping key ranges** — two L0 files might both contain key "foo"
- Because of overlap, a read must check **every** L0 SSTable
- When L0 accumulates too many files (e.g. 4), compaction is triggered

### Level 1+ (L1, L2, L3, ...)

- SSTables within a level have **non-overlapping, sorted key ranges**
- For any given key, at most one SSTable per level can contain it
- A read can binary search across files to find the right one
- Each level is typically 10x the size of the previous level
  - L1: 10MB, L2: 100MB, L3: 1GB, L4: 10GB, etc.
- When a level exceeds its size limit, compaction pushes data to the next level

### Read Path Across Levels

To find a key, search in this order (stop at the first match):

1. Memtable (active)
2. Immutable memtable (if one exists)
3. All L0 SSTables (check each one — they overlap)
4. L1 — at most one SSTable can contain the key
5. L2 — at most one SSTable
6. L3, L4, ... same

The key insight: a key found at a higher level (closer to L0) is **newer** than the same key at a lower level. The first match wins.

---

## 5. Compaction

Compaction is the background process that merges SSTables to reclaim space, remove deleted keys, and reduce the number of files a read must check.

### Why Compaction Is Needed

- Without compaction, every write creates more files and reads get slower over time
- Deleted keys (tombstones) occupy space until compaction removes them
- Superseded values (old versions of updated keys) waste space
- L0 files overlap, which makes reads expensive — compaction sorts them into non-overlapping levels

### Leveled Compaction (LevelDB/RocksDB style)

- Pick an SSTable from level N that overlaps with some SSTables in level N+1
- Merge-sort all of them together
- Write new, non-overlapping SSTables at level N+1
- Delete the old SSTables that were merged
- This keeps each level (except L0) non-overlapping and sorted

### Size-Tiered Compaction (Cassandra style)

- Group SSTables of similar size together
- When enough similarly-sized SSTables accumulate, merge them into one larger SSTable
- Simpler to implement but can use more disk space temporarily

### What Compaction Does to Each Key

- If a key appears in multiple SSTables, **keep only the newest version**
- If the newest version is a tombstone (deletion marker), **drop both the tombstone and any older values** — but only if there are no older levels that might still have the key
- If two values exist for the same key, the one from the higher level (newer) wins

---

## 6. Bloom Filters

A bloom filter is a space-efficient probabilistic data structure that answers: "is this key in this SSTable?"

- **No false negatives** — if the bloom filter says "no", the key is definitely not in the file
- **Possible false positives** — if it says "yes", the key is probably in the file (small chance it's wrong)
- Typical false positive rate: ~1% with 10 bits per key
- Stored as part of each SSTable

### Why They Matter

Without bloom filters, every read would have to check the index block (and possibly a data block) of every SSTable at every level. Bloom filters let you skip files that definitely don't contain your key, which dramatically reduces disk I/O.

### How They Work

A bloom filter is a bit array of size M, with K independent hash functions.

**Insert:** hash the key with each of the K functions, set those K bit positions to 1.

**Query:** hash the key with each of the K functions, check if all K bit positions are 1. If any is 0, the key is definitely absent. If all are 1, the key is probably present.

---

## 7. Tombstones

Deletions in an LSM-tree cannot simply remove data — the key might exist in older, immutable SSTables on disk. Instead, a **tombstone** (a special deletion marker) is written.

- A DELETE writes a tombstone record into the memtable (and WAL), just like a normal PUT
- When a read encounters a tombstone, it returns "key not found" — it does not continue searching older levels
- Tombstones are removed during compaction, but only when there are no older SSTables that might contain the key
- Until compacted away, tombstones occupy space — this is a known tradeoff

This is similar to what you already implemented in Bitcask (valueSize = MaxUint32 as tombstone).

---

## 8. Block Cache

The block cache is an in-memory LRU cache for recently read SSTable data blocks.

- When a data block is read from disk, it's stored in the cache
- Subsequent reads for keys in the same block hit the cache instead of disk
- The cache has a fixed memory budget (e.g. 64MB)
- When the cache is full, the least recently used block is evicted
- Index blocks and bloom filter blocks can also be cached (often pinned permanently)

The block cache is optional but important for read-heavy workloads. Without it, every read hits disk.

---

## 9. Manifest

The manifest tracks the current state of the database: which SSTables exist, at which levels, and what key ranges they cover.

- Updated atomically after every compaction or flush
- On startup, the manifest is read to reconstruct the level structure
- Written as an append-only log of version edits (add file X to L2, remove file Y from L1)
- If the manifest is lost or corrupted, the database cannot determine which SSTables are current

Think of it as the "table of contents" for your SSTables.

---

## 10. Write Amplification, Read Amplification, Space Amplification

These three metrics define the tradeoffs in any LSM-tree design.

### Write Amplification

The ratio of total bytes written to disk vs. bytes written by the user. A single key-value pair gets written once to the WAL, once to an L0 SSTable, then rewritten during compaction from L0→L1, L1→L2, etc. Typical write amplification is 10x–30x.

### Read Amplification

The number of disk reads needed to find a key in the worst case. Without bloom filters, this could mean reading one file per level. Bloom filters reduce this dramatically.

### Space Amplification

The ratio of disk space used vs. the actual size of live data. Obsolete values and tombstones consume space until compacted. Leveled compaction has lower space amplification (~10%) than size-tiered (~100%).

### The Tradeoff Triangle

You can't optimize all three simultaneously:

- **Leveled compaction** — low read and space amplification, higher write amplification
- **Size-tiered compaction** — low write amplification, higher read and space amplification
- Tuning level sizes, bloom filter sizes, and compaction triggers shifts the balance

---

## Summary

| Component | Purpose |
|-----------|---------|
| Memtable | Buffer writes in sorted memory |
| WAL | Durability for unflushed memtable data |
| SSTable | Immutable sorted file on disk |
| Levels | Organize SSTables by age/size |
| Compaction | Merge SSTables, reclaim space, reduce read cost |
| Bloom filter | Skip SSTables that don't contain a key |
| Tombstones | Mark deletions without modifying old files |
| Block cache | Cache hot blocks in memory |
| Manifest | Track which SSTables exist and where |

The entire design optimizes for **write throughput** by never doing random I/O on the write path. Reads are more expensive but are kept practical through bloom filters, sorted levels, and caching.
