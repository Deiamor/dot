package scenario_test

// Stage 3: Risk Management tests.
//
// Verifies:
//  1. Position limit enforcement — order rejected when limit is exceeded
//  2. Insurance fund accumulation — 20% of taker fee goes to fund, 80% to treasury
//  3. Position tracker — net position updated after each trade
//  4. Position limit = 0 (unlimited) — no orders rejected

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// bootstrapNodeWithPolicy creates a LocalNode that uses the supplied RiskPolicy.
func bootstrapNodeWithPolicy(t *testing.T, policy risk.RiskPolicy) (*node.LocalNode, string, string, string, string) {
	t.Helper()
	n := node.NewLocalNodeWithPolicy(policy)
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	var aliceId, bobId string
	n.Subscribe(state.EventAccountCreated, func(e state.Event) {
		p := e.Payload.(state.AccountCreatedPayload)
		if p.OwnerAddress == "alice" {
			aliceId = p.AccountId
		} else if p.OwnerAddress == "bob" {
			bobId = p.AccountId
		}
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddCreateAccount("alice", "alice-root", "alice-withdraw").
		AddCreateAccount("bob", "bob-root", "bob-withdraw").
		Build())
	if err != nil {
		t.Fatalf("bootstrap accounts: %v", err)
	}

	var aliceSessionId, bobSessionId string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == aliceId {
			aliceSessionId = p.SessionId
		} else if p.AccountId == bobId {
			bobSessionId = p.SessionId
		}
	})

	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 1_000_000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 1_000_000}).
		Build())
	if err != nil {
		t.Fatalf("bootstrap sessions: %v", err)
	}

	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddDeposit(aliceId, "USDC", 1_000_000).
		AddDeposit(bobId, "BTC", 1_000).
		Build())
	if err != nil {
		t.Fatalf("bootstrap deposits: %v", err)
	}

	return n, aliceId, bobId, aliceSessionId, bobSessionId
}

