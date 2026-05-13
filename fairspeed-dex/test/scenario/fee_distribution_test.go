package scenario_test

// Stage 19: Fee distribution to validators tests.
//
// Verifies:
//  1. Single bonded validator receives full treasury fee after a trade
//  2. Two validators split fees proportional to stake
//  3. No distribution when treasury is empty
//  4. EventFeeDistributed emitted per validator with correct amount
//  5. Rounding: remainder goes to last validator (no fee is lost)
//  6. Distribution resets treasury to 0 after each block

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// -------------------------------------------------------------------------
// Scenario 1: Single validator receives full treasury fee
// -------------------------------------------------------------------------
func TestFeeDistribution_SingleValidator(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// Bond a validator.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-fd-01", "fd-01", "pubfd01", 100_000).Build())

	// Generate a trade: Bob sells, Alice buys at price=10_000 qty=1
	// makerFee = 10_000 * 2 / 10_000 = 2; takerFee = 10_000 * 5 / 10_000 = 5
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

	// At end of block 6, fees (2+5=7 USDC) should be distributed to val-fd-01.
	reward := n.GetValidatorReward("val-fd-01", "USDC")
	if reward <= 0 {
		t.Errorf("expected validator reward > 0, got %d", reward)
	}
	// Treasury should be 0 (all distributed).
	treasury := n.GetTreasury("USDC")
	if treasury.Available != 0 {
		t.Errorf("treasury should be 0 after distribution, got %d", treasury.Available)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Two validators split fees proportionally to stake
// -------------------------------------------------------------------------
func TestFeeDistribution_TwoValidatorsSplit(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// val-a: 30_000 stake, val-b: 70_000 stake → total = 100_000
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-a", "a", "puba", 30_000).
		AddBondValidator("val-b", "b", "pubb", 70_000).Build())

	// Trade: price=10_000 qty=1 → fees = 7 USDC total
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

	rwdA := n.GetValidatorReward("val-a", "USDC")
	rwdB := n.GetValidatorReward("val-b", "USDC")

	// Treasury = makerFee(2) + takerFee - insuranceShare(1) = 6 USDC.
	// val-a gets 30%: 6 * 30_000/100_000 = 1; val-b (last) gets 6-1 = 5.
	if rwdA+rwdB != 6 {
		t.Errorf("total reward should equal 6, got a=%d b=%d total=%d", rwdA, rwdB, rwdA+rwdB)
	}
	if rwdA == 0 || rwdB == 0 {
		t.Errorf("both validators should receive non-zero reward: a=%d b=%d", rwdA, rwdB)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: No distribution when treasury is empty
// -------------------------------------------------------------------------
func TestFeeDistribution_EmptyTreasury(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-empty", "e", "pube", 50_000).Build())

	var distributed int
	n.Subscribe(state.EventFeeDistributed, func(e state.Event) {
		distributed++
	})

	// Advance a block with no trades → treasury = 0 → no distribution events.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).Build())

	if distributed != 0 {
		t.Errorf("expected 0 distributions with empty treasury, got %d", distributed)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: EventFeeDistributed emitted with correct fields
// -------------------------------------------------------------------------
func TestFeeDistribution_EventPayload(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-ev", "ev", "pubev", 100_000).Build())

	var evPayload state.FeeDistributedPayload
	n.Subscribe(state.EventFeeDistributed, func(e state.Event) {
		evPayload = e.Payload.(state.FeeDistributedPayload)
	})

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

	if evPayload.ValidatorId != "val-ev" {
		t.Errorf("validatorId: want val-ev got %s", evPayload.ValidatorId)
	}
	if evPayload.AssetId != "USDC" {
		t.Errorf("assetId: want USDC got %s", evPayload.AssetId)
	}
	if evPayload.Amount <= 0 {
		t.Errorf("amount should be > 0, got %d", evPayload.Amount)
	}
	if evPayload.BlockHeight == 0 {
		t.Error("blockHeight should be populated")
	}
}

// -------------------------------------------------------------------------
// Scenario 5: No fees lost to rounding (total = sum of shares)
// -------------------------------------------------------------------------
func TestFeeDistribution_NoFeesLostToRounding(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// 3 validators with unequal stakes to create rounding.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-r1", "r1", "pubr1", 33_333).
		AddBondValidator("val-r2", "r2", "pubr2", 33_333).
		AddBondValidator("val-r3", "r3", "pubr3", 33_334).Build())

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

	total := n.GetValidatorReward("val-r1", "USDC") +
		n.GetValidatorReward("val-r2", "USDC") +
		n.GetValidatorReward("val-r3", "USDC")

	// Treasury = 6 USDC (makerFee=2 + takerFee net of insurance=4).
	// All 6 must be distributed with no rounding loss.
	if total != 6 {
		t.Errorf("expected total distributed = 6, got %d", total)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Treasury resets to 0 after each block distribution
// -------------------------------------------------------------------------
func TestFeeDistribution_TreasuryResetsEachBlock(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator("val-tr", "tr", "pubtr", 100_000).Build())

	// First trade in block 5.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

	// After block 6: treasury should be 0.
	if n.GetTreasury("USDC").Available != 0 {
		t.Errorf("treasury should be 0 after distribution, got %d", n.GetTreasury("USDC").Available)
	}
}
