package scenario_test

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/token"
)

// setupPointsNode creates a fresh node with BTC/USDC registered, creates two accounts
// with sessions and initial balances, and returns the node and account/session IDs.
func setupPointsNode(t *testing.T) (n *node.LocalNode, aliceId, bobId, aliceSession, bobSession string) {
	t.Helper()
	n = node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	// Collect events to extract account/session IDs.
	var evts []state.Event
	n.Subscribe(state.EventAll, func(e state.Event) {
		evts = append(evts, e)
	})

	// Block 1: create accounts
	batch1 := fairbatch.NewBatchBuilder(1).
		AddCreateAccount("alice@points.test", "alice-root", "alice-wd").
		AddCreateAccount("bob@points.test", "bob-root", "bob-wd").
		Build()
	if _, err := n.SubmitBatch(batch1); err != nil {
		t.Fatalf("create accounts: %v", err)
	}

	// Extract account IDs from events.
	for _, e := range evts {
		if e.Type == state.EventAccountCreated {
			p := e.Payload.(state.AccountCreatedPayload)
			switch p.OwnerAddress {
			case "alice@points.test":
				aliceId = p.AccountId
			case "bob@points.test":
				bobId = p.AccountId
			}
		}
	}
	if aliceId == "" || bobId == "" {
		t.Fatal("could not determine alice/bob account IDs")
	}

	// Block 2: create sessions
	evts = evts[:0]
	batch2 := fairbatch.NewBatchBuilder(2).
		AddCreateSession(aliceId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC"},
			MaxOrderAmount: 1_000_000,
			MaxDailyVolume: 1_000_000_000,
		}).
		AddCreateSession(bobId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC"},
			MaxOrderAmount: 1_000_000,
			MaxDailyVolume: 1_000_000_000,
		}).
		Build()
	if _, err := n.SubmitBatch(batch2); err != nil {
		t.Fatalf("create sessions: %v", err)
	}
	for _, e := range evts {
		if e.Type == state.EventSessionCreated {
			p := e.Payload.(state.SessionCreatedPayload)
			switch p.AccountId {
			case aliceId:
				aliceSession = p.SessionId
			case bobId:
				bobSession = p.SessionId
			}
		}
	}
	if aliceSession == "" || bobSession == "" {
		t.Fatal("could not determine alice/bob session IDs")
	}

	// Block 3: deposit funds
	batch3 := fairbatch.NewBatchBuilder(3).
		AddDeposit(aliceId, "USDC", 1_000_000).
		AddDeposit(bobId, "BTC", 1_000).
		Build()
	if _, err := n.SubmitBatch(batch3); err != nil {
		t.Fatalf("deposit funds: %v", err)
	}

	return n, aliceId, bobId, aliceSession, bobSession
}

// submitTrade places a maker (resting) order from seller and a taker (crossing) order
// from buyer on BTC-USDC, at the given price and qty. Returns the block result.
func submitTrade(t *testing.T, n *node.LocalNode, sellerId, sellerSession, buyerId, buyerSession string, price, qty, blockHeight int64) {
	t.Helper()

	// Maker (resting sell)
	sellOrder := clob.NewLimitOrder(sellerId, sellerSession, "BTC-USDC",
		clob.OrderSideSell, price, qty, clob.TimeInForceGtc, blockHeight)
	sellOrder.AccountSequence = n.GetAccountSequence(sellerId)
	batch1 := fairbatch.NewBatchBuilder(blockHeight).AddSubmitOrder(sellOrder).Build()
	if _, err := n.SubmitBatch(batch1); err != nil {
		t.Fatalf("submit sell order block %d: %v", blockHeight, err)
	}

	// Taker (crossing buy)
	buyOrder := clob.NewLimitOrder(buyerId, buyerSession, "BTC-USDC",
		clob.OrderSideBuy, price, qty, clob.TimeInForceGtc, blockHeight+1)
	buyOrder.AccountSequence = n.GetAccountSequence(buyerId)
	batch2 := fairbatch.NewBatchBuilder(blockHeight + 1).AddSubmitOrder(buyOrder).Build()
	result, err := n.SubmitBatch(batch2)
	if err != nil {
		t.Fatalf("submit buy order block %d: %v", blockHeight+1, err)
	}
	if result.TradeCount == 0 {
		t.Fatalf("expected trade to execute at block %d, got 0 trades", blockHeight+1)
	}
}

