// Package store provides a durable, file-backed key-value store for AppState.
//
// Architecture:
//   - Snapshot: full state dump written atomically to a .snap file
//   - WAL (write-ahead log): delta entries appended to a .wal file after each block
//   - Recovery: load latest snapshot → replay WAL entries on top
//
// All I/O uses encoding/gob (stdlib). No external dependencies.
package store

import (
	"encoding/gob"
	"fmt"
	"os"
	"sync"
)

// Snapshot is a complete point-in-time dump of the KV store.
type Snapshot struct {
	Height int64
	Data   map[string][]byte // key → gob-encoded value
}

// SnapshotStore writes and reads full-state snapshots to disk.
type SnapshotStore struct {
	mu   sync.Mutex
	path string
}

func NewSnapshotStore(path string) *SnapshotStore {
	return &SnapshotStore{path: path}
}

// Write atomically replaces the snapshot file. Uses temp-file + rename for safety.
func (s *SnapshotStore) Write(snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create snapshot temp: %w", err)
	}
	if err := gob.NewEncoder(f).Encode(snap); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode snapshot: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync snapshot: %w", err)
	}
	f.Close()
	return os.Rename(tmp, s.path)
}

// Read loads the snapshot from disk. Returns an empty snapshot if the file
// does not exist yet (first boot).
func (s *SnapshotStore) Read() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return Snapshot{Data: make(map[string][]byte)}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("open snapshot: %w", err)
	}
	defer f.Close()

	var snap Snapshot
	if err := gob.NewDecoder(f).Decode(&snap); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if snap.Data == nil {
		snap.Data = make(map[string][]byte)
	}
	return snap, nil
}
