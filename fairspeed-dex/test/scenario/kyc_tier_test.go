package scenario_test

// Stage 14: KYC tier and jurisdiction-based trade limit tests.
//
// Verifies:
//  1. Tier 1 account cannot exceed MaxSingleOrderNotional (10,000)
//  2. Tier 2 account can trade up to 100,000 notional
//  3. Tier 3 (institutional) has no single-order limit
//  4. Jurisdiction-specific limits apply (US same as DEFAULT for Tier 1)
//  5. Regulatory reporting covers maker+taker accountIds and fees
//  6. Tier and jurisdiction are set via TxKYCApproveWithTier and persist

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// -------------------------------------------------------------------------
// Scenario 1: Tier 1 order rejected when notional exceeds 10,000
// -------------------------------------------------------------------------
func TestKYCTier_Tier1_OrderRejectedOverLimit(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	// Set alice to Tier 1, EU jurisdiction.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddKYCApproveWithTier(aliceId, "APPROVED", 1, "EU").Build())
	if err != nil {
		t.Fatalf("kyc tier: %v", err)
	}

	// Order with notional = 10_000 * 2 = 20,000 — exceeds Tier 1 limit of 10,000.
	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	bigOrder := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 2, clob.TimeInForceGtc, 5)
	bigOrder.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(bigOrder).Build())

	if rejectedId == "" {
		t.Error("expected ORDER_REJECTED for Tier 1 account exceeding notional limit")
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Tier 1 order accepted when notional ≤ 10,000
// -------------------------------------------------------------------------
func TestKYCTier_Tier1_OrderAcceptedWithinLimit(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddKYCApproveWithTier(aliceId, "APPROVED", 1, "EU").Build())

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	// notional = 10_000 * 1 = 10,000 — exactly at the limit, should pass.
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	order.AccountSequence = n.GetAccountSequence(aliceId)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(order).Build())
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rejectedId != "" {
		t.Errorf("order within Tier 1 limit should not be rejected: %s", rejectedId)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Tier 2 account can trade up to 100,000 notional
// -------------------------------------------------------------------------
func TestKYCTier_Tier2_HigherLimit(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddKYCApproveWithTier(aliceId, "APPROVED", 2, "US").Build())

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	// notional = 50_000 * 1 = 50,000 — within Tier 2 limit (100,000).
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 50_000, 1, clob.TimeInForceGtc, 5)
	order.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(order).Build())

	if rejectedId != "" {
		t.Errorf("Tier 2 order within 100k limit should not be rejected: %s", rejectedId)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Tier 3 (institutional) has no single-order limit
// -------------------------------------------------------------------------
func TestKYCTier_Tier3_NoLimit(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	// Fund Alice with enough USDC to place a large institutional order.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddDeposit(aliceId, "USDC", 500_000).
		AddKYCApproveWithTier(aliceId, "APPROVED", 3, "US").Build())

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	// notional = 200_000 * 1 — far exceeds Tier 1/2 limits; Tier 3 = unlimited.
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 200_000, 1, clob.TimeInForceGtc, 5)
	order.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(order).Build())

	if rejectedId != "" {
		t.Errorf("Tier 3 order should never be rejected for notional: %s", rejectedId)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Regulatory reporting — TradeRecord has correct fields
// -------------------------------------------------------------------------
func TestKYCTier_RegulatoryReporting(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// Bob sells, Alice buys — full fill at price 10,000, qty 1.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("trade: %v", err)
	}
	if res.TradeCount != 1 {
		t.Fatalf("expected 1 trade, got %d", res.TradeCount)
	}

	trades := n.AllTrades()
	records := compliance.ReportTrades([]settlement.TradeExecution(toSettlementSlice(trades)))
	if len(records) == 0 {
		t.Fatal("expected at least 1 trade record")
	}
	r := records[0]
	if r.Notional != 10_000 {
		t.Errorf("notional: want 10000 got %d", r.Notional)
	}
	if r.MakerAccountId == "" || r.TakerAccountId == "" {
		t.Error("maker/taker accountId should be populated in report")
	}
	if r.TotalFee != r.MakerFee+r.TakerFee {
		t.Error("total fee mismatch in regulatory report")
	}
}

func toSettlementSlice(trades []settlement.TradeExecution) []settlement.TradeExecution {
	return trades
}

// -------------------------------------------------------------------------
// Scenario 6: Tier and jurisdiction persist through KYCApproveWithTier
// -------------------------------------------------------------------------
func TestKYCTier_TierAndJurisdictionPersist(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddKYCApproveWithTier(aliceId, "APPROVED", 2, "APAC").Build())
	if err != nil {
		t.Fatalf("kyc approve: %v", err)
	}

	acc, err := n.GetAccount(aliceId)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if acc.KYCTier != account.KYCTier2 {
		t.Errorf("tier: want 2 got %d", acc.KYCTier)
	}
	if acc.Jurisdiction != account.JurisdictionAPAC {
		t.Errorf("jurisdiction: want APAC got %s", acc.Jurisdiction)
	}
}