// TestPoints_TradeEarnsPoints verifies that after a SPOT trade both the maker (seller)
// and the taker (buyer) receive non-zero trade points.
func TestPoints_TradeEarnsPoints(t *testing.T) {
	n, aliceId, bobId, aliceSession, bobSession := setupPointsNode(t)

	// Bob sells (maker), Alice buys (taker). notional = 10_000 * 5 = 50_000.
	submitTrade(t, n, bobId, bobSession, aliceId, aliceSession, 10_000, 5, 4)

	bobPts := n.GetAccountPoints(bobId)
	alicePts := n.GetAccountPoints(aliceId)

	if bobPts == nil || bobPts.TradePoints == 0 {
		t.Errorf("maker (bob) should have earned trade points, got %v", bobPts)
	}
	if alicePts == nil || alicePts.TradePoints == 0 {
		t.Errorf("taker (alice) should have earned trade points, got %v", alicePts)
	}

	// Maker gets 1.5x boost over taker for the same notional (no early-bird here, all
	// accounts registered before limit so both may be early-bird; just check maker > taker).
	// Both are early-bird so compare raw ratio: maker = notional * 3/2, taker = notional * 1.
	// After early-bird 2x: maker_pts = notional * 3/2 * 2, taker_pts = notional * 1 * 2.
	// Maker > Taker always.
	if bobPts.TradePoints <= alicePts.TradePoints {
		t.Errorf("maker points (%d) should exceed taker points (%d)", bobPts.TradePoints, alicePts.TradePoints)
	}
}

// TestPoints_PerpBonus verifies that a PERP trade earns 2x points vs a SPOT trade
// of equivalent notional.
func TestPoints_PerpBonus(t *testing.T) {
	const notional int64 = 100_000

	// Unit-level check using the local helper that mirrors points.TradePoints logic.
	spotPts := computeExpectedTradePoints(notional, false, false)
	perpPts := computeExpectedTradePoints(notional, true, false)
	if perpPts != 2*spotPts {
		t.Errorf("PERP points (%d) should be 2x SPOT points (%d)", perpPts, spotPts)
	}

	// Integration check using the PointsKeeper with two registered accounts.
	n, aliceId, bobId, _, _ := setupPointsNode(t)

	pk := n.GetPointsKeeper()
	if pk == nil {
		t.Fatal("PointsKeeper is nil")
	}

	// Snapshot trade points before recording.
	aliceBefore := n.GetAccountPoints(aliceId)
	bobBefore := n.GetAccountPoints(bobId)
	var aliceTradeBefore, bobTradeBefore int64
	if aliceBefore != nil {
		aliceTradeBefore = aliceBefore.TradePoints
	}
	if bobBefore != nil {
		bobTradeBefore = bobBefore.TradePoints
	}

	pk.RecordTrade(aliceId, notional, false, false) // SPOT taker
	pk.RecordTrade(bobId, notional, true, false)    // PERP taker

	aliceAfter := n.GetAccountPoints(aliceId)
	bobAfter := n.GetAccountPoints(bobId)

	aliceSpotGain := aliceAfter.TradePoints - aliceTradeBefore
	bobPerpGain := bobAfter.TradePoints - bobTradeBefore

	// After any early-bird multiplier both are multiplied equally, so ratio holds.
	if bobPerpGain != 2*aliceSpotGain {
		t.Errorf("PERP gain (%d) should be 2x SPOT gain (%d)", bobPerpGain, aliceSpotGain)
	}
}

