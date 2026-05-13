package scenario_test

// Edge-case integration tests.
//
// These tests verify correct behaviour at arithmetic and business-logic
// boundaries that could cause over-reservation, under-reservation, wrong
// P&L, or silent failures.
//
// EC01 – Minimum PERP margin: tiny notional/leverage rounds to 0 → clamped to 1
// EC02 – SPOT exact-balance boundary: exactly sufficient vs 1-unit short
// EC03 – PERP exact-margin boundary: exactly sufficient vs 1-unit short
// EC04 – Liquidation boundary: equity == maintenance (safe) vs equity == maintenance-1 (liquidate)
// EC05 – AvgEntryPrice weighted average precision with asymmetric quantities
// EC06 – Position reversal: LONG → FLAT → SHORT; AvgEntryPrice reset correctly
// EC07 – Funding rate clamp at exact boundary (100 bps max, 101 bps premium)
// EC08 – Conditional order triggers at exact trigger price (boundary GTE / LTE)
// EC09 – PERP partial fill: collateral released proportional to unfilled quantity
// EC10 – Funding debit when Available is zero: margin absorbs
// EC11 – Over-reservation prevention: multiple PERP orders exhaust margin correctly
// EC12 – ReduceOnly rejected when it would increase position (wrong direction)

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/funding"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

// bootstrapPerpNode creates a node with one PERP market (BTC-USDC-PERP).
// Alice and Bob each have a PERP-enabled session.
// Returns (n, aliceId, bobId, aliceSess, bobSess, nextBlock).
func bootstrapPerpNode(
	t *testing.T,
	initialMarginBps, maintenanceMarginBps, maxLeverage, fundingInterval int64,
	aliceUSDC, bobUSDC int64,
) (*node.LocalNode, string, string, string, string, int64) {
	t.Helper()
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      initialMarginBps,
			MaintenanceMarginBps:  maintenanceMarginBps,
			MaxLeverage:           maxLeverage,
			FundingIntervalBlocks: fundingInterval,
			MaxFundingRateBps:     100,
		}).Build())

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", aliceUSDC).
		AddDeposit(bobId, "USDC", bobUSDC).Build())

	var aliceSess, bobSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == aliceId && aliceSess == "" {
			aliceSess = p.SessionId
		} else if p.AccountId == bobId && bobSess == "" {
			bobSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(aliceId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC-PERP"},
			MaxOrderAmount: 100_000_000,
		}).
		AddCreateSession(bobId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC-PERP"},
			MaxOrderAmount: 100_000_000,
		}).Build())

	return n, aliceId, bobId, aliceSess, bobSess, 7
}

// matchPerp places a sell then a buy at the same price to create a matched trade.
// Returns the next available block height.
func matchPerp(
	n *node.LocalNode,
	sellerId, sellerSess, buyerId, buyerSess, marketId string,
	price, qty, sellBlock, buyBlock int64,
) int64 {
	sell := clob.NewLimitOrder(sellerId, sellerSess, marketId, clob.OrderSideSell, price, qty, clob.TimeInForceGtc, sellBlock)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(sellBlock).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(buyerId, buyerSess, marketId, clob.OrderSideBuy, price, qty, clob.TimeInForceGtc, buyBlock)
	buy.AccountSequence = n.GetAccountSequence(buyerId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(buyBlock).AddSubmitOrder(buy).Build())
	return buyBlock + 1
}

