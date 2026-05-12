package store

import (
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"sync"
)

// WALEntry is a single mutation recorded in the write-ahead log.
// Op is "set" or "del"; Key and Value are the affected key/value pair.
type WALEntry struct {
	Height int64
	Op     string // "set" | "del"
	Key    string
	Value  []byte
}

// WAL is an append-only write-ahead log backed by a single file.
// Each call to Append encodes one WALEntry and flushes to disk.
type WAL struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	enc     *gob.Encoder
}

// OpenWAL opens (or creates) the WAL at the given path.
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}
	return &WAL{path: path, f: f, enc: gob.NewEncoder(f)}, nil
}

// Append writes a WALEntry to the log and flushes.
func (w *WAL) Append(entry WALEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(entry); err != nil {
		return fmt.Errorf("encode WAL entry: %w", err)
	}
	return w.f.Sync()
}

// Replay reads all WAL entries written after `afterHeight` and applies them
// to the provided data map. Returns the highest height seen.
func (w *WAL) Replay(afterHeight int64, data map[string][]byte) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Seek to beginning for full replay.
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("WAL seek: %w", err)
	}
	dec := gob.NewDecoder(w.f)

	var maxHeight int64 = afterHeight
	for {
		var entry WALEntry
		if err := dec.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			return maxHeight, fmt.Errorf("WAL decode: %w", err)
		}
		if entry.Height <= afterHeight {
			continue // already captured in snapshot
		}
		switch entry.Op {
		case "set":
			data[entry.Key] = entry.Value
		case "del":
			delete(data, entry.Key)
		}
		if entry.Height > maxHeight {
			maxHeight = entry.Height
		}
	}
	return maxHeight, nil
}

// Truncate rewrites the WAL keeping only entries after `afterHeight`.
// Call this after a new snapshot is written to prune old entries.
func (w *WAL) Truncate(afterHeight int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Read all entries we want to keep.
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	dec := gob.NewDecoder(w.f)
	var keep []WALEntry
	for {
		var entry WALEntry
		if err := dec.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if entry.Height > afterHeight {
			keep = append(keep, entry)
		}
	}

	// Rewrite file.
	tmp := w.path + ".tmp"
	tf, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := gob.NewEncoder(tf)
	for _, e := range keep {
		if err := enc.Encode(e); err != nil {
			tf.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := tf.Sync(); err != nil {
		tf.Close()
		os.Remove(tmp)
		return err
	}
	tf.Close()
	if err := os.Rename(tmp, w.path); err != nil {
		return err
	}

	// Reopen the file for appending.
	w.f.Close()
	w.f, err = os.OpenFile(w.path, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.enc = gob.NewEncoder(w.f)
	return nil
}

// Close closes the underlying file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
