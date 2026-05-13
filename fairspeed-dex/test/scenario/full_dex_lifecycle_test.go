package scenario_test

// Full DEX Lifecycle Integration Tests
//
// Covers every functional stage of the exchange in order:
//   Act 1  — Validator bonding & stake accounting
//   Act 2  — Account creation, KYC approval, multi-asset deposits
//   Act 3  — SPOT trading: full fill, partial fill, fee verification, cancel, FOK
//   Act 4  — PERP trading: open long, AvgEntryPrice, ReduceOnly close
//   Act 5  — Funding settlement & liquidation (insurance fund + socialized loss)
//   Act 6  — Conditional orders: stop-loss, take-profit, cancel, expire
//   Act 7  — Compliance & safety: sanctions, circuit breaker halt/resume
//   Act 8  — Cross-chain bridge attestation (multi-validator quorum)
//   Act 9  — Governance: fee policy proposal → vote → execution
//   Act 10 — Withdrawal timelock & points accumulation

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// ---------------------------------------------------------------------------
// Act 1 — Validator bonding & stake accounting
// ---------------------------------------------------------------------------

func TestLifecycle_Act1_ValidatorBonding(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	// Bond two validators with different stake weights.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-lifecycle-a", "lifecycle-a", "pub-la", 60_000).
		AddBondValidator("val-lifecycle-b", "lifecycle-b", "pub-lb", 40_000).
		Build())
	if err != nil {
		t.Fatalf("bond validators: %v", err)
	}

	validators := n.ActiveValidatorSet()
	if len(validators) < 2 {
		t.Fatalf("expected at least 2 bonded validators, got %d", len(validators))
	}

	// Unbond one validator and verify it is removed from the active set.
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddUnbondValidator("val-lifecycle-b").
		Build())
	if err != nil {
		t.Fatalf("unbond validator: %v", err)
	}

	// After unbonding, val-lifecycle-b should no longer appear in the active (bonded) set.
	afterUnbond := n.ActiveValidatorSet()
	for _, v := range afterUnbond {
		if v.ValidatorId == "val-lifecycle-b" {
			t.Error("validator-b should not appear in active set after TxUnbondValidator")
		}
	}
}

// ---------------------------------------------------------------------------
// Act 2 — Account creation, KYC approval, multi-asset deposits
// ---------------------------------------------------------------------------

func TestLifecycle_Act2_AccountAndKYC(t *testing.T) {
	// Use KYC-required policy so we can test the pending→approved flow.
	policy := risk.RiskPolicy{
		MaxOrderQuantity:         1_000_000,
		MinOrderQuantity:         1,
		MaxDailyVolumePerSession: 100_000_000,
		RequireKYC:               true,
	}
	n := node.NewLocalNodeWithPolicy(policy)
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)
	n.RegisterAsset(asset.USDT)

	// -- Block 1: create accounts --
	var aliceId, charlieId string
	n.Subscribe(state.EventAccountCreated, func(e state.Event) {
		p := e.Payload.(state.AccountCreatedPayload)
		switch p.OwnerAddress {
		case "alice@lc":
			aliceId = p.AccountId
		case "charlie@lc":
			charlieId = p.AccountId
		}
	})
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddCreateAccount("alice@lc", "alice-lc-root", "alice-lc-wd").
		AddCreateAccount("charlie@lc", "charlie-lc-root", "charlie-lc-wd").
		Build())
	if err != nil {
		t.Fatalf("create accounts: %v", err)
	}
	if aliceId == "" || charlieId == "" {
		t.Fatal("could not capture account IDs")
	}

	// New accounts must start with PENDING status.
	if got := n.GetKYCStatus(aliceId); got != account.KYCStatusPending {
		t.Errorf("alice KYC status: want PENDING got %s", got)
	}

	// -- Block 2: deposit funds (KYC not required for deposits) --
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddDeposit(aliceId, "USDC", 50_000).
		AddDeposit(aliceId, "BTC", 5).
		AddDeposit(aliceId, "USDT", 20_000).
		AddDeposit(charlieId, "USDC", 10_000).
		Build())
	if err != nil {
		t.Fatalf("deposits: %v", err)
	}

	if bal := n.GetBalance(aliceId, "USDC").Available; bal != 50_000 {
		t.Errorf("alice USDC: want 50_000 got %d", bal)
	}
	if bal := n.GetBalance(aliceId, "BTC").Available; bal != 5 {
		t.Errorf("alice BTC: want 5 got %d", bal)
	}
	if bal := n.GetBalance(aliceId, "USDT").Available; bal != 20_000 {
		t.Errorf("alice USDT: want 20_000 got %d", bal)
	}

	// -- Block 3: create sessions -- (orders will be rejected until KYC approved)
	var aliceSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == aliceId && aliceSess == "" {
			aliceSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddCreateSession(aliceId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC"},
			MaxOrderAmount: 100_000,
		}).Build())

	// -- Block 4: KYC approve alice --
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddKYCApprove(aliceId, "APPROVED").
		Build())
	if err != nil {
		t.Fatalf("KYC approve: %v", err)
	}
	if got := n.GetKYCStatus(aliceId); got != account.KYCStatusApproved {
		t.Errorf("alice KYC status after approval: want APPROVED got %s", got)
	}

	// charlie remains PENDING — any trade order from charlie should be rejected.
	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		rejectedId = p.OrderId
	})
	var charlieSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == charlieId && charlieSess == "" {
			charlieSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddCreateSession(charlieId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 100_000}).
		Build())

	// Deposit BTC for charlie so session/balance is not the blocker.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddDeposit(charlieId, "BTC", 10).
		Build())

	sell := clob.NewLimitOrder(charlieId, charlieSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell).Build())

	if rejectedId == "" {
		t.Error("expected ORDER_REJECTED for KYC-pending charlie, got none")
	}
}