// ────────────────────────────────────────────────────────────────────────────
// EC01 – Minimum PERP margin: notional / leverage rounds to 0 → clamped to 1
// ────────────────────────────────────────────────────────────────────────────
// price=1, qty=1, InitialMarginBps=1000 (10%) → notional=1, leverage=10,
// margin = 1/10 = 0 (integer division) → must be clamped to 1, not 0.
// If the engine stored 0 as reserved and later tried to release 0, the
// position's AllocatedMargin would be stuck at 0 forever.
func TestEC01_MinimumPerpMarginClampedToOne(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
		t, 1000, 500, 10, 100_000, 50_000, 50_000,
	)

	// price=1, qty=1, notional=1, margin target = 1/10 = 0 → clamped to 1.
	nextH = matchPerp(n, bobId, bobSess, aliceId, aliceSess, "BTC-USDC-PERP", 1, 1, nextH, nextH+1)

	reserved := n.GetBalance(aliceId, "USDC").Reserved
	if reserved < 1 {
		t.Errorf("EC01: expected at least 1 unit reserved (min clamp), got %d", reserved)
	}

	pos := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if pos.NetQuantity != 1 {
		t.Errorf("EC01: expected NetQuantity=1 after match, got %d", pos.NetQuantity)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// EC02 – SPOT exact-balance boundary
// ────────────────────────────────────────────────────────────────────────────
// Test 1: Alice has exactly 10_000 USDC → SPOT BUY at price=10_000, qty=1 should succeed.
// Test 2: Alice has exactly 9_999 USDC → same order should be rejected.
func TestEC02_SpotExactBalanceBoundary(t *testing.T) {
	t.Run("ExactlyEnough", func(t *testing.T) {
		n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)
		// Alice starts with 100_000 USDC (from bootstrapNode).
		// Drain her to exactly 10_000 USDC available by having her make a
		// resting BUY that reserves 90_000 first, then cancel it.
		// Simpler: just give her exactly 10_000 via a fresh node.

		// Create a fresh node manually so we control Alice's balance.
		n2 := node.NewLocalNode()
		{
			var aId, bId string
			n2.Subscribe(state.EventAccountCreated, func(e state.Event) {
				p := e.Payload.(state.AccountCreatedPayload)
				if p.OwnerAddress == "alice" {
					aId = p.AccountId
				} else if p.OwnerAddress == "bob" {
					bId = p.AccountId
				}
			})
			_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(1).
				AddCreateAccount("alice", "a-root", "a-withdraw").
				AddCreateAccount("bob", "b-root", "b-withdraw").Build())

			var aSess, bSess string
			n2.Subscribe(state.EventSessionCreated, func(e state.Event) {
				p := e.Payload.(state.SessionCreatedPayload)
				if p.AccountId == aId {
					aSess = p.SessionId
				} else if p.AccountId == bId {
					bSess = p.SessionId
				}
			})
			_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(2).
				AddCreateSession(aId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 100_000}).
				AddCreateSession(bId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 100_000}).Build())

			// Give Alice exactly 10_000 USDC, Bob some BTC.
			_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(3).
				AddDeposit(aId, "USDC", 10_000).
				AddDeposit(bId, "BTC", 10).Build())

			// Bob posts a sell at 10_000. Alice buys 1 — should succeed.
			sell := clob.NewLimitOrder(bId, bSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
			_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
			buy := clob.NewLimitOrder(aId, aSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
			buy.AccountSequence = n2.GetAccountSequence(aId)
			result, _ := n2.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())

			if len(result.Trades) != 1 {
				t.Errorf("EC02 ExactlyEnough: expected 1 trade, got %d", len(result.Trades))
			}
			btcBal := n2.GetBalance(aId, "BTC").Available
			if btcBal != 1 {
				t.Errorf("EC02 ExactlyEnough: expected Alice to receive 1 BTC, got %d", btcBal)
			}
		}
		_ = n
		_ = aliceId
		_ = bobId
		_ = aliceSess
		_ = bobSess
	})

	t.Run("OneLess", func(t *testing.T) {
		n2 := node.NewLocalNode()
		var aId, bId string
		n2.Subscribe(state.EventAccountCreated, func(e state.Event) {
			p := e.Payload.(state.AccountCreatedPayload)
			if p.OwnerAddress == "alice" {
				aId = p.AccountId
			} else if p.OwnerAddress == "bob" {
				bId = p.AccountId
			}
		})
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(1).
			AddCreateAccount("alice", "a-root", "a-withdraw").
			AddCreateAccount("bob", "b-root", "b-withdraw").Build())

		var aSess, bSess string
		n2.Subscribe(state.EventSessionCreated, func(e state.Event) {
			p := e.Payload.(state.SessionCreatedPayload)
			if p.AccountId == aId {
				aSess = p.SessionId
			} else if p.AccountId == bId {
				bSess = p.SessionId
			}
		})
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(2).
			AddCreateSession(aId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 100_000}).
			AddCreateSession(bId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 100_000}).Build())

		// Alice has 9_999 USDC — one unit short of the 10_000 required.
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(3).
			AddDeposit(aId, "USDC", 9_999).
			AddDeposit(bId, "BTC", 10).Build())

		sell := clob.NewLimitOrder(bId, bSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
		buy := clob.NewLimitOrder(aId, aSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
		buy.AccountSequence = n2.GetAccountSequence(aId)
		result, _ := n2.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())

		if len(result.Trades) > 0 {
			t.Errorf("EC02 OneLess: expected no trades (insufficient balance), got %d", len(result.Trades))
		}
		// Balance must not change.
		bal := n2.GetBalance(aId, "USDC")
		if bal.Reserved > 0 {
			t.Errorf("EC02 OneLess: rejected order must not reserve funds, got reserved=%d", bal.Reserved)
		}
	})
}

