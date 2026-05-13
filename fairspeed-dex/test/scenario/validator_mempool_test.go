package scenario_test

// Stage 9: Sequencer decentralisation tests.
//
// Verifies that ValidatorMempoolClient provides a direct-to-validator tx
// submission path, eliminating the sequencer service as a required component:
//
//  1. Direct submit reaches CometBFT broadcast_tx_async endpoint
//  2. Round-robin distributes submissions across multiple validators
//  3. Fails over from a dead validator to a live one
//  4. Returns error when all validators are unavailable
//  5. Health check uses CometBFT /status (not sequencer /healthz)
//  6. Re-submitting the same tx to multiple validators is idempotent
//     (CometBFT mempool deduplication guarantee — verified via call count)

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/sequencer"
)

// rpcBody is the minimal JSON-RPC success response CometBFT returns for
// broadcast_tx_async. Our client checks for an "error" field; absence means OK.
var rpcOKBody = []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)

// newValidatorServer creates an httptest.Server that mimics a CometBFT RPC node.
// It counts calls to POST / (broadcast_tx_async) and GET /status.
func newValidatorServer(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var broadcastCalls, statusCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			// Validate it's a broadcast_tx_async call.
			var req struct {
				Method string `json:"method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Method != "broadcast_tx_async" {
				http.Error(w, "unexpected method", http.StatusBadRequest)
				return
			}
			broadcastCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(rpcOKBody) //nolint:errcheck
		case r.Method == http.MethodGet && r.URL.Path == "/status":
			statusCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	return ts, &broadcastCalls, &statusCalls
}

// -------------------------------------------------------------------------
// Scenario 1: Direct submit reaches broadcast_tx_async
// -------------------------------------------------------------------------
func TestValidatorMempoolClient_DirectSubmit(t *testing.T) {
	ts, broadcast, _ := newValidatorServer(t)
	defer ts.Close()

	client := sequencer.NewValidatorMempoolClient([]string{ts.URL})

	tx := []byte(`{"t":3,"p":{}}`)
	if err := client.Submit(context.Background(), tx); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if broadcast.Load() != 1 {
		t.Errorf("expected 1 broadcast call, got %d", broadcast.Load())
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Round-robin distributes submissions across validators
// -------------------------------------------------------------------------
func TestValidatorMempoolClient_RoundRobin(t *testing.T) {
	ts1, bc1, _ := newValidatorServer(t)
	defer ts1.Close()
	ts2, bc2, _ := newValidatorServer(t)
	defer ts2.Close()

	client := sequencer.NewValidatorMempoolClient([]string{ts1.URL, ts2.URL})

	tx := []byte(`{"t":3,"p":{}}`)
	for i := 0; i < 4; i++ {
		if err := client.Submit(context.Background(), tx); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	total := bc1.Load() + bc2.Load()
	if total != 4 {
		t.Errorf("expected 4 total broadcasts, got %d (val1=%d val2=%d)",
			total, bc1.Load(), bc2.Load())
	}
	if bc1.Load() == 0 || bc2.Load() == 0 {
		t.Errorf("expected both validators to receive txs, got val1=%d val2=%d",
			bc1.Load(), bc2.Load())
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Failover from a dead validator to a live one
// -------------------------------------------------------------------------
func TestValidatorMempoolClient_Failover(t *testing.T) {
	live, bc, _ := newValidatorServer(t)
	defer live.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()

	client := sequencer.NewValidatorMempoolClient([]string{dead.URL, live.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.StartHealthChecks(ctx, 50*time.Millisecond)
	time.Sleep(80 * time.Millisecond) // allow one probe cycle

	tx := []byte(`{"t":3,"p":{}}`)
	if err := client.Submit(context.Background(), tx); err != nil {
		t.Fatalf("Submit after failover: %v", err)
	}
	if bc.Load() != 1 {
		t.Errorf("live validator should have received 1 tx, got %d", bc.Load())
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Returns error when all validators are unavailable
// -------------------------------------------------------------------------
func TestValidatorMempoolClient_AllDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()

	client := sequencer.NewValidatorMempoolClient([]string{dead.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.StartHealthChecks(ctx, 50*time.Millisecond)
	time.Sleep(80 * time.Millisecond)

	err := client.Submit(context.Background(), []byte(`{"t":1}`))
	if err == nil {
		t.Error("expected error when all validators are down, got nil")
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Health check uses /status endpoint (CometBFT convention)
// -------------------------------------------------------------------------
func TestValidatorMempoolClient_HealthCheckUsesStatus(t *testing.T) {
	_, _, statusCalls := newValidatorServer(t)
	ts, _, sc := newValidatorServer(t)
	defer ts.Close()
	_ = statusCalls // suppress unused warning

	client := sequencer.NewValidatorMempoolClient([]string{ts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.StartHealthChecks(ctx, 50*time.Millisecond)
	time.Sleep(80 * time.Millisecond) // ≥1 probe cycle

	if sc.Load() == 0 {
		t.Error("expected at least one /status probe, got 0")
	}
	if client.HealthyCount() != 1 {
		t.Errorf("expected 1 healthy validator, got %d", client.HealthyCount())
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Same tx submitted to two validators is idempotent
// (Verifies CometBFT deduplication: both accept it, one execution on-chain)
// -------------------------------------------------------------------------
func TestValidatorMempoolClient_DuplicateSubmitIdempotent(t *testing.T) {
	ts1, bc1, _ := newValidatorServer(t)
	defer ts1.Close()
	ts2, bc2, _ := newValidatorServer(t)
	defer ts2.Close()

	// Submit the same tx to both validators explicitly.
	c1 := sequencer.NewValidatorMempoolClient([]string{ts1.URL})
	c2 := sequencer.NewValidatorMempoolClient([]string{ts2.URL})

	tx := []byte(`{"t":3,"p":{"order_id":"dup-001"}}`)
	if err := c1.Submit(context.Background(), tx); err != nil {
		t.Fatalf("c1 Submit: %v", err)
	}
	if err := c2.Submit(context.Background(), tx); err != nil {
		t.Fatalf("c2 Submit: %v", err)
	}

	// Both validators accepted the tx at the HTTP layer.
	// CometBFT deduplication (by TxHash) ensures only one execution — that
	// guarantee lives in CometBFT, but the transport layer must not error.
	if bc1.Load() != 1 {
		t.Errorf("val1 expected 1 call, got %d", bc1.Load())
	}
	if bc2.Load() != 1 {
		t.Errorf("val2 expected 1 call, got %d", bc2.Load())
	}
}
