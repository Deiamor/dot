package sequencer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SequencerPool routes transaction submissions across multiple sequencer
// instances with health-based failover.
//
// Active-active model: all healthy sequencers can accept submissions
// simultaneously. CometBFT deduplicates transactions by hash in its mempool,
// so the same tx reaching two sequencers results in exactly one execution.
//
// On Submit, the pool tries sequencers in round-robin order and skips any
// that were marked unhealthy by the most recent health probe. If every
// sequencer is unhealthy the call returns an error immediately (no retries
// against known-bad endpoints).
type SequencerPool struct {
	endpoints []string
	healthy   []atomic.Bool // true = last probe succeeded
	client    *http.Client
	mu        sync.RWMutex
	index     atomic.Int64 // round-robin cursor
}

// NewSequencerPool creates a pool from a list of sequencer base URLs
// (e.g. []string{"http://seq1:9090", "http://seq2:9090"}).
// All endpoints start as healthy until the first probe cycle.
func NewSequencerPool(endpoints []string) *SequencerPool {
	p := &SequencerPool{
		endpoints: endpoints,
		healthy:   make([]atomic.Bool, len(endpoints)),
		client:    &http.Client{Timeout: 2 * time.Second},
	}
	for i := range p.healthy {
		p.healthy[i].Store(true) // optimistic initial state
	}
	return p
}

// Submit sends tx to one healthy sequencer via POST /tx.
// It tries each sequencer in round-robin order, skipping unhealthy ones.
// Returns an error only if no healthy sequencer accepted the request.
func (p *SequencerPool) Submit(ctx context.Context, tx []byte) error {
	n := len(p.endpoints)
	if n == 0 {
		return fmt.Errorf("sequencer pool is empty")
	}

	start := int(p.index.Add(1)) % n
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if !p.healthy[idx].Load() {
			continue
		}
		if err := p.submitOne(ctx, p.endpoints[idx], tx); err == nil {
			return nil
		}
		// Mark as unhealthy on submission failure so the next caller skips it.
		p.healthy[idx].Store(false)
	}
	return fmt.Errorf("all sequencers unavailable")
}

// StartHealthChecks begins periodic probing of all sequencer /healthz
// endpoints. It runs until ctx is cancelled. interval should be ≤ 5s for
// timely failover.
func (p *SequencerPool) StartHealthChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		p.probeAll() // immediate first probe
		for {
			select {
			case <-ticker.C:
				p.probeAll()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// HealthyCount returns the number of currently-healthy sequencers.
func (p *SequencerPool) HealthyCount() int {
	count := 0
	for i := range p.healthy {
		if p.healthy[i].Load() {
			count++
		}
	}
	return count
}

func (p *SequencerPool) probeAll() {
	for i, ep := range p.endpoints {
		ok := p.probe(ep)
		p.healthy[i].Store(ok)
	}
}

func (p *SequencerPool) probe(endpoint string) bool {
	resp, err := p.client.Get(endpoint + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *SequencerPool) submitOne(ctx context.Context, endpoint string, tx []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		endpoint+"/tx", strings.NewReader(string(tx)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("sequencer %s: status %d: %s", endpoint, resp.StatusCode, body)
}