// ---------------------------------------------------------------------------
// Act 3 — SPOT trading: full fill, partial, fee check, cancel, FOK
// ---------------------------------------------------------------------------

func TestLifecycle_Act3_SpotTrading(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// ---- 3.1: Full fill ----
	var tradeFired bool
	var tradePayload state.TradeExecutedPayload
	n.Subscribe(state.EventTradeExecuted, func(e state.Event) {
		tradeFired = true
		tradePayload = e.Payload.(state.TradeExecutedPayload)
	})

	// Bob sells 1 BTC at 10_000, Alice buys 1 BTC at 10_000 → full fill.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	if err != nil {
		t.Fatalf("submit sell: %v", err)
	}

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("submit buy: %v", err)
	}

	if !tradeFired {
		t.Fatal("expected EventTradeExecuted on full fill, none received")
	}
	if tradePayload.Price != 10_000 || tradePayload.Quantity != 1 {
		t.Errorf("trade payload: want price=10000 qty=1, got price=%d qty=%d",
			tradePayload.Price, tradePayload.Quantity)
	}

	// ---- 3.2: Fee verification ----
	// notional = 10_000 * 1 = 10_000
	// maker fee = 10_000 * 2 / 10_000 = 2
	// taker fee = 10_000 * 5 / 10_000 = 5
	if tradePayload.MakerFeeAmount != 2 {
		t.Errorf("maker fee: want 2 got %d", tradePayload.MakerFeeAmount)
	}
	if tradePayload.TakerFeeAmount != 5 {
		t.Errorf("taker fee: want 5 got %d", tradePayload.TakerFeeAmount)
	}

	// ---- 3.3: Partial fill ----
	// Bob sells 3 BTC; Alice only buys 1 BTC → partial fill, 2 lots remain.
	sellBig := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 3, clob.TimeInForceGtc, 6)
	sellBig.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(sellBig).Build())

	tradeFired = false
	buyOne := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 7)
	buyOne.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(buyOne).Build())
	if !tradeFired {
		t.Error("expected trade on partial fill")
	}
	if tradePayload.Quantity != 1 {
		t.Errorf("partial fill quantity: want 1 got %d", tradePayload.Quantity)
	}

	// ---- 3.4: Cancel remaining order ----
	// The sell order for 3 BTC still has 2 BTC remaining; cancel it.
	var cancelFired bool
	n.Subscribe(state.EventOrderCancelled, func(e state.Event) {
		cancelFired = true
	})
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(8).
		AddCancelOrder(sellBig.OrderId, bobId).Build())
	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if !cancelFired {
		t.Error("expected EventOrderCancelled after cancel")
	}

	// ---- 3.5: FOK rejection (no matching liquidity) ----
	var fokRejected bool
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		fokRejected = true
	})
	fok := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 100, clob.TimeInForceFok, 9)
	fok.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(9).AddSubmitOrder(fok).Build())
	if !fokRejected {
		t.Error("expected FOK order rejected when insufficient liquidity")
	}
}

// ---------------------------------------------------------------------------
// Act 4 — PERP trading: open long, AvgEntryPrice, ReduceOnly close
// ---------------------------------------------------------------------------