// ────────────────────────────────────────────────────────────────────────────
// EC03 – PERP exact-margin boundary
// ────────────────────────────────────────────────────────────────────────────
// InitialMarginBps=1000 (10%). Order: price=10_000, qty=1 → margin required = 1_000.
// Test A: Alice has exactly 1_000 USDC → order accepted.
// Test B: Alice has 999 USDC → order rejected.
func TestEC03_PerpExactMarginBoundary(t *testing.T) {
	// margin needed = price * qty * InitialMarginBps / 10_000
	//               = 10_000 * 1 * 1000 / 10_000 = 1_000

	t.Run("ExactlyEnough", func(t *testing.T) {
		n2 := node.NewLocalNode()
		var aId, bId string
		n2.Subscribe(state.EventAccountCreated, func(e state.Event) {
			p := e.Payload.(state.AccountCreatedPayload)
			if p.OwnerAddress == "alice" {
				aId = p.AccountId
			} else if p.OwnerAddress == "bob" {
				bId = p.AccountId
			}
		})
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(1).
			AddCreateAccount("alice", "a-root", "a-withdraw").
			AddCreateAccount("bob", "b-root", "b-withdraw").Build())

		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(4).
			AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
				MarketId:              "BTC-USDC-PERP",
				BaseAsset:             "BTC",
				QuoteAsset:            "USDC",
				InitialMarginBps:      1000,
				MaintenanceMarginBps:  500,
				MaxLeverage:           10,
				FundingIntervalBlocks: 100_000,
				MaxFundingRateBps:     100,
			}).Build())

		var aSess, bSess string
		n2.Subscribe(state.EventSessionCreated, func(e state.Event) {
			p := e.Payload.(state.SessionCreatedPayload)
			if p.AccountId == aId {
				aSess = p.SessionId
			} else if p.AccountId == bId {
				bSess = p.SessionId
			}
		})
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(2).
			AddCreateSession(aId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC-PERP"}, MaxOrderAmount: 1_000_000}).
			AddCreateSession(bId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC-PERP"}, MaxOrderAmount: 1_000_000}).Build())

		// Alice has exactly 1_000 USDC.
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(3).
			AddDeposit(aId, "USDC", 1_000).
			AddDeposit(bId, "USDC", 100_000).Build())

		// Bob posts a resting sell first.
		sell := clob.NewLimitOrder(bId, bSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())

		// Alice buys — should succeed (margin exactly = balance).
		buy := clob.NewLimitOrder(aId, aSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
		buy.AccountSequence = n2.GetAccountSequence(aId)
		result, _ := n2.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

		if len(result.Trades) == 0 {
			t.Error("EC03 ExactlyEnough: expected trade to succeed when balance == margin required")
		}
	})

	t.Run("OneLess", func(t *testing.T) {
		n2 := node.NewLocalNode()
		var aId, bId string
		n2.Subscribe(state.EventAccountCreated, func(e state.Event) {
			p := e.Payload.(state.AccountCreatedPayload)
			if p.OwnerAddress == "alice" {
				aId = p.AccountId
			} else if p.OwnerAddress == "bob" {
				bId = p.AccountId
			}
		})
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(1).
			AddCreateAccount("alice", "a-root", "a-withdraw").
			AddCreateAccount("bob", "b-root", "b-withdraw").Build())

		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(4).
			AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
				MarketId:              "BTC-USDC-PERP",
				BaseAsset:             "BTC",
				QuoteAsset:            "USDC",
				InitialMarginBps:      1000,
				MaintenanceMarginBps:  500,
				MaxLeverage:           10,
				FundingIntervalBlocks: 100_000,
				MaxFundingRateBps:     100,
			}).Build())

		var aSess, bSess string
		n2.Subscribe(state.EventSessionCreated, func(e state.Event) {
			p := e.Payload.(state.SessionCreatedPayload)
			if p.AccountId == aId {
				aSess = p.SessionId
			} else if p.AccountId == bId {
				bSess = p.SessionId
			}
		})
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(2).
			AddCreateSession(aId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC-PERP"}, MaxOrderAmount: 1_000_000}).
			AddCreateSession(bId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC-PERP"}, MaxOrderAmount: 1_000_000}).Build())

		// Alice has 999 USDC — one short of the required 1_000.
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(3).
			AddDeposit(aId, "USDC", 999).
			AddDeposit(bId, "USDC", 100_000).Build())

		sell := clob.NewLimitOrder(bId, bSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())

		buy := clob.NewLimitOrder(aId, aSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
		buy.AccountSequence = n2.GetAccountSequence(aId)
		result, _ := n2.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

		if len(result.Trades) > 0 {
			t.Error("EC03 OneLess: order should be rejected when balance is 1 unit short of margin")
		}
		if n2.GetBalance(aId, "USDC").Reserved > 0 {
			t.Errorf("EC03 OneLess: rejected order must not reserve funds")
		}
	})
}

