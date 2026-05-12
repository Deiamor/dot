package store

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"sync"
)

// PersistentStore is a thread-safe in-memory KV store that is backed by a
// WAL and periodic snapshots. It implements the same Get/Set/Delete semantics
// as AppState's raw maps, but survives process restarts.
//
// Callers commit a block by calling Commit(height), which appends all pending
// mutations to the WAL. Every `snapshotInterval` commits, a full snapshot is
// written and the WAL is truncated.
type PersistentStore struct {
	mu               sync.RWMutex
	data             map[string][]byte
	pending          []WALEntry // mutations since last Commit
	height           int64
	wal              *WAL
	snap             *SnapshotStore
	snapshotInterval int64
}

// Open loads (or creates) a PersistentStore at the given directory prefix.
// `prefix` is used as the base path: `prefix.snap` and `prefix.wal` are created.
func Open(prefix string, snapshotInterval int64) (*PersistentStore, error) {
	if snapshotInterval <= 0 {
		snapshotInterval = 100
	}

	snap := NewSnapshotStore(prefix + ".snap")
	snapshot, err := snap.Read()
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	wal, err := OpenWAL(prefix + ".wal")
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	// Replay WAL on top of snapshot.
	height, err := wal.Replay(snapshot.Height, snapshot.Data)
	if err != nil {
		return nil, fmt.Errorf("replay WAL: %w", err)
	}

	return &PersistentStore{
		data:             snapshot.Data,
		height:           height,
		wal:              wal,
		snap:             snap,
		snapshotInterval: snapshotInterval,
	}, nil
}

// Get returns the raw bytes stored at key, or nil if absent.
func (p *PersistentStore) Get(key string) []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.data[key]
}

// Set stages a value for the given key. The mutation is recorded in `pending`
// and will be persisted on the next Commit.
func (p *PersistentStore) Set(key string, value []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data[key] = value
	p.pending = append(p.pending, WALEntry{Op: "set", Key: key, Value: value})
}

// Delete removes a key. Staged as a pending mutation.
func (p *PersistentStore) Delete(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.data, key)
	p.pending = append(p.pending, WALEntry{Op: "del", Key: key})
}

// Commit writes all pending mutations to the WAL at `height`.
// If `height % snapshotInterval == 0`, a full snapshot is written and the WAL is truncated.
func (p *PersistentStore) Commit(height int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range p.pending {
		p.pending[i].Height = height
		if err := p.wal.Append(p.pending[i]); err != nil {
			return fmt.Errorf("WAL append: %w", err)
		}
	}
	p.pending = p.pending[:0]
	p.height = height

	if height%p.snapshotInterval == 0 {
		if err := p.writeSnapshot(height); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
		if err := p.wal.Truncate(height); err != nil {
			return fmt.Errorf("WAL truncate: %w", err)
		}
	}
	return nil
}

func (p *PersistentStore) writeSnapshot(height int64) error {
	data := make(map[string][]byte, len(p.data))
	for k, v := range p.data {
		data[k] = v
	}
	return p.snap.Write(Snapshot{Height: height, Data: data})
}

// Height returns the last committed block height.
func (p *PersistentStore) Height() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.height
}

// Close flushes and closes the underlying WAL.
func (p *PersistentStore) Close() error {
	return p.wal.Close()
}

// --- typed helpers -------------------------------------------------------
// These encode/decode Go values into gob bytes for storage.

// GetTyped decodes the stored value at key into dst.
func GetTyped(ps *PersistentStore, key string, dst any) (bool, error) {
	raw := ps.Get(key)
	if raw == nil {
		return false, nil
	}
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(dst); err != nil {
		return false, fmt.Errorf("GetTyped %q: %w", key, err)
	}
	return true, nil
}

// SetTyped gob-encodes value and stores it at key.
func SetTyped(ps *PersistentStore, key string, value any) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		return fmt.Errorf("SetTyped %q: %w", key, err)
	}
	ps.Set(key, buf.Bytes())
	return nil
}
