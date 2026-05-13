package sequencer

// ValidatorMempoolClient submits transactions directly to CometBFT validator
// RPC endpoints, eliminating the sequencer service from the critical path.
//
// Architecture comparison:
//
//	With sequencer:  Client → Sequencer HTTP → broadcast_tx_async → CometBFT mempool
//	Direct path:     Client → ValidatorMempoolClient → broadcast_tx_async → CometBFT mempool
//
// FairBatch ordering is enforced by the proposer's PrepareProposal, so removing
// the sequencer does not affect fairness — it only removes a potential SPOF.
//
// CometBFT gossips each admitted tx to all peers, so submitting to any single
// validator is sufficient. Submitting the same tx to multiple validators is
// safe and idempotent: CometBFT deduplicates by TxHash in the mempool.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ValidatorMempoolClient routes tx submissions directly to CometBFT RPC
// endpoints with health-based round-robin failover.
type ValidatorMempoolClient struct {
	endpoints []string
	healthy   []atomic.Bool
	client    *http.Client
	index     atomic.Int64
}

// NewValidatorMempoolClient creates a client for the given CometBFT RPC
// endpoints (e.g. []string{"http://val0:26666", "http://val1:26676"}).
// All endpoints start healthy until the first probe cycle.
func NewValidatorMempoolClient(rpcEndpoints []string) *ValidatorMempoolClient {
	c := &ValidatorMempoolClient{
		endpoints: rpcEndpoints,
		healthy:   make([]atomic.Bool, len(rpcEndpoints)),
		client:    &http.Client{Timeout: 2 * time.Second},
	}
	for i := range c.healthy {
		c.healthy[i].Store(true)
	}
	return c
}

// Submit sends tx to one healthy validator via CometBFT broadcast_tx_async.
// tx must be the encoded WireTx bytes (see internal/abci/codec.go EncodeTx).
// It tries each endpoint in round-robin order, skipping unhealthy ones.
// Returns an error only when every endpoint fails.
func (c *ValidatorMempoolClient) Submit(ctx context.Context, tx []byte) error {
	n := len(c.endpoints)
	if n == 0 {
		return fmt.Errorf("validator mempool client: no endpoints configured")
	}

	start := int(c.index.Add(1)) % n
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if !c.healthy[idx].Load() {
			continue
		}
		if err := c.broadcastOne(ctx, c.endpoints[idx], tx); err == nil {
			return nil
		}
		c.healthy[idx].Store(false)
	}
	return fmt.Errorf("validator mempool client: all endpoints unavailable")
}

// StartHealthChecks probes CometBFT /status on each endpoint at the given
// interval. It runs until ctx is cancelled. interval ≤ 5s is recommended.
func (c *ValidatorMempoolClient) StartHealthChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		c.probeAll()
		for {
			select {
			case <-ticker.C:
				c.probeAll()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// HealthyCount returns the number of currently healthy endpoints.
func (c *ValidatorMempoolClient) HealthyCount() int {
	count := 0
	for i := range c.healthy {
		if c.healthy[i].Load() {
			count++
		}
	}
	return count
}

func (c *ValidatorMempoolClient) probeAll() {
	for i, ep := range c.endpoints {
		c.healthy[i].Store(c.probe(ep))
	}
}

// probe calls CometBFT GET /status; returns true on HTTP 200.
func (c *ValidatorMempoolClient) probe(endpoint string) bool {
	resp, err := c.client.Get(endpoint + "/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// broadcastOne sends a single tx via CometBFT broadcast_tx_async JSON-RPC.
// The tx bytes are base64-encoded in the "params" field per CometBFT spec.
func (c *ValidatorMempoolClient) broadcastOne(ctx context.Context, endpoint string, tx []byte) error {
	encoded := base64.StdEncoding.EncodeToString(tx)
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_async","params":{"tx":"%s"}}`,
		encoded,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rpc %s: status %d: %s", endpoint, resp.StatusCode, raw)
	}
	// Parse the JSON-RPC response to surface application-level errors.
	var rpcResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err == nil && rpcResp.Error != nil {
		return fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return nil
}