// ────────────────────────────────────────────────────────────────────────────
// EC04 – Liquidation boundary: equity strictly above maintenance (safe) vs at/below
// ────────────────────────────────────────────────────────────────────────────
// entry=10_000, qty=1, allocatedMargin=1_000, maintenanceBps=500.
// maintenanceMargin = (1 * 10_000 / 10_000) * 500 = 500.
// The check in checkAndLiquidate is: "equity > maintenanceMargin" → healthy.
// So equity == maintenance IS liquidated (borderline unsafe).
//
// Test A: markPrice=9_501 → unrealizedPnL=-499 → equity=501 > 500 → NOT liquidated.
// Test B: markPrice=9_500 → unrealizedPnL=-500 → equity=500 == 500 → IS liquidated.
// Test C: markPrice=9_499 → unrealizedPnL=-501 → equity=499 < 500 → IS liquidated.
func TestEC04_LiquidationBoundary(t *testing.T) {
	t.Run("StrictlyAboveMaintenance_NotLiquidated", func(t *testing.T) {
		n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
			t, 1000, 500, 10, 100_000, 200_000, 200_000,
		)
		nextH = matchPerp(n, bobId, bobSess, aliceId, aliceSess, "BTC-USDC-PERP", 10_000, 1, nextH, nextH+1)

		// markPrice = 9_501 → equity = 501 > 500 → NOT liquidated.
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 9_501).Build())
		nextH++
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
		nextH++

		pos := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
		if pos.NetQuantity != 1 {
			t.Errorf("EC04 StrictlyAbove: position should survive when equity > maintenance, got NetQuantity=%d", pos.NetQuantity)
		}
		_ = nextH
	})

	t.Run("ExactlyAtMaintenance_Liquidated", func(t *testing.T) {
		n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
			t, 1000, 500, 10, 100_000, 200_000, 200_000,
		)
		nextH = matchPerp(n, bobId, bobSess, aliceId, aliceSess, "BTC-USDC-PERP", 10_000, 1, nextH, nextH+1)

		var liquidated bool
		n.Subscribe(state.EventLiquidationTriggered, func(e state.Event) {
			p := e.Payload.(state.LiquidationTriggeredPayload)
			if p.AccountId == aliceId {
				liquidated = true
			}
		})

		// markPrice = 9_500 → equity = 500 == maintenanceMargin → IS liquidated (equity NOT > maintenance).
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 9_500).Build())
		nextH++
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())

		if !liquidated {
			t.Error("EC04 ExactlyAt: position should be liquidated when equity == maintenanceMargin (not strictly greater)")
		}
		pos := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
		if pos.NetQuantity != 0 {
			t.Errorf("EC04 ExactlyAt: liquidated position should be zero, got NetQuantity=%d", pos.NetQuantity)
		}
	})

	t.Run("OneBelowMaintenance_Liquidated", func(t *testing.T) {
		n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
			t, 1000, 500, 10, 100_000, 200_000, 200_000,
		)
		nextH = matchPerp(n, bobId, bobSess, aliceId, aliceSess, "BTC-USDC-PERP", 10_000, 1, nextH, nextH+1)

		var liquidated bool
		n.Subscribe(state.EventLiquidationTriggered, func(e state.Event) {
			p := e.Payload.(state.LiquidationTriggeredPayload)
			if p.AccountId == aliceId {
				liquidated = true
			}
		})

		// markPrice = 9_499 → equity = 499 < 500 → IS liquidated.
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 9_499).Build())
		nextH++
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())

		if !liquidated {
			t.Error("EC04 OneBelow: expected liquidation when equity < maintenanceMargin")
		}
		pos := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
		if pos.NetQuantity != 0 {
			t.Errorf("EC04 OneBelow: liquidated position should be zero, got NetQuantity=%d", pos.NetQuantity)
		}
	})
}

// ────────────────────────────────────────────────────────────────────────────
// EC05 – AvgEntryPrice weighted average precision with asymmetric quantities
// ────────────────────────────────────────────────────────────────────────────
// Buy 3 @ 10_000 and 2 @ 11_000.
// Correct avg = (3*10_000 + 2*11_000) / 5 = 52_000 / 5 = 10_400.
func TestEC05_AvgEntryPricePrecision(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
		t, 1000, 500, 10, 100_000, 500_000, 500_000,
	)

	// Fill 1: Bob sells 3 @ 10_000, Alice buys 3.
	sell1 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 3, clob.TimeInForceGtc, nextH)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(sell1).Build())
	nextH++
	buy1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 3, clob.TimeInForceGtc, nextH)
	buy1.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(buy1).Build())
	nextH++

	pos1 := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if pos1.NetQuantity != 3 {
		t.Fatalf("EC05: after fill1 expected NetQty=3, got %d", pos1.NetQuantity)
	}
	if pos1.AvgEntryPrice != 10_000 {
		t.Errorf("EC05: after fill1 AvgEntryPrice want 10_000, got %d", pos1.AvgEntryPrice)
	}

	// Fill 2: Bob sells 2 @ 11_000, Alice buys 2.
	sell2 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideSell, 11_000, 2, clob.TimeInForceGtc, nextH)
	sell2.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(sell2).Build())
	nextH++
	buy2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 11_000, 2, clob.TimeInForceGtc, nextH)
	buy2.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(buy2).Build())
	nextH++

	pos2 := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if pos2.NetQuantity != 5 {
		t.Fatalf("EC05: after fill2 expected NetQty=5, got %d", pos2.NetQuantity)
	}
	// (3*10_000 + 2*11_000) / 5 = 52_000 / 5 = 10_400
	if pos2.AvgEntryPrice != 10_400 {
		t.Errorf("EC05: AvgEntryPrice want 10_400, got %d (expected weighted avg of 10k×3 + 11k×2)", pos2.AvgEntryPrice)
	}
	_ = nextH
}

