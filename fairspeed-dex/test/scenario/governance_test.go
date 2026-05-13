package scenario_test

// Stage 7: On-chain Governance tests.
//
// Verifies:
//  1. TxSubmitProposal creates a proposal in VOTING state
//  2. TxVote tallies stake-weighted votes correctly
//  3. Proposal with >50% YES passes and executes at VoteEndHeight
//  4. Proposal with ≤50% YES is rejected at VoteEndHeight
//  5. UpdateFeePolicy execution changes fees applied to future trades
//  6. UpdateRiskPolicy execution changes order quantity limits

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// -------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------

// firstProposal returns the first proposal from the node (panics if none).
func firstProposal(t *testing.T, n interface{ AllProposals() []governance.Proposal }) governance.Proposal {
	t.Helper()
	all := n.AllProposals()
	if len(all) == 0 {
		t.Fatal("expected at least 1 proposal, got 0")
	}
	return all[0]
}

// -------------------------------------------------------------------------
// Scenario 1: SubmitProposal → VOTING state
// -------------------------------------------------------------------------
func TestGovernance_SubmitProposal_IsVoting(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Raise fees",
			Description:   "Increase maker to 10 bps",
			VoteEndHeight: 10,
			MakerBps:      10,
			TakerBps:      20,
		}).
		Build())
	if err != nil {
		t.Fatalf("submit proposal: %v", err)
	}

	p := firstProposal(t, n)
	if p.Status != governance.StatusVoting {
		t.Errorf("proposal status: want VOTING got %s", p.Status)
	}
	if p.VoteEndHeight != 10 {
		t.Errorf("vote end height: want 10 got %d", p.VoteEndHeight)
	}
	if p.Type != governance.TypeUpdateFeePolicy {
		t.Errorf("proposal type: want UpdateFeePolicy got %s", p.Type)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Vote tallied with correct stake weight
// -------------------------------------------------------------------------
func TestGovernance_Vote_TalliedCorrectly(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	// Submit proposal with voteEndHeight=20 (far enough that it won't tally yet).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Fee test",
			VoteEndHeight: 20,
			MakerBps:      10,
			TakerBps:      20,
		}).
		Build())

	p := firstProposal(t, n)

	// Vote YES with stake=50_000.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddVote(p.ProposalId, "val-alpha", "YES", 50_000).
		Build())
	if err != nil {
		t.Fatalf("vote: %v", err)
	}
	// Vote NO with stake=30_000.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddVote(p.ProposalId, "val-beta", "NO", 30_000).
		Build())

	updated, ok := n.GetProposal(p.ProposalId)
	if !ok {
		t.Fatal("proposal not found")
	}
	if updated.TallyYes != 50_000 {
		t.Errorf("tally YES: want 50000 got %d", updated.TallyYes)
	}
	if updated.TallyNo != 30_000 {
		t.Errorf("tally NO: want 30000 got %d", updated.TallyNo)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Proposal passes with majority → status EXECUTED
// -------------------------------------------------------------------------
func TestGovernance_Proposal_PassesWithMajority(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	// VoteEndHeight=6 (bootstrapNode leaves height at 3, so we need 3 more blocks).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Fee proposal",
			VoteEndHeight: 6,
			MakerBps:      10,
			TakerBps:      20,
		}).
		Build())

	p := firstProposal(t, n)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddVote(p.ProposalId, "val-alpha", "YES", 80_000).
		AddVote(p.ProposalId, "val-beta", "NO", 10_000).
		Build())

	var passedId, executedId string
	n.Subscribe(state.EventProposalPassed, func(e state.Event) {
		passedId = e.Payload.(state.ProposalPassedPayload).ProposalId
	})
	n.Subscribe(state.EventProposalExecuted, func(e state.Event) {
		executedId = e.Payload.(state.ProposalExecutedPayload).ProposalId
	})

	// Submit an empty block at height 6 to trigger tally.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(6).Build())
	if err != nil {
		t.Fatalf("tally block: %v", err)
	}

	if passedId != p.ProposalId {
		t.Errorf("expected ProposalPassed event, got %q", passedId)
	}
	if executedId != p.ProposalId {
		t.Errorf("expected ProposalExecuted event, got %q", executedId)
	}
	updated, _ := n.GetProposal(p.ProposalId)
	if updated.Status != governance.StatusExecuted {
		t.Errorf("proposal status after pass: want EXECUTED got %s", updated.Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Proposal rejected with minority YES votes
// -------------------------------------------------------------------------
func TestGovernance_Proposal_RejectedWithMinority(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Rejected proposal",
			VoteEndHeight: 6,
			MakerBps:      100,
			TakerBps:      200,
		}).
		Build())

	p := firstProposal(t, n)

	// Vote 30% YES, 70% NO — should not reach majority.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddVote(p.ProposalId, "val-alpha", "YES", 30_000).
		AddVote(p.ProposalId, "val-beta", "NO", 70_000).
		Build())

	var rejectedId string
	n.Subscribe(state.EventProposalRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.ProposalRejectedPayload).ProposalId
	})

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).Build())

	if rejectedId != p.ProposalId {
		t.Errorf("expected ProposalRejected event for %s, got %q", p.ProposalId, rejectedId)
	}
	updated, _ := n.GetProposal(p.ProposalId)
	if updated.Status != governance.StatusRejected {
		t.Errorf("proposal status: want REJECTED got %s", updated.Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: UpdateFeePolicy execution changes trade fees
// -------------------------------------------------------------------------
func TestGovernance_FeePolicy_Execution_ChangesFees(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// Submit fee update: MakerBps=10, TakerBps=20 (vs defaults 2 and 5).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "New fees",
			VoteEndHeight: 6,
			MakerBps:      10,
			TakerBps:      20,
		}).
		Build())

	p := firstProposal(t, n)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddVote(p.ProposalId, "val-alpha", "YES", 100_000).
		Build())

	// Tally block — fees update takes effect here.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).Build())

	// Now execute a trade and check new fees.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell).Build())

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("trade after fee update: %v", err)
	}
	if res.TradeCount != 1 {
		t.Fatalf("expected 1 trade, got %d", res.TradeCount)
	}

	trade := res.Trades[0]
	// notional = 10_000 * 1 = 10_000
	// makerFee = 10_000 * 10 / 10_000 = 10
	// takerFee = 10_000 * 20 / 10_000 = 20
	if trade.MakerFeeAmount != 10 {
		t.Errorf("makerFee after governance update: want 10 got %d", trade.MakerFeeAmount)
	}
	if trade.TakerFeeAmount != 20 {
		t.Errorf("takerFee after governance update: want 20 got %d", trade.TakerFeeAmount)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: UpdateRiskPolicy execution enforces new quantity limit
// -------------------------------------------------------------------------
func TestGovernance_RiskPolicy_Execution_ChangesOrderLimit(t *testing.T) {
	n, _, bobId, _, bobSess := bootstrapNode(t)

	// Propose reducing MaxOrderQuantity to 5 (current default is 1_000_000).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:     "UpdateRiskPolicy",
			Title:            "Lower order limit",
			VoteEndHeight:    6,
			MaxOrderQuantity: 5,
			MinOrderQuantity: 1,
			MaxDailyVolumePerSession: 100_000_000,
		}).
		Build())

	p := firstProposal(t, n)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddVote(p.ProposalId, "val-alpha", "YES", 100_000).
		Build())
	// Execute the policy change.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).Build())

	// Now try to submit an order with qty=10 — should be rejected (limit=5).
	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	overLimit := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 10, clob.TimeInForceGtc, 7)
	overLimit.AccountSequence = n.GetAccountSequence(bobId)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(overLimit).Build())
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rejectedId == "" {
		t.Error("expected ORDER_REJECTED for quantity exceeding new governance limit, got none")
	}
}
