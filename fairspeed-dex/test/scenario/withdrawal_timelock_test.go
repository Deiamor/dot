package scenario_test

// Stage 18: Withdrawal timelock tests.
//
// Verifies:
//  1. TxWithdrawRequest reserves funds and creates a pending withdrawal
//  2. Funds are unavailable during the timelock period
//  3. After ReadyAtHeight, withdrawal auto-finalizes and funds are released
//  4. EventWithdrawRequested has correct fields
//  5. EventWithdrawFinalized is emitted after auto-finalization
//  6. Pending withdrawals persist through snapshot round-trip

import (
	"os"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// bootstrapNodeWithTimelock creates a node with a 3-block withdrawal timelock.
func bootstrapNodeWithTimelock(t *testing.T) (*node.LocalNode, string, string, string, string) {
	t.Helper()
	policy := risk.DefaultRiskPolicy
	policy.WithdrawTimelockBlocks = 3
	return bootstrapNodeWithPolicy(t, policy)
}

// advanceEmptyBlocks submits N empty batches to advance block height.
func advanceEmptyBlocks(t *testing.T, n *node.LocalNode, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		h := n.CurrentHeight() + 1
		_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(h).Build())
		if err != nil {
			t.Fatalf("advance block: %v", err)
		}
	}
}

// -------------------------------------------------------------------------
// Scenario 1: TxWithdrawRequest creates a pending withdrawal
// -------------------------------------------------------------------------
func TestWithdrawTimelock_RequestCreatesPending(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithTimelock(t)

	var requested state.WithdrawRequestedPayload
	n.Subscribe(state.EventWithdrawRequested, func(e state.Event) {
		requested = e.Payload.(state.WithdrawRequestedPayload)
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddWithdrawRequest(aliceId, "USDC", 5_000, n.GetAccountSequence(aliceId), "").Build())
	if err != nil {
		t.Fatalf("withdraw request: %v", err)
	}

	if requested.WithdrawalId == "" {
		t.Fatal("expected EventWithdrawRequested to be emitted")
	}
	if requested.Amount != 5_000 {
		t.Errorf("amount: want 5000 got %d", requested.Amount)
	}
	if requested.ReadyAtHeight != 4+3 {
		t.Errorf("readyAtHeight: want %d got %d", 4+3, requested.ReadyAtHeight)
	}

	pending := n.GetPendingWithdrawals(aliceId)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending withdrawal, got %d", len(pending))
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Funds are reserved (unavailable) during timelock
// -------------------------------------------------------------------------
func TestWithdrawTimelock_FundsReservedDuringDelay(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithTimelock(t)

	balBefore := n.GetBalance(aliceId, "USDC")

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddWithdrawRequest(aliceId, "USDC", 10_000, n.GetAccountSequence(aliceId), "").Build())

	balAfter := n.GetBalance(aliceId, "USDC")

	// Available should decrease, Reserved should increase.
	if balAfter.Available >= balBefore.Available {
		t.Errorf("available should decrease: before=%d after=%d", balBefore.Available, balAfter.Available)
	}
	if balAfter.Reserved <= 0 {
		t.Errorf("reserved should be > 0, got %d", balAfter.Reserved)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Auto-finalization after timelock expires
// -------------------------------------------------------------------------
func TestWithdrawTimelock_AutoFinalizesAfterDelay(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithTimelock(t)

	var finalizedId string
	n.Subscribe(state.EventWithdrawFinalized, func(e state.Event) {
		finalizedId = e.Payload.(state.WithdrawFinalizedPayload).WithdrawalId
	})

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddWithdrawRequest(aliceId, "USDC", 5_000, n.GetAccountSequence(aliceId), "").Build())

	pending := n.GetPendingWithdrawals(aliceId)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending withdrawal, got %d", len(pending))
	}
	wid := pending[0].WithdrawalId

	// Advance 3 blocks — ReadyAtHeight is 4+3=7, so after 3 more blocks (block 7) it finalizes.
	advanceEmptyBlocks(t, n, 3)

	if finalizedId != wid {
		t.Errorf("expected finalized ID %s, got %s", wid, finalizedId)
	}

	remaining := n.GetPendingWithdrawals(aliceId)
	if len(remaining) != 0 {
		t.Errorf("pending withdrawal should be cleared after finalization, got %d", len(remaining))
	}
}

// -------------------------------------------------------------------------
// Scenario 4: EventWithdrawRequested has correct fields
// -------------------------------------------------------------------------
func TestWithdrawTimelock_EventPayload(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithTimelock(t)

	var ev state.WithdrawRequestedPayload
	n.Subscribe(state.EventWithdrawRequested, func(e state.Event) {
		ev = e.Payload.(state.WithdrawRequestedPayload)
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddWithdrawRequest(aliceId, "USDC", 8_000, n.GetAccountSequence(aliceId), "").Build())
	if err != nil {
		t.Fatalf("withdraw request: %v", err)
	}

	if ev.AccountId != aliceId {
		t.Errorf("accountId: want %s got %s", aliceId, ev.AccountId)
	}
	if ev.AssetId != "USDC" {
		t.Errorf("assetId: want USDC got %s", ev.AssetId)
	}
	if ev.Amount != 8_000 {
		t.Errorf("amount: want 8000 got %d", ev.Amount)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: EventWithdrawFinalized emitted after auto-finalization
// -------------------------------------------------------------------------
func TestWithdrawTimelock_FinalizedEvent(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithTimelock(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddWithdrawRequest(aliceId, "USDC", 3_000, n.GetAccountSequence(aliceId), "").Build())

	var finalEv state.WithdrawFinalizedPayload
	n.Subscribe(state.EventWithdrawFinalized, func(e state.Event) {
		finalEv = e.Payload.(state.WithdrawFinalizedPayload)
	})

	advanceEmptyBlocks(t, n, 3)

	if finalEv.Amount != 3_000 {
		t.Errorf("finalized amount: want 3000 got %d", finalEv.Amount)
	}
	if finalEv.AccountId != aliceId {
		t.Errorf("finalized accountId: want %s got %s", aliceId, finalEv.AccountId)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Pending withdrawals persist through snapshot
// -------------------------------------------------------------------------
func TestWithdrawTimelock_SnapshotPersistence(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithTimelock(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddWithdrawRequest(aliceId, "USDC", 7_000, n.GetAccountSequence(aliceId), "").Build())

	before := n.GetPendingWithdrawals(aliceId)
	if len(before) != 1 {
		t.Fatalf("expected 1 pending withdrawal before snapshot, got %d", len(before))
	}

	path := t.TempDir() + "/timelock_snap.json"
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load: %v", err)
	}

	after := n2.GetPendingWithdrawals(aliceId)
	if len(after) != 1 {
		t.Errorf("expected 1 pending withdrawal after snapshot, got %d", len(after))
	} else if after[0].WithdrawalId != before[0].WithdrawalId {
		t.Errorf("withdrawalId mismatch: want %s got %s", before[0].WithdrawalId, after[0].WithdrawalId)
	}

	os.Remove(path)
}