// ────────────────────────────────────────────────────────────────────────────
// EC06 – Position reversal: LONG 2 → FLAT → SHORT 2
// ────────────────────────────────────────────────────────────────────────────
// After closing the long and opening a short, NetQuantity should be -2
// and AvgEntryPrice should reflect the short entry price, not the old long price.
func TestEC06_PositionReversal(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
		t, 1000, 500, 10, 100_000, 500_000, 500_000,
	)

	// Step 1: Alice opens LONG 2 @ 10_000.
	nextH = matchPerp(n, bobId, bobSess, aliceId, aliceSess, "BTC-USDC-PERP", 10_000, 2, nextH, nextH+1)

	posLong := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if posLong.NetQuantity != 2 {
		t.Fatalf("EC06: expected NetQty=2 after long, got %d", posLong.NetQuantity)
	}

	// Step 2: Alice closes LONG (sells 2 @ 12_000, ReduceOnly).
	// Bob buys 2 @ 12_000.
	buy3 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideBuy, 12_000, 2, clob.TimeInForceGtc, nextH)
	buy3.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(buy3).Build())
	nextH++
	closeOrder := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideSell, 12_000, 2, clob.TimeInForceGtc, nextH)
	closeOrder.ReduceOnly = true
	closeOrder.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(closeOrder).Build())
	nextH++

	posFlat := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if posFlat.NetQuantity != 0 {
		t.Fatalf("EC06: position should be flat after ReduceOnly close, got NetQty=%d", posFlat.NetQuantity)
	}

	// Step 3: Alice opens SHORT 2 @ 14_000.
	// Bob buys 2 @ 14_000 to match.
	buy4 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideBuy, 14_000, 2, clob.TimeInForceGtc, nextH)
	buy4.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(buy4).Build())
	nextH++
	shortOrder := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideSell, 14_000, 2, clob.TimeInForceGtc, nextH)
	shortOrder.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(shortOrder).Build())
	nextH++

	posShort := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if posShort.NetQuantity != -2 {
		t.Errorf("EC06: expected NetQty=-2 after reversal to SHORT, got %d", posShort.NetQuantity)
	}
	if posShort.AvgEntryPrice != 14_000 {
		t.Errorf("EC06: SHORT AvgEntryPrice should be 14_000 (not old long entry), got %d", posShort.AvgEntryPrice)
	}
	_ = nextH
}

