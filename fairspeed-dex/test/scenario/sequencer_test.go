package scenario_test

// Stage 5: Multi-sequencer SPOF elimination tests.
//
// Verifies:
//  1. GET /healthz → 200 on a healthy sequencer
//  2. GET /healthz → 503 when queue is at ≥95% capacity
//  3. SequencerPool routes submissions to healthy sequencers
//  4. SequencerPool skips a dead sequencer and falls back to a live one
//  5. SequencerPool returns error when all sequencers are down
//  6. Active-active: both sequencers accept the same tx (idempotent)

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/sequencer"
)

// newTestSequencer creates a Sequencer backed by an httptest.Server.
// Returns the server and the sequencer (not started via Start() — the test
// uses httptest.NewServer to serve the same mux routes directly).
func newTestSequencer(queueDepth int) (*sequencer.Sequencer, *httptest.Server) {
	cfg := sequencer.Config{
		BatchInterval: 10 * time.Millisecond,
		MaxBatchSize:  10,
		QueueDepth:    queueDepth,
	}
	seq := sequencer.New(cfg)
	ts := httptest.NewServer(seq.Handler())
	return seq, ts
}

// -------------------------------------------------------------------------
// Scenario 1: /healthz returns 200 for a healthy sequencer
// -------------------------------------------------------------------------
func TestSequencer_Healthz_Healthy(t *testing.T) {
	_, ts := newTestSequencer(100)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 got %d", resp.StatusCode)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: /healthz returns 503 when queue is saturated (≥95%)
// -------------------------------------------------------------------------
func TestSequencer_Healthz_Full(t *testing.T) {
	// Queue depth of 4 — fill 4 slots so depth/cap = 100% ≥ 95%.
	seq, ts := newTestSequencer(4)
	defer ts.Close()

	// Fill queue directly via exported FillForTest helper.
	seq.FillForTest(4)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 got %d", resp.StatusCode)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Pool routes to a healthy sequencer
// -------------------------------------------------------------------------
func TestSequencerPool_SubmitsToHealthy(t *testing.T) {
	var received atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/tx":
			received.Add(1)
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer ts.Close()

	pool := sequencer.NewSequencerPool([]string{ts.URL})
	if err := pool.Submit(context.Background(), []byte(`{"type":1}`)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if received.Load() != 1 {
		t.Errorf("expected 1 received, got %d", received.Load())
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Pool fails over from a dead sequencer to a live one
// -------------------------------------------------------------------------
func TestSequencerPool_Failover(t *testing.T) {
	var received atomic.Int32

	// Second sequencer — always healthy and accepts txs.
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/tx":
			received.Add(1)
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer live.Close()

	// First sequencer — immediately returns 503 on /healthz and 500 on /tx.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()

	pool := sequencer.NewSequencerPool([]string{dead.URL, live.URL})
	// Run one health probe cycle so the pool knows dead is unhealthy.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.StartHealthChecks(ctx, 50*time.Millisecond)
	time.Sleep(80 * time.Millisecond) // wait for first probe + tick

	if err := pool.Submit(context.Background(), []byte(`{"type":1}`)); err != nil {
		t.Fatalf("Submit after failover: %v", err)
	}
	if received.Load() != 1 {
		t.Errorf("live sequencer should have received 1 tx, got %d", received.Load())
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Pool returns error when all sequencers are down
// -------------------------------------------------------------------------
func TestSequencerPool_AllDown(t *testing.T) {
	dead1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead1.Close()
	dead2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead2.Close()

	pool := sequencer.NewSequencerPool([]string{dead1.URL, dead2.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.StartHealthChecks(ctx, 50*time.Millisecond)
	time.Sleep(80 * time.Millisecond)

	err := pool.Submit(context.Background(), []byte(`{"type":1}`))
	if err == nil {
		t.Error("expected error when all sequencers are down, got nil")
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Active-active — both sequencers accept the same tx (idempotent)
// -------------------------------------------------------------------------
func TestSequencerPool_ActiveActive_BothAccept(t *testing.T) {
	var count1, count2 atomic.Int32

	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/tx":
			count1.Add(1)
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer ts1.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/tx":
			count2.Add(1)
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer ts2.Close()

	pool := sequencer.NewSequencerPool([]string{ts1.URL, ts2.URL})

	// Submit 4 txs — they should spread across both sequencers.
	tx := []byte(`{"type":1,"payload":{}}`)
	for i := 0; i < 4; i++ {
		if err := pool.Submit(context.Background(), tx); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	total := count1.Load() + count2.Load()
	if total != 4 {
		t.Errorf("expected 4 total submissions, got %d (seq1=%d, seq2=%d)",
			total, count1.Load(), count2.Load())
	}
	// Both sequencers should have received at least one tx (round-robin).
	if count1.Load() == 0 || count2.Load() == 0 {
		t.Errorf("expected both sequencers to receive txs, got seq1=%d seq2=%d",
			count1.Load(), count2.Load())
	}
}