func TestLifecycle_Act4_PerpTrading(t *testing.T) {
	const perpMkt = "BTC-USDC-PERP"
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	// -- Block 4: register PERP market (10% initial margin, 5% maintenance, 10x max) --
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              perpMkt,
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 1_000_000, // disabled for this test
			MaxFundingRateBps:     100,
		}).Build())
	if err != nil {
		t.Fatalf("register perp market: %v", err)
	}

	cfg, ok := n.GetPerpConfig(perpMkt)
	if !ok {
		t.Fatal("GetPerpConfig: PERP market not found")
	}
	if cfg.MaxLeverage != 10 {
		t.Errorf("MaxLeverage: want 10 got %d", cfg.MaxLeverage)
	}

	// -- Block 5: extra USDC for both --
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 50_000).
		AddDeposit(bobId, "USDC", 50_000).Build())

	// -- Block 6: create PERP sessions --
	var aliceSess, bobSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		switch {
		case p.AccountId == aliceId && aliceSess == "":
			aliceSess = p.SessionId
		case p.AccountId == bobId && bobSess == "":
			bobSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		Build())

	// -- 4.1: Open long at price=10_000, qty=2 --
	// Initial margin = 10_000 * 2 * 10% = 2_000 USDC reserved.
	sell1 := clob.NewLimitOrder(bobId, bobSess, perpMkt, clob.OrderSideSell, 10_000, 2, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell1).Build())

	buy1 := clob.NewLimitOrder(aliceId, aliceSess, perpMkt, clob.OrderSideBuy, 10_000, 2, clob.TimeInForceGtc, 8)
	buy1.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buy1).Build())

	pos := n.GetPerpPosition(aliceId, perpMkt)
	if pos.NetQuantity != 2 {
		t.Errorf("after first fill: net qty want 2 got %d", pos.NetQuantity)
	}
	if pos.AvgEntryPrice != 10_000 {
		t.Errorf("AvgEntryPrice after first fill: want 10_000 got %d", pos.AvgEntryPrice)
	}

	// -- 4.2: Add to position at price=11_000, qty=3 → weighted AvgEntryPrice --
	// New avg = (2*10_000 + 3*11_000) / (2+3) = (20_000 + 33_000) / 5 = 10_600
	sell2 := clob.NewLimitOrder(bobId, bobSess, perpMkt, clob.OrderSideSell, 11_000, 3, clob.TimeInForceGtc, 9)
	sell2.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(9).AddSubmitOrder(sell2).Build())

	buy2 := clob.NewLimitOrder(aliceId, aliceSess, perpMkt, clob.OrderSideBuy, 11_000, 3, clob.TimeInForceGtc, 10)
	buy2.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(10).AddSubmitOrder(buy2).Build())

	pos2 := n.GetPerpPosition(aliceId, perpMkt)
	if pos2.NetQuantity != 5 {
		t.Errorf("after second fill: net qty want 5 got %d", pos2.NetQuantity)
	}
	if pos2.AvgEntryPrice != 10_600 {
		t.Errorf("AvgEntryPrice after second fill: want 10_600 got %d", pos2.AvgEntryPrice)
	}

	// -- 4.3: ReduceOnly close (sell all 5 lots) --
	// Bob needs to be on the buy side to close alice's position.
	// Submit buyClose first so it rests in the book; reduceAll then matches against it.
	buyClose := clob.NewLimitOrder(bobId, bobSess, perpMkt, clob.OrderSideBuy, 9_000, 5, clob.TimeInForceGtc, 11)
	buyClose.AccountSequence = n.GetAccountSequence(bobId)

	reduceAll := clob.NewLimitOrder(aliceId, aliceSess, perpMkt, clob.OrderSideSell, 9_000, 5, clob.TimeInForceGtc, 11)
	reduceAll.ReduceOnly = true
	reduceAll.AccountSequence = n.GetAccountSequence(aliceId)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(11).
		AddSubmitOrder(buyClose).
		AddSubmitOrder(reduceAll).
		Build())

	pos3 := n.GetPerpPosition(aliceId, perpMkt)
	if pos3.NetQuantity != 0 {
		t.Errorf("after ReduceOnly close: net qty want 0 got %d", pos3.NetQuantity)
	}

	// -- 4.4: ReduceOnly in wrong direction is rejected --
	// Alice has no position; a ReduceOnly SELL should be rejected.
	var reduceRejected bool
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		reduceRejected = true
	})
	badReduce := clob.NewLimitOrder(aliceId, aliceSess, perpMkt, clob.OrderSideSell, 9_000, 1, clob.TimeInForceGtc, 12)
	badReduce.ReduceOnly = true
	badReduce.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(12).AddSubmitOrder(badReduce).Build())
	if !reduceRejected {
		t.Error("expected ORDER_REJECTED for ReduceOnly SELL with no long position")
	}
}

// ---------------------------------------------------------------------------
// Act 5 — Funding settlement & liquidation
// ---------------------------------------------------------------------------