// ────────────────────────────────────────────────────────────────────────────
// EC07 – Funding rate clamp at exact boundary
// ────────────────────────────────────────────────────────────────────────────
// maxFundingRateBps = 100.
// When premium = exactly 1% → rate = 100 bps (at clamp boundary, not clamped).
// When premium = 1.01% → rate clamped to 100 bps.
// This tests the CalcFundingRate pure function.
func TestEC07_FundingRateClampBoundary(t *testing.T) {
	// Exactly at boundary: (markPrice - indexPrice) * 10_000 / indexPrice == 100
	// indexPrice = 10_000, markPrice = 10_100 → (100 * 10_000) / 10_000 = 100 bps.
	rate := funding.CalcFundingRate(10_100, 10_000, 100)
	if rate != 100 {
		t.Errorf("EC07: rate at exact boundary want 100, got %d", rate)
	}

	// One over boundary: markPrice = 10_101 → (101 * 10_000) / 10_000 = 101 → clamped to 100.
	rateClamped := funding.CalcFundingRate(10_101, 10_000, 100)
	if rateClamped != 100 {
		t.Errorf("EC07: rate one over boundary should be clamped to 100, got %d", rateClamped)
	}

	// Negative clamp: markPrice = 9_899 → (-101 * 10_000) / 10_000 = -101 → clamped to -100.
	rateNegClamped := funding.CalcFundingRate(9_899, 10_000, 100)
	if rateNegClamped != -100 {
		t.Errorf("EC07: negative rate over boundary should be clamped to -100, got %d", rateNegClamped)
	}

	// Exactly at negative boundary: markPrice = 9_900 → -100 bps (not clamped).
	rateNeg := funding.CalcFundingRate(9_900, 10_000, 100)
	if rateNeg != -100 {
		t.Errorf("EC07: negative rate at exact boundary want -100, got %d", rateNeg)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// EC08 – Conditional order triggers at exact trigger price boundary
// ────────────────────────────────────────────────────────────────────────────
// TriggerGTE: fires when markPrice >= triggerPrice.
//   Sub-test A: markPrice == triggerPrice → fires.
//   Sub-test B: markPrice == triggerPrice - 1 → does NOT fire.
//
// TriggerLTE: fires when markPrice <= triggerPrice.
//   Sub-test C: markPrice == triggerPrice → fires.
//   Sub-test D: markPrice == triggerPrice + 1 → does NOT fire.
func TestEC08_ConditionalOrderTriggerBoundary(t *testing.T) {
	makeConditionalNode := func(t *testing.T) (*node.LocalNode, string, string, int64) {
		t.Helper()
		n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
			t, 1000, 500, 10, 100_000, 500_000, 500_000,
		)
		// Open Alice LONG position so ReduceOnly close is valid.
		nextH = matchPerp(n, bobId, bobSess, aliceId, aliceSess, "BTC-USDC-PERP", 10_000, 1, nextH, nextH+1)
		return n, aliceId, aliceSess, nextH
	}

	// Sub-test A: TriggerGTE fires at exact price.
	t.Run("GTE_ExactPrice_Fires", func(t *testing.T) {
		n, aliceId, aliceSess, nextH := makeConditionalNode(t)

		var triggered bool
		n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
			p := e.Payload.(state.ConditionalOrderTriggeredPayload)
			if p.AccountId == aliceId {
				triggered = true
			}
		})

		co := clob.ConditionalOrder{
			OrderId:          "co-a",
			AccountId:        aliceId,
			SessionId:        aliceSess,
			MarketId:         "BTC-USDC-PERP",
			Side:             clob.OrderSideSell,
			OrderType:        clob.OrderTypeMarket,
			Quantity:         1,
			TriggerPrice:     11_000,
			TriggerCondition: clob.TriggerGTE,
			ReduceOnly:       true,
			ExpireBlockHeight: nextH + 100,
			Status:           clob.OrderStatusOpen,
		}
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(co).Build())
		nextH++

		// Mark price set to exactly triggerPrice.
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 11_000).Build())
		nextH++
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())

		if !triggered {
			t.Error("EC08 GTE_ExactPrice: conditional order should fire at markPrice == triggerPrice")
		}
	})

	// Sub-test B: TriggerGTE does NOT fire one below trigger price.
	t.Run("GTE_OneBelowPrice_DoesNotFire", func(t *testing.T) {
		n, aliceId, aliceSess, nextH := makeConditionalNode(t)

		var triggered bool
		n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
			p := e.Payload.(state.ConditionalOrderTriggeredPayload)
			if p.AccountId == aliceId {
				triggered = true
			}
		})

		co := clob.ConditionalOrder{
			OrderId:          "co-b",
			AccountId:        aliceId,
			SessionId:        aliceSess,
			MarketId:         "BTC-USDC-PERP",
			Side:             clob.OrderSideSell,
			OrderType:        clob.OrderTypeMarket,
			Quantity:         1,
			TriggerPrice:     11_000,
			TriggerCondition: clob.TriggerGTE,
			ReduceOnly:       true,
			ExpireBlockHeight: nextH + 100,
			Status:           clob.OrderStatusOpen,
		}
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(co).Build())
		nextH++

		// Mark price one below trigger.
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 10_999).Build())
		nextH++
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())

		if triggered {
			t.Error("EC08 GTE_OneBelowPrice: conditional order must NOT fire when markPrice < triggerPrice")
		}
	})

	// Sub-test C: TriggerLTE fires at exact price.
	t.Run("LTE_ExactPrice_Fires", func(t *testing.T) {
		n, aliceId, aliceSess, nextH := makeConditionalNode(t)

		var triggered bool
		n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
			p := e.Payload.(state.ConditionalOrderTriggeredPayload)
			if p.AccountId == aliceId {
				triggered = true
			}
		})

		co := clob.ConditionalOrder{
			OrderId:          "co-c",
			AccountId:        aliceId,
			SessionId:        aliceSess,
			MarketId:         "BTC-USDC-PERP",
			Side:             clob.OrderSideSell,
			OrderType:        clob.OrderTypeMarket,
			Quantity:         1,
			TriggerPrice:     9_500,
			TriggerCondition: clob.TriggerLTE,
			ReduceOnly:       true,
			ExpireBlockHeight: nextH + 100,
			Status:           clob.OrderStatusOpen,
		}
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(co).Build())
		nextH++

		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 9_500).Build())
		nextH++
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())

		if !triggered {
			t.Error("EC08 LTE_ExactPrice: conditional order should fire at markPrice == triggerPrice")
		}
	})

	// Sub-test D: TriggerLTE does NOT fire one above trigger price.
	t.Run("LTE_OneAbovePrice_DoesNotFire", func(t *testing.T) {
		n, aliceId, aliceSess, nextH := makeConditionalNode(t)

		var triggered bool
		n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
			p := e.Payload.(state.ConditionalOrderTriggeredPayload)
			if p.AccountId == aliceId {
				triggered = true
			}
		})

		co := clob.ConditionalOrder{
			OrderId:          "co-d",
			AccountId:        aliceId,
			SessionId:        aliceSess,
			MarketId:         "BTC-USDC-PERP",
			Side:             clob.OrderSideSell,
			OrderType:        clob.OrderTypeMarket,
			Quantity:         1,
			TriggerPrice:     9_500,
			TriggerCondition: clob.TriggerLTE,
			ReduceOnly:       true,
			ExpireBlockHeight: nextH + 100,
			Status:           clob.OrderStatusOpen,
		}
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(co).Build())
		nextH++

		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 9_501).Build())
		nextH++
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())

		if triggered {
			t.Error("EC08 LTE_OneAbovePrice: conditional order must NOT fire when markPrice > triggerPrice")
		}
	})
}

