package scenario_test

// Phase 2: FairBatch determinism tests.
//
// Verifies that:
// 1. The same set of order transactions, regardless of submission order,
//    always produces the same BatchHash.
// 2. Two LocalNodes processing identical BatchHashes produce identical trades.
// 3. Different order sets produce different BatchHashes.

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// bootstrapNode creates a fully-initialized LocalNode with Alice and Bob accounts
// and returns (node, aliceId, bobId, aliceSessionId, bobSessionId).
func bootstrapNode(t *testing.T) (*node.LocalNode, string, string, string, string) {
	t.Helper()
	n := node.NewLocalNode()
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
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 1000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 1000}).
		Build())
	if err != nil {
		t.Fatalf("bootstrap sessions: %v", err)
	}

	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddDeposit(aliceId, "USDC", 100_000).
		AddDeposit(bobId, "BTC", 100).
		Build())
	if err != nil {
		t.Fatalf("bootstrap deposits: %v", err)
	}

	return n, aliceId, bobId, aliceSessionId, bobSessionId
}

func TestFairBatchDeterminism(t *testing.T) {
	// -------------------------------------------------------------------------
	// Test 1: Same transactions in different order → same BatchHash
	// -------------------------------------------------------------------------
	t.Run("SameOrdersDifferentSubmissionOrder_SameBatchHash", func(t *testing.T) {
		n1, aliceId, bobId, aliceSession, bobSession := bootstrapNode(t)
		_ = n1 // only need IDs

		orderA := clob.NewLimitOrder(aliceId, aliceSession, "BTC-USDC",
			clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)
		orderB := clob.NewLimitOrder(bobId, bobSession, "BTC-USDC",
			clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)

		// Build batch A→B
		batchAB := fairbatch.NewBatchBuilder(4).
			AddSubmitOrder(orderA).
			AddSubmitOrder(orderB).
			Build()

		// Build batch B→A (reversed)
		batchBA := fairbatch.NewBatchBuilder(4).
			AddSubmitOrder(orderB).
			AddSubmitOrder(orderA).
			Build()

		if batchAB.BatchHash != batchBA.BatchHash {
			t.Errorf("BatchHash mismatch:\n  A→B: %s\n  B→A: %s\nSame orders must produce same hash regardless of submission order",
				batchAB.BatchHash, batchBA.BatchHash)
		}
	})

	// -------------------------------------------------------------------------
	// Test 2: Same BatchHash → same execution sequence → same trades
	// -------------------------------------------------------------------------
	t.Run("SameBatchHash_SameTradeResults", func(t *testing.T) {
		n1, aliceId1, bobId1, aliceSess1, bobSess1 := bootstrapNode(t)
		n2, aliceId2, bobId2, aliceSess2, bobSess2 := bootstrapNode(t)

		// Create semantically identical orders on both nodes.
		// Use the same ClientOrderId and AccountSequence to get the same hash.
		makeOrders := func(aliceId, bobId, aliceSess, bobSess string) (clob.Order, clob.Order) {
			sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC",
				clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
			sell.ClientOrderId = "bob-sell-1"
			sell.AccountSequence = 1

			buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC",
				clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)
			buy.ClientOrderId = "alice-buy-1"
			buy.AccountSequence = 1
			return sell, buy
		}

		sell1, buy1 := makeOrders(aliceId1, bobId1, aliceSess1, bobSess1)
		sell2, buy2 := makeOrders(aliceId2, bobId2, aliceSess2, bobSess2)

		// Both nodes process a SELL order in block 4 first.
		batch4_n1 := fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell1).Build()
		batch4_n2 := fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell2).Build()

		if _, err := n1.SubmitBatch(batch4_n1); err != nil {
			t.Fatalf("n1 block 4: %v", err)
		}
		if _, err := n2.SubmitBatch(batch4_n2); err != nil {
			t.Fatalf("n2 block 4: %v", err)
		}

		// Both nodes process BUY in block 5 → trade should occur on both.
		batch5_n1 := fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy1).Build()
		batch5_n2 := fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy2).Build()

		result1, err := n1.SubmitBatch(batch5_n1)
		if err != nil {
			t.Fatalf("n1 block 5: %v", err)
		}
		result2, err := n2.SubmitBatch(batch5_n2)
		if err != nil {
			t.Fatalf("n2 block 5: %v", err)
		}

		if result1.TradeCount != result2.TradeCount {
			t.Errorf("trade count mismatch: n1=%d n2=%d", result1.TradeCount, result2.TradeCount)
		}
		if result1.TradeCount != 1 {
			t.Errorf("expected 1 trade on each node, got %d", result1.TradeCount)
		}
		if result1.Trades[0].Price != result2.Trades[0].Price {
			t.Errorf("trade price mismatch: %d vs %d", result1.Trades[0].Price, result2.Trades[0].Price)
		}
		if result1.Trades[0].Quantity != result2.Trades[0].Quantity {
			t.Errorf("trade qty mismatch: %d vs %d", result1.Trades[0].Quantity, result2.Trades[0].Quantity)
		}
	})

	// -------------------------------------------------------------------------
	// Test 3: Different order sets → different BatchHashes
	// -------------------------------------------------------------------------
	t.Run("DifferentOrders_DifferentBatchHash", func(t *testing.T) {
		n1, aliceId, _, aliceSession, _ := bootstrapNode(t)
		_ = n1

		orderX := clob.NewLimitOrder(aliceId, aliceSession, "BTC-USDC",
			clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)
		orderY := clob.NewLimitOrder(aliceId, aliceSession, "BTC-USDC",
			clob.OrderSideBuy, 11_000, 2, clob.TimeInForceGtc, 4)

		batchX := fairbatch.NewBatchBuilder(4).AddSubmitOrder(orderX).Build()
		batchY := fairbatch.NewBatchBuilder(4).AddSubmitOrder(orderY).Build()

		if batchX.BatchHash == batchY.BatchHash {
			t.Errorf("different orders produced the same BatchHash: %s", batchX.BatchHash)
		}
	})

	// -------------------------------------------------------------------------
	// Test 4: TxHash is stable (same order → same TxHash across builds)
	// -------------------------------------------------------------------------
	t.Run("TxHash_Stable_AcrossBuilds", func(t *testing.T) {
		_, aliceId, _, aliceSession, _ := bootstrapNode(t)

		order := clob.NewLimitOrder(aliceId, aliceSession, "BTC-USDC",
			clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)
		order.ClientOrderId = "stable-test"
		order.AccountSequence = 42

		batch1 := fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build()
		batch2 := fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build()

		if batch1.BatchHash != batch2.BatchHash {
			t.Errorf("BatchHash not stable: %s vs %s", batch1.BatchHash, batch2.BatchHash)
		}
		tx1 := batch1.Transactions[0]
		tx2 := batch2.Transactions[0]
		if tx1.TxHash != tx2.TxHash {
			t.Errorf("TxHash not stable: %s vs %s", tx1.TxHash, tx2.TxHash)
		}
	})
}
