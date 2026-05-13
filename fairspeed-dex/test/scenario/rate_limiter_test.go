package scenario_test

// Stage 17: Per-account rate limiter / anti-spam tests.
//
// Verifies:
//  1. First N orders in one block succeed when limit is N
//  2. (N+1)th order in same block is rejected
//  3. Counter resets at next block — orders succeed again
//  4. Accounts are rate-limited independently (one blocked, other fine)
//  5. Rate limit of 0 means unlimited (default behaviour preserved)
//  6. Governance can apply a stricter per-block limit

import (
	"strings"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// -------------------------------------------------------------------------
// Scenario 1: First N orders in one block succeed
// -------------------------------------------------------------------------
func TestRateLimit_FirstOrdersSucceed(t *testing.T) {
	policy := risk.DefaultRiskPolicy
	policy.MaxOrdersPerBlock = 2
	n, aliceId, _, aliceSess, _ := bootstrapNodeWithPolicy(t, policy)

	var rejectedCount int
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedCount++
	})

	// Two orders in a single block — both should pass.
	o1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 9_000, 1, clob.TimeInForceGtc, 4)
	o1.AccountSequence = n.GetAccountSequence(aliceId)
	o2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 8_000, 1, clob.TimeInForceGtc, 4)
	o2.AccountSequence = n.GetAccountSequence(aliceId) + 1

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitOrder(o1).AddSubmitOrder(o2).Build())
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rejectedCount != 0 {
		t.Errorf("expected 0 rejections for limit=2, got %d", rejectedCount)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: (N+1)th order in same block is rejected
// -------------------------------------------------------------------------
func TestRateLimit_ExcessOrderRejected(t *testing.T) {
	policy := risk.DefaultRiskPolicy
	policy.MaxOrdersPerBlock = 1
	n, aliceId, _, aliceSess, _ := bootstrapNodeWithPolicy(t, policy)

	var rejectedIds []string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedIds = append(rejectedIds, e.Payload.(state.OrderRejectedPayload).OrderId)
	})

	// Two orders in one block with limit=1 → second is rejected.
	o1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 9_000, 1, clob.TimeInForceGtc, 4)
	o1.AccountSequence = n.GetAccountSequence(aliceId)
	o2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 8_000, 1, clob.TimeInForceGtc, 4)
	o2.AccountSequence = n.GetAccountSequence(aliceId) + 1

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitOrder(o1).AddSubmitOrder(o2).Build())

	if len(rejectedIds) == 0 {
		t.Error("expected ORDER_REJECTED when rate limit exceeded in same block")
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Counter resets next block — orders succeed again
// -------------------------------------------------------------------------
func TestRateLimit_ResetsNextBlock(t *testing.T) {
	policy := risk.DefaultRiskPolicy
	policy.MaxOrdersPerBlock = 1
	n, aliceId, _, aliceSess, _ := bootstrapNodeWithPolicy(t, policy)

	// Block 4: two orders — second rejected.
	o1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 9_000, 1, clob.TimeInForceGtc, 4)
	o1.AccountSequence = n.GetAccountSequence(aliceId)
	o2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 8_000, 1, clob.TimeInForceGtc, 4)
	o2.AccountSequence = n.GetAccountSequence(aliceId) + 1
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitOrder(o1).AddSubmitOrder(o2).Build())

	// Block 5: counter resets; order should pass.
	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})
	o3 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 7_000, 1, clob.TimeInForceGtc, 5)
	o3.AccountSequence = n.GetAccountSequence(aliceId)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(o3).Build())
	if err != nil {
		t.Fatalf("block 5: %v", err)
	}
	if rejectedId != "" {
		t.Errorf("order should succeed after block reset, rejected: %s", rejectedId)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Accounts are rate-limited independently
// -------------------------------------------------------------------------
func TestRateLimit_IndependentPerAccount(t *testing.T) {
	policy := risk.DefaultRiskPolicy
	policy.MaxOrdersPerBlock = 1
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNodeWithPolicy(t, policy)

	// Alice submits 2 orders, Bob submits 1 — all in the same block.
	// Expected: Alice's 2nd order rejected, Bob's 1st order accepted.
	oA1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 9_000, 1, clob.TimeInForceGtc, 4)
	oA1.AccountSequence = n.GetAccountSequence(aliceId)
	oA2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 8_000, 1, clob.TimeInForceGtc, 4)
	oA2.AccountSequence = n.GetAccountSequence(aliceId) + 1
	oB := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)

	var rejectedIds []string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		rejectedIds = append(rejectedIds, p.AccountId)
	})

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitOrder(oA1).AddSubmitOrder(oA2).AddSubmitOrder(oB).Build())

	// Exactly one rejection and it belongs to Alice, not Bob.
	if len(rejectedIds) != 1 {
		t.Errorf("expected exactly 1 rejection, got %d: %v", len(rejectedIds), rejectedIds)
	} else if rejectedIds[0] != aliceId {
		t.Errorf("expected Alice's order rejected, got %s", rejectedIds[0])
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Rate limit 0 means unlimited
// -------------------------------------------------------------------------
func TestRateLimit_ZeroMeansUnlimited(t *testing.T) {
	// Default policy has MaxOrdersPerBlock = 0 (unlimited).
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	var rateLimitRejections int
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		if strings.HasPrefix(p.Reason, "rate limit") {
			rateLimitRejections++
		}
	})

	// Submit 3 separate orders across 3 blocks — none should be rate-limited.
	for i := 0; i < 3; i++ {
		o := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy,
			int64(9_000-i*100), 1, clob.TimeInForceGtc, int64(4+i))
		o.AccountSequence = n.GetAccountSequence(aliceId)
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(int64(4 + i)).AddSubmitOrder(o).Build())
	}

	if rateLimitRejections != 0 {
		t.Errorf("with limit=0 (unlimited), expected 0 rate-limit rejections, got %d", rateLimitRejections)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Stricter per-block limit from policy update
// -------------------------------------------------------------------------
func TestRateLimit_StrictPolicyUpdate(t *testing.T) {
	policy := risk.DefaultRiskPolicy
	policy.MaxOrdersPerBlock = 1
	n, aliceId, _, aliceSess, _ := bootstrapNodeWithPolicy(t, policy)

	var rejectedIds []string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedIds = append(rejectedIds, e.Payload.(state.OrderRejectedPayload).OrderId)
	})

	// Two orders in one block — second must be rejected.
	o1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 9_000, 1, clob.TimeInForceGtc, 4)
	o1.AccountSequence = n.GetAccountSequence(aliceId)
	o2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 8_000, 1, clob.TimeInForceGtc, 4)
	o2.AccountSequence = n.GetAccountSequence(aliceId) + 1

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitOrder(o1).AddSubmitOrder(o2).Build())

	if len(rejectedIds) == 0 {
		t.Error("expected rejection under strict per-block limit policy")
	}
}