// ────────────────────────────────────────────────────────────────────────────
// EC09 – PERP partial fill: GTC order keeps full reserve until cancel/expire
// ────────────────────────────────────────────────────────────────────────────
// Alice submits a GTC BUY for 4 lots. Bob partially fills 2 lots. The other
// 2 lots rest on the book.
//
// Design: For GTC orders, the full margin is reserved upfront and only released
// when the remaining resting quantity is cancelled or expires. This prevents
// over-ordering on a single margin budget.
//
// Verify:
//   1. Position is correctly updated to 2 lots after partial fill.
//   2. Reserved stays equal to the full 4-lot margin (GTC convention).
//   3. Available balance is NOT negative.
//   4. AllocatedMargin in position tracker ≥ 0.
func TestEC09_PerpPartialFillGTCReserveKept(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
		t, 1000, 500, 10, 100_000, 500_000, 500_000,
	)

	// Alice places a resting BUY for 4 lots @ 10_000.
	// Full margin reserved = 4 * 10_000 / 10 = 4_000.
	aliceBuy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 4, clob.TimeInForceGtc, nextH)
	aliceBuy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(aliceBuy).Build())
	nextH++

	reservedAfterOrder := n.GetBalance(aliceId, "USDC").Reserved

	// Bob sells 2 lots → partial fill.
	bobSell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 2, clob.TimeInForceGtc, nextH)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(bobSell).Build())
	nextH++

	pos := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	bal := n.GetBalance(aliceId, "USDC")

	// 1. Position correctly shows 2 lots.
	if pos.NetQuantity != 2 {
		t.Errorf("EC09: expected NetQty=2 after partial fill, got %d", pos.NetQuantity)
	}
	// 2. Reserved remains at the full 4-lot margin (GTC keeps full reserve).
	if bal.Reserved != reservedAfterOrder {
		t.Errorf("EC09: GTC order reserve should be unchanged after partial fill: want %d got %d",
			reservedAfterOrder, bal.Reserved)
	}
	// 3. Available balance is not negative.
	if bal.Available < 0 {
		t.Errorf("EC09: Available balance must not go negative, got %d", bal.Available)
	}
	// 4. AllocatedMargin is non-negative.
	if pos.AllocatedMargin < 0 {
		t.Errorf("EC09: AllocatedMargin must not go negative, got %d", pos.AllocatedMargin)
	}
	_ = nextH
}

// ────────────────────────────────────────────────────────────────────────────
// EC10 – Funding interval fires exactly at configured block boundary
// ────────────────────────────────────────────────────────────────────────────
// Regardless of when the last funding epoch fired during setup, we compute the
// exact number of blocks until the NEXT epoch and verify:
//   - (blocksUntilNext - 1) blocks: lastFundingBlock unchanged.
//   - 1 more block: lastFundingBlock advances.
func TestEC10_FundingIntervalFiresAtBoundary(t *testing.T) {
	const interval = int64(10)
	n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
		t, 1000, 500, 10, interval, 200_000, 200_000,
	)

	// Open position so funding has something to settle.
	nextH = matchPerp(n, bobId, bobSess, aliceId, aliceSess, "BTC-USDC-PERP", 10_000, 1, nextH, nextH+1)

	// Snapshot last funding block and current height.
	lastFundingBefore := n.GetLastFundingBlock("BTC-USDC-PERP")
	// nextH-1 is the last processed block height.
	currentH := nextH - 1
	elapsed := currentH - lastFundingBefore  // blocks since last funding
	blocksUntilNext := interval - elapsed    // how many more blocks needed

	// If the interval was already reached during setup, advance past it so we
	// start fresh from a just-fired state.
	if blocksUntilNext <= 0 {
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
		nextH++
		lastFundingBefore = n.GetLastFundingBlock("BTC-USDC-PERP")
		currentH = nextH - 1
		elapsed = currentH - lastFundingBefore
		blocksUntilNext = interval - elapsed
	}

	// Run (blocksUntilNext - 1) blocks: funding must NOT fire.
	for i := int64(0); i < blocksUntilNext-1; i++ {
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
		nextH++
	}
	midFunding := n.GetLastFundingBlock("BTC-USDC-PERP")
	if midFunding != lastFundingBefore {
		t.Errorf("EC10: funding fired early — lastFundingBlock changed from %d to %d (%d blocks before boundary)",
			lastFundingBefore, midFunding, blocksUntilNext-1)
	}

	// One more block — exactly at interval boundary → funding fires.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
	nextH++
	lastFundingAfter := n.GetLastFundingBlock("BTC-USDC-PERP")
	if lastFundingAfter <= lastFundingBefore {
		t.Errorf("EC10: funding should have fired at the interval boundary; lastFundingBlock stayed at %d", lastFundingAfter)
	}

	// Position state must remain consistent.
	pos := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if pos.AllocatedMargin < 0 {
		t.Errorf("EC10: AllocatedMargin must not go negative after funding, got %d", pos.AllocatedMargin)
	}
	_ = nextH
}