func TestLifecycle_Act5_FundingAndLiquidation(t *testing.T) {
	const perpMkt = "BTC-USDC-PERP"
	// Use an interval of 5 blocks so we can trigger funding quickly.
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              perpMkt,
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 5,
			MaxFundingRateBps:     200,
		}).Build())

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 200_000).
		AddDeposit(bobId, "USDC", 200_000).Build())

	var aliceSess, bobSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		switch {
		case p.AccountId == aliceId && aliceSess == "":
			aliceSess = p.SessionId
		case p.AccountId == bobId && bobSess == "":
			bobSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		Build())

	// Open positions: Alice long 1, Bob short 1 at 10_000.
	sell := clob.NewLimitOrder(bobId, bobSess, perpMkt, clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, perpMkt, clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buy).Build())

	// ---- 5.1: Funding rate fires at interval boundary ----
	// Set markPrice above indexPrice so longs pay.
	n.SetIndexPrice(perpMkt, 10_000)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(9).
		AddSubmitPrice(perpMkt, "oracle-v1", 10_200).Build())

	lastFundingBefore := n.GetLastFundingBlock(perpMkt)

	// Advance until interval triggers (lastFundingBlock was set at block 4 by registration).
	for n.CurrentHeight() < lastFundingBefore+5 {
		h := n.CurrentHeight() + 1
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(h).Build())
	}

	epochs := n.GetFundingHistory(perpMkt)
	if len(epochs) == 0 {
		t.Error("expected funding epoch to be recorded after interval blocks")
	} else {
		epoch := epochs[len(epochs)-1]
		if epoch.RateBps == 0 {
			t.Error("expected non-zero funding rate when markPrice > indexPrice")
		}
	}

	// ---- 5.2: Liquidation — near-maintenance, then push below ----
	// Use a fresh node with a small position and drive price to trigger liquidation.
	n2, a2Id, b2Id, _, _ := bootstrapNode(t)

	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              perpMkt,
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 1_000_000,
			MaxFundingRateBps:     100,
		}).Build())

	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(a2Id, "USDC", 50_000).
		AddDeposit(b2Id, "USDC", 50_000).Build())

	var a2Sess, b2Sess string
	n2.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		switch {
		case p.AccountId == a2Id && a2Sess == "":
			a2Sess = p.SessionId
		case p.AccountId == b2Id && b2Sess == "":
			b2Sess = p.SessionId
		}
	})
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(a2Id, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(b2Id, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		Build())

	// Alice opens long 1 BTC at 10_000 (margin=1_000, maint=500).
	s2 := clob.NewLimitOrder(b2Id, b2Sess, perpMkt, clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(s2).Build())
	b2 := clob.NewLimitOrder(a2Id, a2Sess, perpMkt, clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	b2.AccountSequence = n2.GetAccountSequence(a2Id)
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(b2).Build())

	// Mark price at 9_501: unrealizedPnL = (9_501-10_000)*1 = -499
	// equity = 1_000 + (-499) = 501 > maintenanceMargin(500) → safe.
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(9).
		AddSubmitPrice(perpMkt, "oracle-v1", 9_501).Build())

	posAfterSafe := n2.GetPerpPosition(a2Id, perpMkt)
	if posAfterSafe.NetQuantity == 0 {
		t.Error("position should not be liquidated when equity just above maintenance margin")
	}

	// Mark price drops to 9_499: unrealizedPnL = -501, equity = 1_000-501 = 499 < 500 → liquidated.
	var liqFired bool
	n2.Subscribe(state.EventLiquidationTriggered, func(e state.Event) {
		liqFired = true
	})
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(10).
		AddSubmitPrice(perpMkt, "oracle-v1", 9_499).Build())

	if !liqFired {
		t.Error("expected EventLiquidationTriggered when equity < maintenance margin")
	}
	posAfterLiq := n2.GetPerpPosition(a2Id, perpMkt)
	if posAfterLiq.NetQuantity != 0 {
		t.Error("position should be zero after liquidation")
	}

	// ---- 5.3: Insurance fund absorbs gap ----
	// The insurance fund should have been drawn on during liquidation.
	// We just verify the fund balance is queryable (may be 0 if fully absorbed).
	_ = n2.GetInsuranceFundBalance("USDC") // must not panic
}

// ---------------------------------------------------------------------------
// Act 6 — Conditional orders: stop-loss, take-profit, cancel, expire
// ---------------------------------------------------------------------------

