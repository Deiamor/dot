package scenario_test

// Stage 12: Auto-slashing via evidence detectors.
//
// Verifies that DoubleSignDetector and FairBatchDetector automatically slash
// validators when Byzantine behaviour is detected:
//
//  1. DoubleSignDetector: same proposer + same height + different hashes → slash
//  2. DoubleSignDetector: same hash submitted twice → no slash (idempotent)
//  3. DoubleSignDetector: without slasher wired → evidence only, no slash
//  4. FairBatchDetector: out-of-canonical-order txs → slash proposer
//  5. FairBatchDetector: canonical order → no evidence
//  6. FairBatchDetector: without slasher wired → evidence only, no slash

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/evidence"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// slashableNode returns a node with one bonded validator that can be slashed.
func slashableNode(t *testing.T, valId string) *node.LocalNode {
	t.Helper()
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddBondValidator(valId, valId, "aabb", 100_000).Build())
	if err != nil {
		t.Fatalf("bond validator %s: %v", valId, err)
	}
	return n
}

// -------------------------------------------------------------------------
// Scenario 1: DoubleSignDetector auto-slashes on conflicting proposals
// -------------------------------------------------------------------------
func TestAutoSlash_DoubleSign_Slashes(t *testing.T) {
	n := slashableNode(t, "val-ds01")
	stakeBeforeSlash := n.GetValidatorStake("val-ds01")

	detector := evidence.NewDoubleSignDetector()
	detector.SetSlasher(n)

	// First proposal — recorded, no conflict.
	ev1 := detector.RecordProposal("val-ds01", 2, "hash-aaa")
	if ev1 != nil {
		t.Errorf("first proposal should not produce evidence, got %v", ev1)
	}

	// Second proposal at same height with different hash — double-sign!
	ev2 := detector.RecordProposal("val-ds01", 2, "hash-bbb")
	if ev2 == nil {
		t.Fatal("expected double-sign evidence, got nil")
	}
	if ev2.Type != evidence.EvidenceDoubleSign {
		t.Errorf("evidence type: want DOUBLE_SIGN got %s", ev2.Type)
	}

	// Validator should have been slashed.
	stakeAfter := n.GetValidatorStake("val-ds01")
	if stakeAfter >= stakeBeforeSlash {
		t.Errorf("stake not reduced: before=%d after=%d", stakeBeforeSlash, stakeAfter)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Same hash submitted twice — idempotent, no slash
// -------------------------------------------------------------------------
func TestAutoSlash_DoubleSign_SameHashIdempotent(t *testing.T) {
	n := slashableNode(t, "val-ds02")
	stakeInit := n.GetValidatorStake("val-ds02")

	detector := evidence.NewDoubleSignDetector()
	detector.SetSlasher(n)

	detector.RecordProposal("val-ds02", 2, "hash-same")
	ev := detector.RecordProposal("val-ds02", 2, "hash-same") // same hash
	if ev != nil {
		t.Errorf("same hash submitted twice should not produce evidence, got %v", ev)
	}
	if n.GetValidatorStake("val-ds02") != stakeInit {
		t.Errorf("stake should not change on idempotent submission")
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Without slasher wired — evidence recorded but no slash
// -------------------------------------------------------------------------
func TestAutoSlash_DoubleSign_NoSlasherWired(t *testing.T) {
	n := slashableNode(t, "val-ds03")
	stakeInit := n.GetValidatorStake("val-ds03")

	detector := evidence.NewDoubleSignDetector() // no SetSlasher

	detector.RecordProposal("val-ds03", 2, "hash-x")
	ev := detector.RecordProposal("val-ds03", 2, "hash-y")
	if ev == nil {
		t.Fatal("expected evidence even without slasher")
	}
	if ev.Type != evidence.EvidenceDoubleSign {
		t.Errorf("evidence type: want DOUBLE_SIGN got %s", ev.Type)
	}
	// No slash because no slasher is wired.
	if n.GetValidatorStake("val-ds03") != stakeInit {
		t.Errorf("stake should not change without slasher")
	}
}

// -------------------------------------------------------------------------
// Scenario 4: FairBatchDetector auto-slashes on out-of-order txs
// -------------------------------------------------------------------------
func TestAutoSlash_FairBatch_SlashesOnBadOrder(t *testing.T) {
	const height = int64(5)
	n := slashableNode(t, "val-fb01")
	stakeInit := n.GetValidatorStake("val-fb01")

	detector := evidence.NewFairBatchDetector()
	detector.SetSlasher(n)

	// Build a batch with two txs intentionally in reversed (non-canonical) order.
	tx1 := fairbatch.Transaction{TxType: fairbatch.TxDeposit, TxHash: fairbatch.ComputeTxHash(fairbatch.Transaction{TxType: fairbatch.TxDeposit}, height)}
	tx2 := fairbatch.Transaction{TxType: fairbatch.TxSubmitOrder, TxHash: fairbatch.ComputeTxHash(fairbatch.Transaction{TxType: fairbatch.TxSubmitOrder}, height)}

	// Sort canonically first to get the canonical hash, then reverse.
	txs := []fairbatch.Transaction{tx1, tx2}
	_, canonicalHash := fairbatch.SortAndHash(txs, height)

	// Reverse the order so it's non-canonical.
	reversed := []fairbatch.Transaction{tx2, tx1}
	reversedHash := fairbatch.ComputeTxHash(fairbatch.Transaction{}, height) // just need a different hash
	_ = reversedHash

	batch := fairbatch.FairBatch{
		BlockHeight:  height,
		Transactions: reversed,
		BatchHash:    canonicalHash + "-tampered", // wrong hash for reversed order
	}

	ev := detector.CheckBlock("val-fb01", height, batch)
	if ev == nil {
		t.Fatal("expected front-run evidence for non-canonical ordering")
	}
	if ev.Type != evidence.EvidenceFrontRun {
		t.Errorf("evidence type: want FRONT_RUN got %s", ev.Type)
	}

	// Validator should have been slashed.
	if n.GetValidatorStake("val-fb01") >= stakeInit {
		t.Errorf("validator not slashed after front-run evidence: stake=%d", n.GetValidatorStake("val-fb01"))
	}
}

// -------------------------------------------------------------------------
// Scenario 5: FairBatchDetector — canonical order produces no evidence
// -------------------------------------------------------------------------
func TestAutoSlash_FairBatch_CanonicalNoEvidence(t *testing.T) {
	const height = int64(5)
	n := slashableNode(t, "val-fb02")

	detector := evidence.NewFairBatchDetector()
	detector.SetSlasher(n)

	tx1 := fairbatch.Transaction{TxType: fairbatch.TxDeposit}
	tx2 := fairbatch.Transaction{TxType: fairbatch.TxSubmitOrder}
	txs := []fairbatch.Transaction{tx1, tx2}
	sorted, canonicalHash := fairbatch.SortAndHash(txs, height)

	batch := fairbatch.FairBatch{
		BlockHeight:  height,
		Transactions: sorted,
		BatchHash:    canonicalHash,
	}

	ev := detector.CheckBlock("val-fb02", height, batch)
	if ev != nil {
		t.Errorf("canonical ordering should not produce evidence, got %v", ev)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: FairBatchDetector without slasher — evidence only, no slash
// -------------------------------------------------------------------------
func TestAutoSlash_FairBatch_NoSlasherWired(t *testing.T) {
	const height = int64(5)
	n := slashableNode(t, "val-fb03")
	stakeInit := n.GetValidatorStake("val-fb03")

	detector := evidence.NewFairBatchDetector() // no SetSlasher

	tx1 := fairbatch.Transaction{TxType: fairbatch.TxDeposit, TxHash: "aaa"}
	tx2 := fairbatch.Transaction{TxType: fairbatch.TxSubmitOrder, TxHash: "zzz"}
	_, canonicalHash := fairbatch.SortAndHash([]fairbatch.Transaction{tx1, tx2}, height)

	batch := fairbatch.FairBatch{
		BlockHeight:  height,
		Transactions: []fairbatch.Transaction{tx2, tx1}, // reversed
		BatchHash:    canonicalHash + "-tampered",
	}

	ev := detector.CheckBlock("val-fb03", height, batch)
	if ev == nil {
		t.Fatal("expected evidence even without slasher")
	}
	// No slash.
	if n.GetValidatorStake("val-fb03") != stakeInit {
		t.Errorf("stake should not change without slasher")
	}
}