// TestPoints_ReferralChain verifies that when Alice refers Bob, Bob's trades
// cause Alice to receive 10% referral points.
func TestPoints_ReferralChain(t *testing.T) {
	n, aliceId, bobId, _, bobSession := setupPointsNode(t)

	// Also need alice to be the seller so we need a third account for the trade.
	// Instead, let's just use SetReferrer and RecordTrade via the keeper directly.
	pk := n.GetPointsKeeper()
	if pk == nil {
		t.Fatal("PointsKeeper is nil")
	}

	// Alice refers Bob.
	n.SetReferrer(bobId, aliceId)

	// Verify referrer is set
	bobPts := n.GetAccountPoints(bobId)
	if bobPts == nil || bobPts.ReferrerId != aliceId {
		t.Fatalf("bob's referrer should be alice (%s), got %v", aliceId, bobPts)
	}

	alicePtsBefore := n.GetAccountPoints(aliceId)
	var aliceRefBefore int64
	if alicePtsBefore != nil {
		aliceRefBefore = alicePtsBefore.ReferralPoints
	}

	// Bob executes a SPOT taker trade for notional = 50_000.
	const notional int64 = 50_000
	pk.RecordTrade(bobId, notional, false, false)

	// Bob's trade points (with early-bird 2x): 50_000 * 1 * 2 = 100_000
	// Alice's referral points: 10% of bob's earned pts.
	bobAfter := n.GetAccountPoints(bobId)
	aliceAfter := n.GetAccountPoints(aliceId)

	bobTradeGain := bobAfter.TradePoints
	aliceRefGain := aliceAfter.ReferralPoints - aliceRefBefore

	expectedRef := bobTradeGain * 10 / 100
	if aliceRefGain != expectedRef {
		t.Errorf("alice referral gain: want %d (10%% of bob's %d), got %d",
			expectedRef, bobTradeGain, aliceRefGain)
	}
	if aliceRefGain == 0 {
		t.Error("alice should have received non-zero referral points")
	}
	_ = bobSession
}

// TestPoints_TGEAllocation verifies that two accounts with a 3:1 points ratio receive
// FAIR allocations in the same 3:1 ratio from the community pool.
func TestPoints_TGEAllocation(t *testing.T) {
	n, aliceId, bobId, _, _ := setupPointsNode(t)

	pk := n.GetPointsKeeper()
	if pk == nil {
		t.Fatal("PointsKeeper is nil")
	}

	// Award alice 3x more points than bob using direct keeper calls.
	// notional: alice = 300, bob = 100 (both SPOT taker, no early-bird difference matters
	// as long as we compare the ratio).
	// We reset by using specific amounts.
	pk.RecordTrade(aliceId, 300, false, false) // alice earns 300 * 1 (+ earlybird)
	pk.RecordTrade(bobId, 100, false, false)   // bob earns 100 * 1 (+ earlybird)

	alicePts := n.GetAccountPoints(aliceId)
	bobPts := n.GetAccountPoints(bobId)

	if alicePts == nil || bobPts == nil {
		t.Fatal("expected non-nil points for both accounts")
	}

	// Both are early-birds, so the 2x multiplier applies equally.
	// alice = 600, bob = 200. Total = 800.
	// alice share = 600/800 = 3/4; bob share = 200/800 = 1/4.
	// The ratio alice:bob should be 3:1.
	if alicePts.TotalPoints == 0 || bobPts.TotalPoints == 0 {
		t.Fatalf("expected non-zero points: alice=%d bob=%d", alicePts.TotalPoints, bobPts.TotalPoints)
	}

	// Verify ratio is exactly 3:1
	ratio := alicePts.TotalPoints / bobPts.TotalPoints
	if ratio != 3 {
		t.Errorf("expected alice:bob points ratio of 3:1, got alice=%d bob=%d (ratio=%d)",
			alicePts.TotalPoints, bobPts.TotalPoints, ratio)
	}

	// TGE allocation
	totalFAIR := token.CommunityAlloc
	aliceFAIR := n.TGEAllocation(aliceId, totalFAIR)
	bobFAIR := n.TGEAllocation(bobId, totalFAIR)

	if aliceFAIR == 0 || bobFAIR == 0 {
		t.Fatalf("expected non-zero FAIR allocation: alice=%d bob=%d", aliceFAIR, bobFAIR)
	}

	// alice should get 3x more FAIR than bob.
	if aliceFAIR/bobFAIR != 3 {
		t.Errorf("expected alice FAIR (%d) to be 3x bob FAIR (%d), ratio=%d",
			aliceFAIR, bobFAIR, aliceFAIR/bobFAIR)
	}

	// Total should not exceed community alloc (integer division means it may be slightly less).
	if aliceFAIR+bobFAIR > totalFAIR {
		t.Errorf("combined allocation (%d) exceeds community alloc (%d)",
			aliceFAIR+bobFAIR, totalFAIR)
	}
}

// computeExpectedTradePoints is a local helper mirroring the points.TradePoints logic.
func computeExpectedTradePoints(notional int64, isPerp, isMaker bool) int64 {
	pts := notional // PointsPerNotional = 1
	if isPerp {
		pts = pts * 2 / 1 // PerpBoostNumerator=2, PerpBoostDenominator=1
	}
	if isMaker {
		pts = pts * 3 / 2 // MakerBoostNum=3, MakerBoostDen=2
	}
	return pts
}
