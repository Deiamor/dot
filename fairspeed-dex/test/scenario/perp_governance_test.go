package scenario_test

// Governance: UpdatePerpConfig proposal type.
//
// Verifies that a passed UpdatePerpConfig proposal changes PERP market
// parameters (MaxLeverage, InitialMarginBps, etc.) while leaving unspecified
// fields unchanged.

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

func TestGovernance_PerpConfig_Execution_ChangesMarginParams(t *testing.T) {
	n := node.NewLocalNode()

	// Register PERP market.
	if _, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
		MarketId:              "BTC-USDC-PERP",
		BaseAsset:             "BTC",
		QuoteAsset:            "USDC",
		InitialMarginBps:      1000,
		MaintenanceMarginBps:  500,
		MaxLeverage:           10,
		FundingIntervalBlocks: 100,
		MaxFundingRateBps:     200,
	}).Build()); err != nil {
		t.Fatalf("register perp market: %v", err)
	}

	cfgBefore, ok := n.GetPerpConfig("BTC-USDC-PERP")
	if !ok {
		t.Fatal("PERP market not registered")
	}
	if cfgBefore.MaxLeverage != 10 {
		t.Fatalf("expected initial MaxLeverage=10, got %d", cfgBefore.MaxLeverage)
	}

	// Propose raising leverage to 20.
	if _, err := n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdatePerpConfig",
			Title:         "Raise leverage cap",
			VoteEndHeight: 4,
			MarketId:      "BTC-USDC-PERP",
			MaxLeverage:   20,
		}).Build()); err != nil {
		t.Fatalf("submit proposal: %v", err)
	}

	proposals := n.AllProposals()
	if len(proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
	propId := proposals[0].ProposalId

	if _, err := n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddVote(propId, "val-alpha", "YES", 100_000).
		Build()); err != nil {
		t.Fatalf("cast vote: %v", err)
	}

	// Tally block — config update executes here.
	if _, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).Build()); err != nil {
		t.Fatalf("tally block: %v", err)
	}

	cfgAfter, ok := n.GetPerpConfig("BTC-USDC-PERP")
	if !ok {
		t.Fatal("PERP market missing after governance execution")
	}
	if cfgAfter.MaxLeverage != 20 {
		t.Errorf("expected MaxLeverage=20 after governance, got %d", cfgAfter.MaxLeverage)
	}
	// Other fields should remain unchanged.
	if cfgAfter.InitialMarginBps != 1000 {
		t.Errorf("InitialMarginBps changed unexpectedly: got %d", cfgAfter.InitialMarginBps)
	}
	if cfgAfter.MaintenanceMarginBps != 500 {
		t.Errorf("MaintenanceMarginBps changed unexpectedly: got %d", cfgAfter.MaintenanceMarginBps)
	}
	if cfgAfter.MaxFundingRateBps != 200 {
		t.Errorf("MaxFundingRateBps changed unexpectedly: got %d", cfgAfter.MaxFundingRateBps)
	}
}
