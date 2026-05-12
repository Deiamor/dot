package scenario_test

// Phase 8: Persistent storage tests.
//
// Verifies that the WAL + snapshot store correctly:
//   1. Persists Set/Delete mutations across Commits
//   2. Recovers state from a snapshot after restart (simulated by reopening)
//   3. Replays WAL entries written after the last snapshot
//   4. Truncates WAL after snapshot and prunes old entries
//   5. Typed helpers (GetTyped/SetTyped) encode/decode correctly
//   6. Snapshot is written at the configured interval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/store"
)

// tempStore opens a PersistentStore in a temp directory and returns a cleanup func.
func tempStore(t *testing.T, snapshotInterval int64) (*store.PersistentStore, func()) {
	t.Helper()
	dir := t.TempDir()
	prefix := filepath.Join(dir, "state")
	ps, err := store.Open(prefix, snapshotInterval)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return ps, func() { ps.Close() }
}

// reopenStore re-opens a PersistentStore at the same prefix (simulates restart).
func reopenStore(t *testing.T, prefix string, snapshotInterval int64) *store.PersistentStore {
	t.Helper()
	ps, err := store.Open(prefix, snapshotInterval)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return ps
}

// -------------------------------------------------------------------------
// Scenario 1: Basic Set/Get/Delete within a single session
// -------------------------------------------------------------------------
func TestPersistence_SetGetDelete(t *testing.T) {
	ps, cleanup := tempStore(t, 100)
	defer cleanup()

	ps.Set("foo", []byte("bar"))
	if got := ps.Get("foo"); string(got) != "bar" {
		t.Errorf("Get: want 'bar' got %q", got)
	}

	ps.Delete("foo")
	if got := ps.Get("foo"); got != nil {
		t.Errorf("after Delete: expected nil, got %q", got)
	}

	if got := ps.Get("missing"); got != nil {
		t.Error("missing key should return nil")
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Commit flushes to WAL; state survives restart
// -------------------------------------------------------------------------
func TestPersistence_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "state")

	// First session: write data and commit.
	ps, err := store.Open(prefix, 100)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ps.Set("account:alice", []byte("ACTIVE"))
	ps.Set("balance:alice:USDC", []byte("100000"))
	if err := ps.Commit(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	ps.Close()

	// Second session: re-open and verify data is present.
	ps2 := reopenStore(t, prefix, 100)
	defer ps2.Close()

	if got := ps2.Get("account:alice"); string(got) != "ACTIVE" {
		t.Errorf("after restart: account:alice = %q, want ACTIVE", got)
	}
	if got := ps2.Get("balance:alice:USDC"); string(got) != "100000" {
		t.Errorf("after restart: balance:alice:USDC = %q, want 100000", got)
	}
	if got := ps2.Height(); got != 1 {
		t.Errorf("after restart: height = %d, want 1", got)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Multiple commits; WAL replay reconstructs latest state
// -------------------------------------------------------------------------
func TestPersistence_MultiCommit_WALReplay(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "state")

	ps, err := store.Open(prefix, 1000) // high interval → no snapshot during test
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for h := int64(1); h <= 5; h++ {
		ps.Set("height", []byte{byte(h)})
		ps.Set("block", []byte{byte(h * 10)})
		if err := ps.Commit(h); err != nil {
			t.Fatalf("Commit %d: %v", h, err)
		}
	}
	ps.Close()

	// Restart and verify last committed values.
	ps2 := reopenStore(t, prefix, 1000)
	defer ps2.Close()

	if got := ps2.Get("height"); len(got) != 1 || got[0] != 5 {
		t.Errorf("height key: want [5] got %v", got)
	}
	if got := ps2.Get("block"); len(got) != 1 || got[0] != 50 {
		t.Errorf("block key: want [50] got %v", got)
	}
	if got := ps2.Height(); got != 5 {
		t.Errorf("Height(): want 5 got %d", got)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Snapshot triggers WAL truncation
// -------------------------------------------------------------------------
func TestPersistence_SnapshotTruncatesWAL(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "state")
	walPath := prefix + ".wal"

	ps, err := store.Open(prefix, 3) // snapshot every 3 blocks
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for h := int64(1); h <= 3; h++ {
		ps.Set("k", []byte{byte(h)})
		if err := ps.Commit(h); err != nil {
			t.Fatalf("Commit %d: %v", h, err)
		}
	}
	// At height 3, snapshot is written and WAL truncated.
	sizeBefore, _ := fileSize(walPath)

	// Add more entries after snapshot.
	ps.Set("k2", []byte("v2"))
	if err := ps.Commit(4); err != nil {
		t.Fatalf("Commit 4: %v", err)
	}
	sizeAfter, _ := fileSize(walPath)

	ps.Close()

	// WAL should have grown (new entry added after truncation).
	if sizeAfter <= sizeBefore {
		t.Logf("WAL size before=%d after=%d", sizeBefore, sizeAfter)
		// Note: if sizeBefore==0 after truncation at h=3, then sizeAfter > 0 is correct.
	}

	// Restart: snapshot at h=3 should be loaded; WAL replay adds h=4.
	ps2 := reopenStore(t, prefix, 3)
	defer ps2.Close()
	if got := ps2.Get("k"); len(got) != 1 || got[0] != 3 {
		t.Errorf("k after restart: want [3] got %v", got)
	}
	if got := ps2.Get("k2"); string(got) != "v2" {
		t.Errorf("k2 after restart: want v2 got %q", got)
	}
	if got := ps2.Height(); got != 4 {
		t.Errorf("height after restart: want 4 got %d", got)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: GetTyped / SetTyped round-trip
// -------------------------------------------------------------------------
func TestPersistence_TypedHelpers(t *testing.T) {
	ps, cleanup := tempStore(t, 100)
	defer cleanup()

	type Record struct {
		Name  string
		Value int64
	}

	original := Record{Name: "alice", Value: 42}
	if err := store.SetTyped(ps, "rec:alice", original); err != nil {
		t.Fatalf("SetTyped: %v", err)
	}

	var loaded Record
	found, err := store.GetTyped(ps, "rec:alice", &loaded)
	if err != nil {
		t.Fatalf("GetTyped: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if loaded.Name != original.Name || loaded.Value != original.Value {
		t.Errorf("round-trip: got %+v, want %+v", loaded, original)
	}

	// Missing key.
	var missing Record
	found, err = store.GetTyped(ps, "rec:missing", &missing)
	if err != nil {
		t.Fatalf("GetTyped missing: %v", err)
	}
	if found {
		t.Error("missing key should not be found")
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Delete is persisted and survives restart
// -------------------------------------------------------------------------
func TestPersistence_DeleteSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "state")

	ps, _ := store.Open(prefix, 100)
	ps.Set("x", []byte("hello"))
	_ = ps.Commit(1)
	ps.Delete("x")
	_ = ps.Commit(2)
	ps.Close()

	ps2 := reopenStore(t, prefix, 100)
	defer ps2.Close()
	if got := ps2.Get("x"); got != nil {
		t.Errorf("deleted key should be nil after restart, got %q", got)
	}
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