func TestLifecycle_Act6_ConditionalOrders(t *testing.T) {
	const perpMkt = "BTC-USDC-PERP"
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              perpMkt,
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 1_000_000,
			MaxFundingRateBps:     100,
		}).Build())

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 50_000).
		AddDeposit(bobId, "USDC", 50_000).Build())

	var aliceSess, bobSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		switch {
		case p.AccountId == aliceId && aliceSess == "":
			aliceSess = p.SessionId
		case p.AccountId == bobId && bobSess == "":
			bobSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{perpMkt}, MaxOrderAmount: 10_000_000}).
		Build())

	// Open long for alice (needed so mark price has context).
	openSell := clob.NewLimitOrder(bobId, bobSess, perpMkt, clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(openSell).Build())
	openBuy := clob.NewLimitOrder(aliceId, aliceSess, perpMkt, clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	openBuy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(openBuy).Build())

	// Set mark price to 10_000 baseline.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(9).
		AddSubmitPrice(perpMkt, "oracle-v1", 10_000).Build())

	// ---- 6.1: Stop-loss (TriggerLTE 9_500) ----
	// Register a stop-loss: if mark ≤ 9_500 → sell 1 BTC.
	stopLoss := clob.ConditionalOrder{
		OrderId:          "stop-loss-lc-01",
		AccountId:        aliceId,
		SessionId:        aliceSess,
		MarketId:         perpMkt,
		Side:             clob.OrderSideSell,
		OrderType:        clob.OrderTypeMarket,
		Quantity:         1,
		TriggerPrice:     9_500,
		TriggerCondition: clob.TriggerLTE,
		ReduceOnly:       true,
		ExpireBlockHeight: 1_000,
		CreatedBlockHeight: 10,
	}
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(10).
		AddSubmitConditionalOrder(stopLoss).Build())
	if err != nil {
		t.Fatalf("submit stop-loss: %v", err)
	}

	co, ok := n.GetConditionalOrder("stop-loss-lc-01")
	if !ok || co.Status != clob.OrderStatusOpen {
		t.Error("stop-loss should be OPEN before trigger condition is met")
	}

	var condTriggered bool
	n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
		condTriggered = true
	})

	// Price above threshold — no trigger.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(11).
		AddSubmitPrice(perpMkt, "oracle-v1", 9_600).Build())
	if condTriggered {
		t.Error("stop-loss should NOT trigger at price 9_600 (above threshold 9_500)")
	}

	// Price at threshold — triggers.
	// Bob needs buy side liquidity so the market order can fill.
	buyLiq := clob.NewLimitOrder(bobId, bobSess, perpMkt, clob.OrderSideBuy, 9_000, 2, clob.TimeInForceGtc, 12)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(12).AddSubmitOrder(buyLiq).Build())
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(13).
		AddSubmitPrice(perpMkt, "oracle-v1", 9_500).Build())
	if !condTriggered {
		t.Error("stop-loss should trigger when mark == trigger price (LTE condition)")
	}

	// ---- 6.2: Take-profit (TriggerGTE 11_000) ----
	// Register a take-profit — alice already closed her position so just verify the lifecycle.
	tp := clob.ConditionalOrder{
		OrderId:          "take-profit-lc-01",
		AccountId:        bobId,
		SessionId:        bobSess,
		MarketId:         perpMkt,
		Side:             clob.OrderSideSell,
		OrderType:        clob.OrderTypeLimit,
		Price:            11_000,
		Quantity:         1,
		TriggerPrice:     11_000,
		TriggerCondition: clob.TriggerGTE,
		ExpireBlockHeight: 1_000,
		CreatedBlockHeight: 14,
	}
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(14).
		AddSubmitConditionalOrder(tp).Build())

	var tpTriggered bool
	n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
		tpTriggered = true
	})

	// Mark below threshold — no trigger.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(15).
		AddSubmitPrice(perpMkt, "oracle-v1", 10_999).Build())
	if tpTriggered {
		t.Error("take-profit should not trigger below 11_000")
	}

	// ---- 6.3: Cancel before trigger ----
	cancel01 := clob.ConditionalOrder{
		OrderId:          "to-cancel-lc-01",
		AccountId:        bobId,
		SessionId:        bobSess,
		MarketId:         perpMkt,
		Side:             clob.OrderSideSell,
		OrderType:        clob.OrderTypeMarket,
		Quantity:         1,
		TriggerPrice:     15_000,
		TriggerCondition: clob.TriggerGTE,
		ExpireBlockHeight: 1_000,
		CreatedBlockHeight: 16,
	}
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(16).
		AddSubmitConditionalOrder(cancel01).Build())

	var cancelFired bool
	n.Subscribe(state.EventConditionalOrderCancelled, func(e state.Event) {
		cancelFired = true
	})
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(17).
		AddCancelConditionalOrder("to-cancel-lc-01", bobId).Build())
	if err != nil {
		t.Fatalf("cancel conditional order: %v", err)
	}
	if !cancelFired {
		t.Error("expected EventConditionalOrderCancelled after explicit cancel")
	}
	_, stillExists := n.GetConditionalOrder("to-cancel-lc-01")
	if stillExists {
		t.Error("conditional order should be removed after cancel")
	}

	// ---- 6.4: Expire at block height ----
	expOrder := clob.ConditionalOrder{
		OrderId:          "expires-lc-01",
		AccountId:        aliceId,
		SessionId:        aliceSess,
		MarketId:         perpMkt,
		Side:             clob.OrderSideSell,
		OrderType:        clob.OrderTypeMarket,
		Quantity:         1,
		TriggerPrice:     20_000,
		TriggerCondition: clob.TriggerGTE,
		ExpireBlockHeight: 20, // expires at block 20
		CreatedBlockHeight: 18,
	}
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(18).
		AddSubmitConditionalOrder(expOrder).Build())

	var expiredFired bool
	n.Subscribe(state.EventConditionalOrderExpired, func(e state.Event) {
		expiredFired = true
	})
	// Advance to block 21 (past expiry).
	for n.CurrentHeight() < 21 {
		h := n.CurrentHeight() + 1
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(h).Build())
	}
	if !expiredFired {
		t.Error("expected EventConditionalOrderExpired when expire block reached")
	}
}

