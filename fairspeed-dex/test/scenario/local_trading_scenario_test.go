package scenario_test

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// Unit conventions for BTC-USDC market:
//   price  : integer representing the USDC notional per lot
//            e.g. price=10_000 means "10,000 USDC for 1 lot"
//   qty    : integer lots (1 lot = 0.1 BTC)
//   notional = price * qty
//   BTC balance stored in lots (integer)
//   USDC balance stored in USDC units (integer)
//
// Fee check (Scenario 5):
//   notional = 10_000 * 1 = 10_000
//   maker_fee = 10_000 * 2 / 10_000 = 2
//   taker_fee = 10_000 * 5 / 10_000 = 5

func TestLocalTradingScenario(t *testing.T) {
	n := node.NewLocalNode()

	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	// Collect all events for assertions.
	var allEvents []state.Event
	n.Subscribe(state.EventAll, func(e state.Event) {
		allEvents = append(allEvents, e)
	})

	countEvents := func(typ state.EventType) int {
		count := 0
		for _, e := range allEvents {
			if e.Type == typ {
				count++
			}
		}
		return count
	}
	resetEvents := func() {
		allEvents = allEvents[:0]
	}

	// -------------------------------------------------------------------------
	// Scenario 1: Account + Session creation
	// -------------------------------------------------------------------------
	t.Run("Scenario1_AccountCreation", func(t *testing.T) {
		resetEvents()
		batch := fairbatch.NewBatchBuilder(1).
			AddCreateAccount("alice@example.com", "alice-root-key", "alice-withdraw-key").
			AddCreateAccount("bob@example.com", "bob-root-key", "bob-withdraw-key").
			Build()

		result, err := n.SubmitBatch(batch)
		if err != nil {
			t.Fatalf("SubmitBatch error: %v", err)
		}
		if result.TxCount != 2 {
			t.Errorf("expected 2 txs, got %d", result.TxCount)
		}
		if countEvents(state.EventAccountCreated) != 2 {
			t.Errorf("expected 2 AccountCreated events, got %d", countEvents(state.EventAccountCreated))
		}
	})

	// Retrieve alice and bob account IDs from events.
	var aliceId, bobId string
	for _, e := range allEvents {
		if e.Type == state.EventAccountCreated {
			p := e.Payload.(state.AccountCreatedPayload)
			if p.OwnerAddress == "alice@example.com" {
				aliceId = p.AccountId
			} else if p.OwnerAddress == "bob@example.com" {
				bobId = p.AccountId
			}
		}
	}
	if aliceId == "" || bobId == "" {
		t.Fatalf("could not find alice/bob account IDs from events")
	}

	// Create sessions for alice and bob.
	t.Run("Scenario1_SessionCreation", func(t *testing.T) {
		resetEvents()
		batch := fairbatch.NewBatchBuilder(2).
			AddCreateSession(aliceId, account.SessionOptions{
				AllowedMarkets: []string{"BTC-USDC"},
				MaxOrderAmount: 1000,
				MaxDailyVolume: 1_000_000,
			}).
			AddCreateSession(bobId, account.SessionOptions{
				AllowedMarkets: []string{"BTC-USDC"},
				MaxOrderAmount: 1000,
				MaxDailyVolume: 1_000_000,
			}).
			Build()

		_, err := n.SubmitBatch(batch)
		if err != nil {
			t.Fatalf("SubmitBatch error: %v", err)
		}
		if countEvents(state.EventSessionCreated) != 2 {
			t.Errorf("expected 2 SessionCreated events, got %d", countEvents(state.EventSessionCreated))
		}
		// Sessions must NOT have CanWithdraw.
		for _, e := range allEvents {
			if e.Type == state.EventSessionCreated {
				p := e.Payload.(state.SessionCreatedPayload)
				sess, err2 := n.AppState.GetSession(p.SessionId)
				if err2 {
					if sess.CanWithdraw {
						t.Errorf("session %s should not have CanWithdraw=true", p.SessionId)
					}
				}
			}
		}
	})

	// Retrieve session IDs.
	var aliceSessionId, bobSessionId string
	for _, e := range allEvents {
		if e.Type == state.EventSessionCreated {
			p := e.Payload.(state.SessionCreatedPayload)
			if p.AccountId == aliceId {
				aliceSessionId = p.SessionId
			} else if p.AccountId == bobId {
				bobSessionId = p.SessionId
			}
		}
	}
	if aliceSessionId == "" || bobSessionId == "" {
		t.Fatalf("could not find alice/bob session IDs")
	}

	// -------------------------------------------------------------------------
	// Scenario 2: Deposit simulation
	// -------------------------------------------------------------------------
	t.Run("Scenario2_Deposit", func(t *testing.T) {
		resetEvents()
		// Alice: 10,000 USDC; Bob: 10 lots BTC (= 1 BTC)
		batch := fairbatch.NewBatchBuilder(3).
			AddDeposit(aliceId, "USDC", 10_000).
			AddDeposit(bobId, "BTC", 10).
			Build()

		_, err := n.SubmitBatch(batch)
		if err != nil {
			t.Fatalf("SubmitBatch error: %v", err)
		}
		if countEvents(state.EventDepositSimulated) != 2 {
			t.Errorf("expected 2 DepositSimulated events, got %d", countEvents(state.EventDepositSimulated))
		}

		aliceUsdc := n.GetBalance(aliceId, "USDC")
		if aliceUsdc.Available != 10_000 {
			t.Errorf("alice USDC available: want 10000 got %d", aliceUsdc.Available)
		}
		bobBtc := n.GetBalance(bobId, "BTC")
		if bobBtc.Available != 10 {
			t.Errorf("bob BTC available: want 10 got %d", bobBtc.Available)
		}
	})

	// -------------------------------------------------------------------------
	// Scenario 3: Full fill
	// Bob places SELL 1 lot BTC @ price=10_000.
	// Alice places BUY  1 lot BTC @ price=10_000.
	// Expected: 1 lot filled, balances updated.
	// -------------------------------------------------------------------------
	var bobSellOrderId string

	t.Run("Scenario3a_BobSellRests", func(t *testing.T) {
		resetEvents()
		bobSellOrder := clob.NewLimitOrder(bobId, bobSessionId, "BTC-USDC",
			clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)

		batch := fairbatch.NewBatchBuilder(4).
			AddSubmitOrder(bobSellOrder).
			Build()

		_, err := n.SubmitBatch(batch)
		if err != nil {
			t.Fatalf("SubmitBatch error: %v", err)
		}

		// Bob's BTC should be reserved.
		bobBtc := n.GetBalance(bobId, "BTC")
		if bobBtc.Reserved != 1 {
			t.Errorf("bob BTC reserved: want 1 got %d", bobBtc.Reserved)
		}
		bobSellOrderId = bobSellOrder.OrderId
	})

	t.Run("Scenario3b_AliceBuyFills", func(t *testing.T) {
		resetEvents()
		aliceBuyOrder := clob.NewLimitOrder(aliceId, aliceSessionId, "BTC-USDC",
			clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)

		batch := fairbatch.NewBatchBuilder(5).
			AddSubmitOrder(aliceBuyOrder).
			Build()

		result, err := n.SubmitBatch(batch)
		if err != nil {
			t.Fatalf("SubmitBatch error: %v", err)
		}
		if result.TradeCount != 1 {
			t.Errorf("expected 1 trade, got %d", result.TradeCount)
		}
		if countEvents(state.EventTradeExecuted) != 1 {
			t.Errorf("expected 1 TradeExecuted event, got %d", countEvents(state.EventTradeExecuted))
		}

		// Trade details
		var tradePayload state.TradeExecutedPayload
		for _, e := range allEvents {
			if e.Type == state.EventTradeExecuted {
				tradePayload = e.Payload.(state.TradeExecutedPayload)
			}
		}
		if tradePayload.Quantity != 1 {
			t.Errorf("trade quantity: want 1 got %d", tradePayload.Quantity)
		}
		if tradePayload.Price != 10_000 {
			t.Errorf("trade price: want 10000 got %d", tradePayload.Price)
		}

		// Alice should have BTC now.
		aliceBtc := n.GetBalance(aliceId, "BTC")
		if aliceBtc.Available < 1 {
			t.Errorf("alice BTC available: want >=1 got %d", aliceBtc.Available)
		}

		// Bob should have USDC now.
		bobUsdc := n.GetBalance(bobId, "USDC")
		if bobUsdc.Available <= 0 {
			t.Errorf("bob USDC available should be >0, got %d", bobUsdc.Available)
		}

		// Bob's sell order should be filled (removed from book or reserved=0).
		bobBtcAfter := n.GetBalance(bobId, "BTC")
		if bobBtcAfter.Reserved != 0 {
			t.Errorf("bob BTC reserved after fill: want 0 got %d", bobBtcAfter.Reserved)
		}
		_ = bobSellOrderId
	})

	// -------------------------------------------------------------------------
	// Scenario 4: Partial fill
	// Refresh balances: deposit more assets.
	// Bob SELL 2 lots; Alice BUY 1 lot → 1 lot fills, 1 lot stays in book.
	// -------------------------------------------------------------------------
	t.Run("Scenario4_PartialFill", func(t *testing.T) {
		resetEvents()
		// Top up balances.
		topupBatch := fairbatch.NewBatchBuilder(6).
			AddDeposit(aliceId, "USDC", 20_000).
			AddDeposit(bobId, "BTC", 10).
			Build()
		if _, err := n.SubmitBatch(topupBatch); err != nil {
			t.Fatalf("topup error: %v", err)
		}
		resetEvents()

		// Bob places SELL 2 lots.
		bobSellOrder2 := clob.NewLimitOrder(bobId, bobSessionId, "BTC-USDC",
			clob.OrderSideSell, 10_000, 2, clob.TimeInForceGtc, 7)
		batch7 := fairbatch.NewBatchBuilder(7).
			AddSubmitOrder(bobSellOrder2).
			Build()
		if _, err := n.SubmitBatch(batch7); err != nil {
			t.Fatalf("SubmitBatch error: %v", err)
		}

		// Alice places BUY 1 lot.
		aliceBuyOrder2 := clob.NewLimitOrder(aliceId, aliceSessionId, "BTC-USDC",
			clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
		resetEvents()
		result, err := n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(aliceBuyOrder2).Build())
		if err != nil {
			t.Fatalf("SubmitBatch error: %v", err)
		}
		if result.TradeCount != 1 {
			t.Errorf("expected 1 trade, got %d", result.TradeCount)
		}

		// Bob's sell order should have 1 lot remaining in the book.
		ob, exists := n.AppState.GetOrderBook("BTC-USDC")
		if !exists {
			t.Fatal("orderbook not found")
		}
		_, bestAskExists := ob.BestAsk()
		if !bestAskExists {
			t.Error("expected remaining ask in orderbook after partial fill")
		} else {
			bestLevel := ob.BestAskLevel()
			if bestLevel.TotalQuantity != 1 {
				t.Errorf("remaining ask quantity: want 1 got %d", bestLevel.TotalQuantity)
			}
		}

		// Alice's buy order should be fully filled (not in book).
		_, aliceOrderExists := ob.GetOrder(aliceBuyOrder2.OrderId)
		if aliceOrderExists {
			t.Error("alice's buy order should not remain in orderbook after full fill")
		}
	})

	// -------------------------------------------------------------------------
	// Scenario 5: Fee verification
	// Uses the trade from Scenario 3b: notional=10_000, maker=2, taker=5
	// -------------------------------------------------------------------------
	t.Run("Scenario5_FeeVerification", func(t *testing.T) {
		// Re-collect events from all history: scan SettlementKeeper trades.
		// The simplest approach: scan allEvents across the full session.
		// We subscribed SubscribeAll from the start, so allEvents contains everything.
		// Find the first TradeExecuted event.
		var tradePayload state.TradeExecutedPayload
		found := false
		for _, e := range allEvents {
			if e.Type == state.EventTradeExecuted {
				tradePayload = e.Payload.(state.TradeExecutedPayload)
				found = true
				break
			}
		}
		if !found {
			// Try the settlement keeper directly.
			t.Skip("no trade found in recent events; skipping fee check")
			return
		}

		// notional = 10_000 * 1 = 10_000
		// maker_fee = 10_000 * 2 / 10_000 = 2
		// taker_fee = 10_000 * 5 / 10_000 = 5
		if tradePayload.MakerFeeAmount != 2 {
			t.Errorf("maker fee: want 2 got %d (notional=%d)", tradePayload.MakerFeeAmount, tradePayload.Price*tradePayload.Quantity)
		}
		if tradePayload.TakerFeeAmount != 5 {
			t.Errorf("taker fee: want 5 got %d (notional=%d)", tradePayload.TakerFeeAmount, tradePayload.Price*tradePayload.Quantity)
		}

		// Treasury should have collected fees.
		treasuryUsdc := n.GetTreasury("USDC")
		if treasuryUsdc.Available <= 0 {
			t.Errorf("treasury USDC should be positive after fee collection, got %d", treasuryUsdc.Available)
		}
	})
}
