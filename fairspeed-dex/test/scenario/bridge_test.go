package scenario_test

// Stage 20: Cross-chain bridge attestation tests.
//
// Verifies:
//  1. Single validator with 100% stake attests → quorum reached, funds credited
//  2. Two-of-three validators required: first attestation insufficient, second reaches quorum
//  3. Same validator attesting twice is idempotent (no double-credit)
//  4. Non-bonded / unknown validator attestation is rejected
//  5. Quorum not reached when only 1 of 3 validators attests (<2/3 stake)
//  6. EventBridgeCompleted emitted with correct payload at quorum

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// -------------------------------------------------------------------------
// Scenario 1: Single validator with full stake → deposit completed immediately
// -------------------------------------------------------------------------
func TestBridge_SingleValidatorFullQuorum(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-br1", "br1", "pubbr1", 100_000).Build())

	balBefore := n.GetBalance(aliceId, "USDC").Available

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddBridgeAttest("dep-001", aliceId, "USDC", 50_000, "Ethereum", "val-br1").Build())
	if err != nil {
		t.Fatalf("SubmitBatch error: %v", err)
	}

	deposit, ok := n.GetBridgeDeposit("dep-001")
	if !ok {
		t.Fatal("bridge deposit not found")
	}
	if !deposit.Completed {
		t.Error("expected deposit to be completed (quorum reached with 100% stake)")
	}

	balAfter := n.GetBalance(aliceId, "USDC").Available
	if balAfter-balBefore != 50_000 {
		t.Errorf("expected balance increase of 50_000, got %d", balAfter-balBefore)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Two attestations needed — first insufficient, second reaches quorum
// -------------------------------------------------------------------------
func TestBridge_TwoAttestationsReachQuorum(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	// val-a: 40_000, val-b: 60_000 → total 100_000; quorum requires >66_666
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-ba", "ba", "pubba", 40_000).
		AddBondValidator("val-bb", "bb", "pubbb", 60_000).Build())

	balBefore := n.GetBalance(aliceId, "USDC").Available

	// First attestation: 40_000 < 66_667 → not yet complete.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddBridgeAttest("dep-002", aliceId, "USDC", 25_000, "Ethereum", "val-ba").Build())

	dep1, _ := n.GetBridgeDeposit("dep-002")
	if dep1.Completed {
		t.Error("deposit should NOT be completed after first attestation (only 40% stake)")
	}
	bal1 := n.GetBalance(aliceId, "USDC").Available
	if bal1 != balBefore {
		t.Errorf("no funds should be credited before quorum: got delta %d", bal1-balBefore)
	}

	// Second attestation: 40_000 + 60_000 = 100_000 > 66_666 → quorum reached.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddBridgeAttest("dep-002", aliceId, "USDC", 25_000, "Ethereum", "val-bb").Build())

	dep2, _ := n.GetBridgeDeposit("dep-002")
	if !dep2.Completed {
		t.Error("deposit should be completed after second attestation (100% stake)")
	}
	balAfter := n.GetBalance(aliceId, "USDC").Available
	if balAfter-balBefore != 25_000 {
		t.Errorf("expected balance increase of 25_000, got %d", balAfter-balBefore)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Duplicate attestation from same validator is idempotent
// -------------------------------------------------------------------------
func TestBridge_DuplicateAttestationIdempotent(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-dup", "dup", "pubdup", 100_000).Build())

	// First attestation → quorum reached, funds credited.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddBridgeAttest("dep-003", aliceId, "USDC", 10_000, "Ethereum", "val-dup").Build())

	balAfterFirst := n.GetBalance(aliceId, "USDC").Available

	// Second attestation from same validator → should be ignored.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddBridgeAttest("dep-003", aliceId, "USDC", 10_000, "Ethereum", "val-dup").Build())

	balAfterSecond := n.GetBalance(aliceId, "USDC").Available
	if balAfterSecond != balAfterFirst {
		t.Errorf("duplicate attestation should not credit funds again: balance changed by %d", balAfterSecond-balAfterFirst)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Non-bonded / unknown validator attestation rejected
// -------------------------------------------------------------------------
func TestBridge_UnknownValidatorRejected(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	// Do NOT bond any validators.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBridgeAttest("dep-004", aliceId, "USDC", 5_000, "Ethereum", "ghost-val").Build())
	if err == nil {
		t.Error("expected error when attesting with unknown/non-bonded validator")
	}

	_, ok := n.GetBridgeDeposit("dep-004")
	if ok {
		t.Error("deposit should not exist after rejected attestation")
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Quorum NOT reached when only 1 of 3 validators attests
// -------------------------------------------------------------------------
func TestBridge_QuorumNotReached(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	// Three equal validators: 33_333 each → total 99_999; quorum needs >66_666.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-q1", "q1", "pubq1", 33_333).
		AddBondValidator("val-q2", "q2", "pubq2", 33_333).
		AddBondValidator("val-q3", "q3", "pubq3", 33_333).Build())

	balBefore := n.GetBalance(aliceId, "USDC").Available

	// Only 1 validator attests (33_333 / 99_999 ≈ 33% < 67%).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddBridgeAttest("dep-005", aliceId, "USDC", 20_000, "Ethereum", "val-q1").Build())

	dep, ok := n.GetBridgeDeposit("dep-005")
	if !ok {
		t.Fatal("deposit record should exist after first attestation")
	}
	if dep.Completed {
		t.Error("deposit should NOT be completed with only 1/3 stake attested")
	}
	balAfter := n.GetBalance(aliceId, "USDC").Available
	if balAfter != balBefore {
		t.Errorf("no funds should be credited without quorum: delta=%d", balAfter-balBefore)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: EventBridgeCompleted emitted with correct payload
// -------------------------------------------------------------------------
func TestBridge_EventPayload(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-ev2", "ev2", "pubev2", 100_000).Build())

	var completed state.BridgeCompletedPayload
	n.Subscribe(state.EventBridgeCompleted, func(e state.Event) {
		completed = e.Payload.(state.BridgeCompletedPayload)
	})

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddBridgeAttest("dep-006", aliceId, "USDC", 7_777, "Ethereum", "val-ev2").Build())

	if completed.DepositId != "dep-006" {
		t.Errorf("depositId: want dep-006, got %s", completed.DepositId)
	}
	if completed.AccountId != aliceId {
		t.Errorf("accountId: want %s, got %s", aliceId, completed.AccountId)
	}
	if completed.AssetId != "USDC" {
		t.Errorf("assetId: want USDC, got %s", completed.AssetId)
	}
	if completed.Amount != 7_777 {
		t.Errorf("amount: want 7777, got %d", completed.Amount)
	}
	if completed.BlockHeight == 0 {
		t.Error("blockHeight should be populated")
	}
}