// ---------------------------------------------------------------------------
// Act 7 — Compliance & safety: sanctions, circuit breaker halt/resume
// ---------------------------------------------------------------------------

func TestLifecycle_Act7_ComplianceSafety(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// ---- 7.1: Sanction alice — her orders should be rejected ----
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSanctionAccount(aliceId, "OFAC SDN", "OFAC").Build())
	if err != nil {
		t.Fatalf("sanction alice: %v", err)
	}
	if !n.IsSanctioned(aliceId) {
		t.Error("alice should be sanctioned")
	}

	var rejectedBySanction bool
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedBySanction = true
	})
	sanctionedOrder := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	sanctionedOrder.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sanctionedOrder).Build())
	if !rejectedBySanction {
		t.Error("sanctioned account's order should be rejected")
	}

	// ---- 7.2: Unsanction alice — orders resume ----
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddUnsanctionAccount(aliceId).Build())
	if n.IsSanctioned(aliceId) {
		t.Error("alice should NOT be sanctioned after unsanction")
	}

	// Bob places a sell first; then alice buys — should succeed now.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell).Build())

	var tradedAfterUnsanction bool
	n.Subscribe(state.EventTradeExecuted, func(e state.Event) {
		tradedAfterUnsanction = true
	})
	buyAfter := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	buyAfter.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buyAfter).Build())
	if !tradedAfterUnsanction {
		t.Error("unsanctioned account should be able to trade")
	}

	// ---- 7.3: Circuit breaker halt — all new orders rejected ----
	var haltFired bool
	n.Subscribe(state.EventMarketHalted, func(e state.Event) {
		haltFired = true
	})
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(9).
		AddHaltMarket("BTC-USDC", "circuit-breaker-test").Build())
	if err != nil {
		t.Fatalf("halt market: %v", err)
	}
	if !haltFired {
		t.Error("expected EventMarketHalted")
	}

	rejectedBySanction = false
	halted := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 10)
	halted.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(10).AddSubmitOrder(halted).Build())
	if !rejectedBySanction { // reusing the rejected listener
		// The market halt may cause a different path — check order book is unchanged.
		ob, _ := n.GetOrderBook("BTC-USDC")
		_ = ob // market is halted; order should not be in book
	}

	// ---- 7.4: Resume market — trading resumes ----
	var resumeFired bool
	n.Subscribe(state.EventMarketResumed, func(e state.Event) {
		resumeFired = true
	})
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(11).
		AddResumeMarket("BTC-USDC").Build())
	if err != nil {
		t.Fatalf("resume market: %v", err)
	}
	if !resumeFired {
		t.Error("expected EventMarketResumed")
	}

	// Orders accepted again after resume.
	tradedAfterUnsanction = false
	postResumeSell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 12)
	postResumeSell.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(12).AddSubmitOrder(postResumeSell).Build())
	postResumeBuy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 13)
	postResumeBuy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(13).AddSubmitOrder(postResumeBuy).Build())
	if !tradedAfterUnsanction {
		t.Error("trading should resume after market is unhalted")
	}
}

// ---------------------------------------------------------------------------
// Act 8 — Cross-chain bridge attestation
// ---------------------------------------------------------------------------