// -------------------------------------------------------------------------
// Scenario 1: Insurance fund accumulates 20% of taker fee on each trade
// -------------------------------------------------------------------------
func TestRisk_InsuranceFundAccumulation(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// price=10_000, qty=1 → notional=10_000
	// takerFee = 10_000 * 5 / 10_000 = 5
	// insuranceShare = 5 * 2000 / 10_000 = 1
	// treasuryFromTaker = 5 - 1 = 4
	// makerFee = 10_000 * 2 / 10_000 = 2 (all to treasury)
	// total treasury = 4 + 2 = 6
	// total insurance = 1
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	if err != nil {
		t.Fatalf("block 4 sell: %v", err)
	}

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("block 5 buy: %v", err)
	}

	insurance := n.GetInsuranceFundBalance("USDC")
	if insurance != 1 {
		t.Errorf("insurance fund: want 1 got %d", insurance)
	}

	treasury := n.GetTreasury("USDC")
	if treasury.Available != 6 {
		t.Errorf("treasury: want 6 got %d", treasury.Available)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Position tracker — buyer gains +qty, seller loses -qty
// -------------------------------------------------------------------------
func TestRisk_PositionTracker_AfterTrade(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 3, clob.TimeInForceGtc, 4)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	if err != nil {
		t.Fatalf("block 4: %v", err)
	}

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 3, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("block 5: %v", err)
	}

	alicePos := n.GetPosition(aliceId, "BTC-USDC")
	if alicePos.NetQuantity != 3 {
		t.Errorf("alice net position: want +3 got %d", alicePos.NetQuantity)
	}

	bobPos := n.GetPosition(bobId, "BTC-USDC")
	if bobPos.NetQuantity != -3 {
		t.Errorf("bob net position: want -3 got %d", bobPos.NetQuantity)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Position limit enforced — second order rejected
// -------------------------------------------------------------------------
func TestRisk_PositionLimit_OrderRejected(t *testing.T) {
	policy := risk.RiskPolicy{
		MaxOrderQuantity:         1_000_000,
		MinOrderQuantity:         1,
		MaxDailyVolumePerSession: 100_000_000,
		MaxPositionSize:          2, // allow at most net ±2
	}
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNodeWithPolicy(t, policy)

	// Bob sells 2 lots (just at position limit).
	sell2 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 2, clob.TimeInForceGtc, 4)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell2).Build())
	if err != nil {
		t.Fatalf("sell 2: %v", err)
	}

	// Alice buys 2 lots → trades, net position = +2 (at limit, should succeed).
	buy2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 2, clob.TimeInForceGtc, 5)
	buy2.AccountSequence = n.GetAccountSequence(aliceId)
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy2).Build())
	if err != nil {
		t.Fatalf("buy 2: %v", err)
	}
	if res.TradeCount != 1 {
		t.Errorf("expected 1 trade, got %d", res.TradeCount)
	}

	alicePos := n.GetPosition(aliceId, "BTC-USDC")
	if alicePos.NetQuantity != 2 {
		t.Errorf("alice position after first buy: want 2 got %d", alicePos.NetQuantity)
	}

	// Bob deposits more BTC and sells 1 more lot for alice to buy.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddDeposit(bobId, "BTC", 10).Build())
	sell1 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	sell1.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell1).Build())

	// Alice tries to buy 1 more lot → projected net = 3 > MaxPositionSize=2 → should be rejected.
	var rejectedOrderId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		rejectedOrderId = p.OrderId
	})

	buy1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	buy1.AccountSequence = n.GetAccountSequence(aliceId)
	res2, err := n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buy1).Build())
	if err != nil {
		t.Fatalf("buy 1 (over limit): unexpected block error: %v", err)
	}
	if res2.TradeCount != 0 {
		t.Errorf("expected 0 trades (order should be rejected), got %d", res2.TradeCount)
	}
	if rejectedOrderId == "" {
		t.Error("expected ORDER_REJECTED event for position-limit breach, got none")
	}

	// Alice's position should still be 2 (unchanged).
	alicePos = n.GetPosition(aliceId, "BTC-USDC")
	if alicePos.NetQuantity != 2 {
		t.Errorf("alice position after rejected order: want 2 got %d", alicePos.NetQuantity)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: MaxPositionSize=0 (unlimited) — no position-related rejections
// -------------------------------------------------------------------------
func TestRisk_PositionLimit_Zero_Unlimited(t *testing.T) {
	// DefaultRiskPolicy has MaxPositionSize=0 → unlimited
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// Bob sells 100 lots (way above any reasonable limit if one were set).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddDeposit(bobId, "BTC", 1000).Build())
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 100, clob.TimeInForceGtc, 5)
	sell.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())

	// Alice buys 100 lots.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddDeposit(aliceId, "USDC", 10_000_000).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 100, clob.TimeInForceGtc, 7)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("unlimited buy: %v", err)
	}
	if res.TradeCount != 1 {
		t.Errorf("expected 1 trade, got %d", res.TradeCount)
	}

	pos := n.GetPosition(aliceId, "BTC-USDC")
	if pos.NetQuantity != 100 {
		t.Errorf("alice position: want 100 got %d", pos.NetQuantity)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Insurance fund multi-trade accumulation
// -------------------------------------------------------------------------
func TestRisk_InsuranceFund_MultiTrade(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// Execute 3 trades, each with notional=10_000 → takerFee=5, insurance=1 per trade.
	for i := 0; i < 3; i++ {
		h := int64(4 + i*2)
		sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, h)
		sell.AccountSequence = n.GetAccountSequence(bobId)
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(h).AddSubmitOrder(sell).Build())

		buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, h+1)
		buy.AccountSequence = n.GetAccountSequence(aliceId)
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(h + 1).AddSubmitOrder(buy).Build())
	}

	insurance := n.GetInsuranceFundBalance("USDC")
	if insurance != 3 {
		t.Errorf("insurance fund after 3 trades: want 3 got %d", insurance)
	}
}
