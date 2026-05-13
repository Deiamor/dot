// Package sequencer implements the fairspeed-dex sequencer service.
//
// The sequencer sits in front of the validator set and provides:
//  1. A single HTTP ingress for transaction submission (POST /tx)
//  2. Time-windowed batching — transactions accumulate for up to BatchInterval
//     (default 100 ms) before being flushed.
//  3. Round-robin broadcast to validator CometBFT RPC endpoints via
//     broadcast_tx_async, so no single validator is a bottleneck.
//  4. Backpressure — if the internal queue is full, /tx returns 429.
//  5. Health check endpoint (GET /healthz) for load-balancer probing.
//
// Multiple sequencer instances can run simultaneously (active-active).
// CometBFT deduplicates transactions by hash in its mempool, so the same
// tx submitted through two sequencers is safe and results in one execution.
// Use SequencerPool to route submissions across healthy instances.
//
// The sequencer does NOT participate in consensus; it is a pure ingress
// service. Validators execute and order transactions via PrepareProposal /
// FinalizeBlock (FairBatch ordering is enforced on-chain).
package sequencer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config holds tunable parameters for the Sequencer.
type Config struct {
	// ListenAddr is the HTTP listen address (e.g. ":9090").
	ListenAddr string

	// ValidatorRPCs is the list of CometBFT RPC endpoints to broadcast to.
	// Format: "http://host:port" (the JSON-RPC port, typically P2P+10).
	ValidatorRPCs []string

	// BatchInterval is how long to buffer transactions before flushing.
	BatchInterval time.Duration

	// MaxBatchSize caps the number of transactions per flush cycle.
	// If reached before BatchInterval, the batch is flushed immediately.
	MaxBatchSize int

	// QueueDepth is the maximum number of pending transactions in the queue.
	// Submissions that would exceed this limit are rejected with HTTP 429.
	QueueDepth int
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:    ":9090",
		BatchInterval: 100 * time.Millisecond,
		MaxBatchSize:  500,
		QueueDepth:    10_000,
	}
}

// Stats holds live counters exposed via GET /status.
type Stats struct {
	Received   uint64 `json:"received"`
	Broadcast  uint64 `json:"broadcast"`
	Dropped    uint64 `json:"dropped"`
	QueueDepth int    `json:"queue_depth"`
}

// Sequencer accepts transactions, batches them, and broadcasts to validators.
type Sequencer struct {
	cfg    Config
	queue  chan []byte
	robin  atomic.Int64
	client *http.Client

	mu    sync.Mutex
	stats Stats
}

// New creates a Sequencer. Call Start to begin accepting requests.
func New(cfg Config) *Sequencer {
	if cfg.BatchInterval == 0 {
		cfg.BatchInterval = 100 * time.Millisecond
	}
	if cfg.MaxBatchSize == 0 {
		cfg.MaxBatchSize = 500
	}
	if cfg.QueueDepth == 0 {
		cfg.QueueDepth = 10_000
	}
	return &Sequencer{
		cfg:    cfg,
		queue:  make(chan []byte, cfg.QueueDepth),
		client: &http.Client{Timeout: 2 * time.Second},
	}
}

// Start begins the batch loop and the HTTP server. It blocks until ctx is
// cancelled or the server encounters a fatal error.
func (s *Sequencer) Start(ctx context.Context) error {
	go s.batchLoop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/tx", s.handleSubmit)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/healthz", s.handleHealthz)

	srv := &http.Server{Addr: s.cfg.ListenAddr, Handler: mux}

	// Shut down gracefully when ctx is cancelled.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()

	log.Printf("[sequencer] listening on %s, validators=%v, interval=%v",
		s.cfg.ListenAddr, s.cfg.ValidatorRPCs, s.cfg.BatchInterval)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleSubmit accepts a raw WireTx JSON body and enqueues it.