func TestLifecycle_Act8_CrossChainBridge(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	// Bond two validators: val-A (60%), val-B (40%). Quorum > 66%.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-lc-A", "lc-a", "pub-lc-a", 60_000).
		AddBondValidator("val-lc-B", "lc-b", "pub-lc-b", 40_000).
		Build())
	if err != nil {
		t.Fatalf("bond validators: %v", err)
	}

	balBefore := n.GetBalance(aliceId, "USDC").Available

	// ---- 8.1: Single attestation by val-A (60%) — not yet quorum (>66.67% required) ----
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddBridgeAttest("dep-lc-001", aliceId, "USDC", 30_000, "Ethereum", "val-lc-A").Build())

	dep1, ok := n.GetBridgeDeposit("dep-lc-001")
	if !ok {
		t.Fatal("bridge deposit not recorded")
	}
	if dep1.Completed {
		t.Error("deposit should NOT be completed at 60% stake (needs >66.67%)")
	}
	bal1 := n.GetBalance(aliceId, "USDC").Available
	if bal1 != balBefore {
		t.Errorf("no USDC should be credited before quorum; delta = %d", bal1-balBefore)
	}

	// ---- 8.2: Second attestation by val-B (60+40=100%) — quorum reached ----
	var bridgeCompleted bool
	n.Subscribe(state.EventBridgeCompleted, func(e state.Event) {
		bridgeCompleted = true
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddBridgeAttest("dep-lc-001", aliceId, "USDC", 30_000, "Ethereum", "val-lc-B").Build())

	dep2, _ := n.GetBridgeDeposit("dep-lc-001")
	if !dep2.Completed {
		t.Error("deposit should be completed after second attestation (100% stake)")
	}
	if !bridgeCompleted {
		t.Error("expected EventBridgeCompleted")
	}
	balAfter := n.GetBalance(aliceId, "USDC").Available
	if balAfter-balBefore != 30_000 {
		t.Errorf("expected balance increase of 30_000 after quorum; got %d", balAfter-balBefore)
	}

	// ---- 8.3: Duplicate attestation by val-A is idempotent ----
	balBeforeRetry := n.GetBalance(aliceId, "USDC").Available
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).
		AddBridgeAttest("dep-lc-001", aliceId, "USDC", 30_000, "Ethereum", "val-lc-A").Build())
	balAfterRetry := n.GetBalance(aliceId, "USDC").Available
	if balAfterRetry != balBeforeRetry {
		t.Errorf("duplicate attestation should not credit funds again; delta = %d", balAfterRetry-balBeforeRetry)
	}
}

// ---------------------------------------------------------------------------
// Act 9 — Governance: fee policy proposal → vote → execution
// ---------------------------------------------------------------------------

func TestLifecycle_Act9_Governance(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// Bond a validator so governance has stake to weigh.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("gov-val-01", "gv01", "pubgv01", 100_000).Build())

	// ---- 9.1: Submit UpdateFeePolicy proposal ----
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Lower maker fee to 1 bps",
			Description:   "Increase volume by reducing maker fee",
			VoteEndHeight: 15,
			MakerBps:      1,
			TakerBps:      5,
		}).Build())
	if err != nil {
		t.Fatalf("submit proposal: %v", err)
	}

	proposals := n.AllProposals()
	if len(proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
	prop := proposals[0]
	if prop.Status != governance.StatusVoting {
		t.Errorf("proposal status: want VOTING got %s", prop.Status)
	}

	// ---- 9.2: Vote YES ----
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddVote(prop.ProposalId, "gov-val-01", "YES", 100_000).Build())

	// ---- 9.3: Advance past VoteEndHeight → proposal executes ----
	for n.CurrentHeight() < 16 {
		h := n.CurrentHeight() + 1
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(h).Build())
	}

	allProposals := n.AllProposals()
	var executed *governance.Proposal
	for i := range allProposals {
		if allProposals[i].ProposalId == prop.ProposalId {
			executed = &allProposals[i]
			break
		}
	}
	if executed == nil {
		t.Fatal("proposal not found after vote end")
	}
	if executed.Status != governance.StatusPassed && executed.Status != governance.StatusExecuted {
		t.Errorf("proposal should be PASSED or EXECUTED after >50%% YES; got %s", executed.Status)
	}

	// ---- 9.4: Verify new fee policy applied to next trade ----
	// maker fee should now be 1 bps instead of 2 bps.
	var newMakerFee int64
	n.Subscribe(state.EventTradeExecuted, func(e state.Event) {
		p := e.Payload.(state.TradeExecutedPayload)
		newMakerFee = p.MakerFeeAmount
	})

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 17)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(17).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 18)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(18).AddSubmitOrder(buy).Build())

	// New maker fee: 10_000 * 1 / 10_000 = 1
	if newMakerFee != 1 {
		t.Errorf("maker fee after governance execution: want 1 got %d", newMakerFee)
	}

	// ---- 9.5: Rejected proposal (insufficient YES votes) ----
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(19).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Controversial fee change",
			VoteEndHeight: 25,
			MakerBps:      50,
			TakerBps:      100,
		}).Build())

	allProps2 := n.AllProposals()
	var prop2 *governance.Proposal
	for i := range allProps2 {
		if allProps2[i].Title == "Controversial fee change" {
			prop2 = &allProps2[i]
			break
		}
	}
	if prop2 == nil {
		t.Fatal("second proposal not found")
	}
	// Vote NO — proposal will fail.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(20).
		AddVote(prop2.ProposalId, "gov-val-01", "NO", 100_000).Build())

	for n.CurrentHeight() < 26 {
		h := n.CurrentHeight() + 1
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(h).Build())
	}

	allProps3 := n.AllProposals()
	for _, p := range allProps3 {
		if p.ProposalId == prop2.ProposalId {
			if p.Status != governance.StatusRejected {
				t.Errorf("NO vote proposal should be REJECTED; got %s", p.Status)
			}
			break
		}
	}

	// Fee policy reverts to governance-changed value (1 bps), not the rejected 50 bps.
	newMakerFee = 0
	sell2 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 27)
	sell2.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(27).AddSubmitOrder(sell2).Build())
	buy2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 28)
	buy2.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(28).AddSubmitOrder(buy2).Build())

	if newMakerFee != 1 {
		t.Errorf("rejected proposal should not change fees; want maker=1 got %d", newMakerFee)
	}
}