// ────────────────────────────────────────────────────────────────────────────
// EC11 – Multiple PERP orders exhaust margin without over-reservation
// ────────────────────────────────────────────────────────────────────────────
// Alice has exactly 3_000 USDC. Each PERP order at price=10_000, qty=1,
// 10x leverage requires 1_000 margin (10% of 10_000).
// She places 3 orders → exactly exhausts 3_000 margin.
// A 4th order should be rejected.
//
// Uses a dedicated node with only 3_000 USDC to avoid the 100k base balance
// from bootstrapNode that would mask the exhaustion.
func TestEC11_MultiOrderMarginExhaustion(t *testing.T) {
	// Build a node from scratch with Alice holding exactly 3_000 USDC.
	n2 := node.NewLocalNode()
	var aliceId, bobId string
	n2.Subscribe(state.EventAccountCreated, func(e state.Event) {
		p := e.Payload.(state.AccountCreatedPayload)
		if p.OwnerAddress == "alice" {
			aliceId = p.AccountId
		} else if p.OwnerAddress == "bob" {
			bobId = p.AccountId
		}
	})
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddCreateAccount("alice", "a-root", "a-withdraw").
		AddCreateAccount("bob", "b-root", "b-withdraw").Build())

	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000, // 10%
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 100_000,
			MaxFundingRateBps:     100,
		}).Build())

	var aliceSess string
	n2.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == aliceId {
			aliceSess = p.SessionId
		}
	})
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddCreateSession(aliceId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC-PERP"},
			MaxOrderAmount: 1_000_000,
		}).Build())

	// Alice gets exactly 3_000 USDC.
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddDeposit(aliceId, "USDC", 3_000).Build())

	_ = bobId

	var rejectedCount int
	n2.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		if p.AccountId == aliceId {
			rejectedCount++
		}
	})

	nextH := int64(5)
	// Place 3 resting GTC PERP BUY orders — each needs 1_000 margin.
	for i := int64(0); i < 3; i++ {
		o := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, nextH)
		o.AccountSequence = n2.GetAccountSequence(aliceId)
		_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(o).Build())
		nextH++
	}

	// 4th order — should be rejected: 0 USDC left (all reserved).
	o4 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, nextH)
	o4.AccountSequence = n2.GetAccountSequence(aliceId)
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(o4).Build())

	if rejectedCount != 1 {
		t.Errorf("EC11: expected exactly 1 rejected order (4th), got %d", rejectedCount)
	}

	bal := n2.GetBalance(aliceId, "USDC")
	// Reserved must be exactly 3_000 (3 × 1_000).
	if bal.Reserved != 3_000 {
		t.Errorf("EC11: expected 3_000 reserved for 3 orders, got %d", bal.Reserved)
	}
	if bal.Available < 0 {
		t.Errorf("EC11: Available balance must not go negative, got %d", bal.Available)
	}
	if bal.Available != 0 {
		t.Errorf("EC11: Available should be 0 after exhausting margin, got %d", bal.Available)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// EC12 – ReduceOnly rejected when it would increase position
// ────────────────────────────────────────────────────────────────────────────
// Alice has a SHORT position of 1. She submits a ReduceOnly SELL (same
// direction as short = would increase the short). The engine must reject it.
// Only a ReduceOnly BUY should be accepted for a short position.
func TestEC12_ReduceOnlyRejectedWhenIncreasing(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess, nextH := bootstrapPerpNode(
		t, 1000, 500, 10, 100_000, 500_000, 500_000,
	)

	// Alice opens SHORT by selling 1, Bob buys 1.
	buy := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, nextH)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(buy).Build())
	nextH++
	sell := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, nextH)
	sell.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(sell).Build())
	nextH++

	pos := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if pos.NetQuantity != -1 {
		t.Fatalf("EC12: expected SHORT position NetQty=-1, got %d", pos.NetQuantity)
	}

	// ReduceOnly SELL on a SHORT position increases the short → must be rejected.
	var rejected bool
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		if p.AccountId == aliceId {
			rejected = true
		}
	})

	// Bob needs to post a resting sell for Alice's ReduceOnly buy to match against.
	// But first: attempt the invalid ReduceOnly SELL (same direction as existing short).
	invalidReduceOnly := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideSell, 9_000, 1, clob.TimeInForceGtc, nextH)
	invalidReduceOnly.ReduceOnly = true
	invalidReduceOnly.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(invalidReduceOnly).Build())
	nextH++

	if !rejected {
		t.Error("EC12: ReduceOnly SELL on a SHORT position should be rejected (it would increase the short)")
	}

	// Position must remain unchanged.
	posAfter := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if posAfter.NetQuantity != -1 {
		t.Errorf("EC12: position must remain at -1 after rejected ReduceOnly, got %d", posAfter.NetQuantity)
	}
}