//
// POST /tx
// Content-Type: application/json
// Body: WireTx JSON (see internal/abci/codec.go)
//
// Response 202: accepted
// Response 429: queue full
// Response 400: bad request
func (s *Sequencer) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16)) // 64 KB max
	if err != nil || len(body) == 0 {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Basic JSON validity check — full validation happens in CheckTx on the
	// validator side, so we only reject clearly malformed payloads here.
	if !json.Valid(body) {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	select {
	case s.queue <- body:
		atomic.AddUint64(&s.stats.Received, 1)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintln(w, `{"ok":true}`)
	default:
		atomic.AddUint64(&s.stats.Dropped, 1)
		http.Error(w, "queue full", http.StatusTooManyRequests)
	}
}

// handleHealthz is the liveness/readiness probe for load balancers.
//
// GET /healthz
// Response 200: {"status":"ok","queue_depth":N}   — healthy, accepting txs
// Response 503: {"status":"full","queue_depth":N}  — queue saturated (≥95%)
func (s *Sequencer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	depth := len(s.queue)
	cap := cap(s.queue)
	// Treat ≥95% fill as degraded so the pool can route away before 429.
	if cap > 0 && depth*100/cap >= 95 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"status":"full","queue_depth":%d}`, depth)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","queue_depth":%d}`, depth)
}

// handleStatus returns live sequencer metrics as JSON.
//
// GET /status
func (s *Sequencer) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := Stats{
		Received:   atomic.LoadUint64(&s.stats.Received),
		Broadcast:  atomic.LoadUint64(&s.stats.Broadcast),
		Dropped:    atomic.LoadUint64(&s.stats.Dropped),
		QueueDepth: len(s.queue),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap) //nolint:errcheck
}

// batchLoop drains the queue on a timer and flushes batches to validators.
func (s *Sequencer) batchLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.BatchInterval)
	defer ticker.Stop()

	pending := make([][]byte, 0, s.cfg.MaxBatchSize)

	flush := func() {
		if len(pending) == 0 {
			return
		}
		s.broadcast(pending)
		pending = pending[:0]
	}

	for {
		select {
		case tx := <-s.queue:
			pending = append(pending, tx)
			if len(pending) >= s.cfg.MaxBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush() // drain remaining
			return
		}
	}
}

// broadcast sends each transaction to a validator via round-robin.
func (s *Sequencer) broadcast(txs [][]byte) {
	if len(s.cfg.ValidatorRPCs) == 0 {
		return
	}
	for _, raw := range txs {
		idx := s.robin.Add(1) % int64(len(s.cfg.ValidatorRPCs))
		endpoint := s.cfg.ValidatorRPCs[idx]
		if err := s.broadcastOne(endpoint, raw); err != nil {
			log.Printf("[sequencer] broadcast to %s failed: %v", endpoint, err)
		} else {
			atomic.AddUint64(&s.stats.Broadcast, 1)
		}
	}
}

// Handler returns an http.Handler for use with httptest.NewServer.
// The batch loop is not started by this method; call Start for production use.
func (s *Sequencer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/tx", s.handleSubmit)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/healthz", s.handleHealthz)
	return mux
}

// FillForTest stuffs n dummy bytes into the queue, for health-check unit tests.
func (s *Sequencer) FillForTest(n int) {
	dummy := []byte("{}")
	for i := 0; i < n; i++ {
		select {
		case s.queue <- dummy:
		default:
		}
	}
}

// broadcastOne calls CometBFT's broadcast_tx_async JSON-RPC endpoint.
func (s *Sequencer) broadcastOne(rpcEndpoint string, txBytes []byte) error {
	encoded := base64.StdEncoding.EncodeToString(txBytes)
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_async","params":{"tx":"%s"}}`,
		encoded,
	)
	resp, err := s.client.Post(rpcEndpoint, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("rpc %s returned %d", rpcEndpoint, resp.StatusCode)
	}
	return nil
}