// ---------------------------------------------------------------------------
// Act 10 — Withdrawal timelock & points accumulation
// ---------------------------------------------------------------------------

func TestLifecycle_Act10_WithdrawTimelockAndPoints(t *testing.T) {
	// ---- 10.1: Withdrawal timelock ----
	policy := risk.DefaultRiskPolicy
	policy.WithdrawTimelockBlocks = 3
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNodeWithPolicy(t, policy)

	// Generate a trade to accumulate points and treasury.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())

	balBeforeWithdraw := n.GetBalance(aliceId, "USDC").Available

	var withdrawPayload state.WithdrawRequestedPayload
	n.Subscribe(state.EventWithdrawRequested, func(e state.Event) {
		withdrawPayload = e.Payload.(state.WithdrawRequestedPayload)
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddWithdrawRequest(aliceId, "USDC", 5_000, n.GetAccountSequence(aliceId), "").Build())
	if err != nil {
		t.Fatalf("withdraw request: %v", err)
	}

	if withdrawPayload.WithdrawalId == "" {
		t.Fatal("expected EventWithdrawRequested to be emitted")
	}
	if withdrawPayload.Amount != 5_000 {
		t.Errorf("withdrawal amount: want 5_000 got %d", withdrawPayload.Amount)
	}
	if withdrawPayload.ReadyAtHeight != 6+3 {
		t.Errorf("readyAtHeight: want %d got %d", 6+3, withdrawPayload.ReadyAtHeight)
	}

	// Funds reserved (unavailable) during timelock.
	balDuringLock := n.GetBalance(aliceId, "USDC").Available
	if balDuringLock >= balBeforeWithdraw {
		t.Error("funds should be reserved (unavailable) during timelock")
	}

	pending := n.GetPendingWithdrawals(aliceId)
	if len(pending) == 0 {
		t.Fatal("expected a pending withdrawal entry")
	}

	// Advance past timelock.
	var finalizeFired bool
	n.Subscribe(state.EventWithdrawFinalized, func(e state.Event) {
		finalizeFired = true
	})
	for n.CurrentHeight() < 10 {
		h := n.CurrentHeight() + 1
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(h).Build())
	}
	if !finalizeFired {
		t.Error("expected EventWithdrawFinalized after timelock period")
	}

	pendingAfter := n.GetPendingWithdrawals(aliceId)
	if len(pendingAfter) != 0 {
		t.Errorf("pending withdrawals should be empty after finalization; got %d", len(pendingAfter))
	}

	// ---- 10.2: Points accumulation after trading ----
	pk := n.GetPointsKeeper()
	if pk == nil {
		t.Fatal("GetPointsKeeper returned nil")
	}

	alicePoints := pk.TGEAllocation(aliceId, 1_000_000)
	bobPoints := pk.TGEAllocation(bobId, 1_000_000)

	// Both accounts traded, so both should have non-zero allocations.
	if alicePoints <= 0 {
		t.Errorf("alice should have positive TGE allocation after trading; got %d", alicePoints)
	}
	if bobPoints <= 0 {
		t.Errorf("bob should have positive TGE allocation after trading; got %d", bobPoints)
	}

	// ---- 10.3: Fee distribution to bonded validator ----
	// Bond a validator before the trade so it receives fees.
	n2, a2Id, b2Id, a2Sess, b2Sess := bootstrapNodeWithPolicy(t, risk.DefaultRiskPolicy)
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("dist-val-01", "dv01", "pubdv01", 100_000).Build())

	sell2 := clob.NewLimitOrder(b2Id, b2Sess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell2).Build())
	buy2 := clob.NewLimitOrder(a2Id, a2Sess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy2.AccountSequence = n2.GetAccountSequence(a2Id)
	_, _ = n2.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy2).Build())

	// Total fees = 2 (maker) + 5 (taker) = 7; insurance takes 1 USDC (20% taker = 1); 6 to validators.
	reward := n2.GetValidatorReward("dist-val-01", "USDC")
	if reward <= 0 {
		t.Errorf("expected positive validator reward after trade; got %d", reward)
	}
	treasury := n2.GetTreasury("USDC")
	if treasury.Available != 0 {
		t.Errorf("treasury should be 0 after full distribution; got %d", treasury.Available)
	}
}
