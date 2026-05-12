package evidence

import (
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
)

// BatchHashMismatch is the payload for EvidenceFrontRun when a proposer
// submits transactions in a non-canonical order.
type BatchHashMismatch struct {
	ProposerId       string
	Height           int64
	ExpectedBatchHash string
	ActualBatchHash   string
	ProposedTxHashes  []string
	CanonicalTxHashes []string
}

// FairBatchDetector watches incoming blocks and checks that each block's
// transaction ordering matches the canonical FairBatch sort. Any deviation
// is recorded as evidence of Byzantine proposer behaviour.
type FairBatchDetector struct {
	mu       sync.Mutex
	evidence []Evidence
}

func NewFairBatchDetector() *FairBatchDetector {
	return &FairBatchDetector{}
}

// CheckBlock inspects the proposed batch from `proposerId` at `height`.
// If the ordering is not canonical, it records and returns evidence.
// Returns nil if the batch is valid.
func (d *FairBatchDetector) CheckBlock(proposerId string, height int64, batch fairbatch.FairBatch) *Evidence {
	// Re-sort the proposed transactions to get the canonical ordering.
	canonical, canonicalHash := fairbatch.SortAndHash(batch.Transactions, height)

	if canonicalHash == batch.BatchHash {
		return nil // ordering is canonical
	}

	// Build per-position TxHash lists for the evidence report.
	proposed := make([]string, len(batch.Transactions))
	for i, tx := range batch.Transactions {
		proposed[i] = tx.TxHash
	}
	expectedOrder := make([]string, len(canonical))
	for i, tx := range canonical {
		expectedOrder[i] = tx.TxHash
	}

	ev := NewEvidence(
		EvidenceFrontRun,
		height,
		"proposer submitted non-canonical tx ordering",
		BatchHashMismatch{
			ProposerId:        proposerId,
			Height:            height,
			ExpectedBatchHash: canonicalHash,
			ActualBatchHash:   batch.BatchHash,
			ProposedTxHashes:  proposed,
			CanonicalTxHashes: expectedOrder,
		},
	)

	d.mu.Lock()
	d.evidence = append(d.evidence, ev)
	d.mu.Unlock()
	return &ev
}

// AllEvidence returns all recorded evidence items (copies).
func (d *FairBatchDetector) AllEvidence() []Evidence {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Evidence, len(d.evidence))
	copy(out, d.evidence)
	return out
}

// DoubleSignPayload records two conflicting batches from the same proposer at the same height.
type DoubleSignPayload struct {
	ProposerId string
	Height     int64
	BatchHashA string
	BatchHashB string
}

// DoubleSignDetector detects when the same proposer broadcasts different
// batches to different nodes at the same block height.
type DoubleSignDetector struct {
	mu      sync.Mutex
	seen    map[string]string   // "proposerId:height" → batchHash first seen
	evidence []Evidence
}

func NewDoubleSignDetector() *DoubleSignDetector {
	return &DoubleSignDetector{seen: make(map[string]string)}
}

// RecordProposal records a batch from a proposer at a height.
// If a conflicting batch is already recorded, it returns evidence.
func (d *DoubleSignDetector) RecordProposal(proposerId string, height int64, batchHash string) *Evidence {
	key := proposerId + ":" + string(rune(height))

	d.mu.Lock()
	defer d.mu.Unlock()

	prior, exists := d.seen[key]
	if !exists {
		d.seen[key] = batchHash
		return nil
	}
	if prior == batchHash {
		return nil // same proposal, no conflict
	}

	ev := NewEvidence(
		EvidenceDoubleSign,
		height,
		"proposer submitted conflicting batches at same height",
		DoubleSignPayload{
			ProposerId: proposerId,
			Height:     height,
			BatchHashA: prior,
			BatchHashB: batchHash,
		},
	)
	d.evidence = append(d.evidence, ev)
	return &ev
}

// AllEvidence returns all recorded double-sign evidence.
func (d *DoubleSignDetector) AllEvidence() []Evidence {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Evidence, len(d.evidence))
	copy(out, d.evidence)
	return out
}
