package evidence

import (
	"fmt"
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
)

// Slasher is the minimum interface the detectors need to auto-slash validators.
// validator.ValidatorKeeper satisfies this interface.
type Slasher interface {
	SlashDoubleSign(validatorId string, blockHeight int64) (int64, error)
	SlashFrontRun(validatorId string, blockHeight int64) (int64, error)
}

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
// is recorded as evidence of Byzantine proposer behaviour and, if a Slasher
// is configured, the proposer is slashed automatically.
type FairBatchDetector struct {
	mu       sync.Mutex
	evidence []Evidence
	slasher  Slasher
}

func NewFairBatchDetector() *FairBatchDetector {
	return &FairBatchDetector{}
}

// SetSlasher wires the detector to auto-slash on detected front-running.
func (d *FairBatchDetector) SetSlasher(s Slasher) {
	d.mu.Lock()
	d.slasher = s
	d.mu.Unlock()
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

	var slashDesc string
	if d.slasher != nil {
		if slashed, err := d.slasher.SlashFrontRun(proposerId, height); err == nil {
			slashDesc = fmt.Sprintf(" (auto-slashed %d)", slashed)
		}
	}

	ev := NewEvidence(
		EvidenceFrontRun,
		height,
		"proposer submitted non-canonical tx ordering"+slashDesc,
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
// batches to different nodes at the same block height. If a Slasher is
// configured, the proposer is slashed automatically when evidence is found.
type DoubleSignDetector struct {
	mu       sync.Mutex
	seen     map[string]string // "proposerId:height" → batchHash first seen
	evidence []Evidence
	slasher  Slasher
}

func NewDoubleSignDetector() *DoubleSignDetector {
	return &DoubleSignDetector{seen: make(map[string]string)}
}

// SetSlasher wires the detector to auto-slash on detected double-sign.
func (d *DoubleSignDetector) SetSlasher(s Slasher) {
	d.mu.Lock()
	d.slasher = s
	d.mu.Unlock()
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

	var slashDesc string
	if d.slasher != nil {
		if slashed, err := d.slasher.SlashDoubleSign(proposerId, height); err == nil {
			slashDesc = fmt.Sprintf(" (auto-slashed %d)", slashed)
		}
	}

	ev := NewEvidence(
		EvidenceDoubleSign,
		height,
		"proposer submitted conflicting batches at same height"+slashDesc,
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
