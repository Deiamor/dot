package scenario_test

// Stage 10: Snapshot completeness tests.
//
// Verifies that SaveSnapshot / LoadSnapshot persists and restores all state
// introduced in Stages 6-7:
//
//  1. Bonded validators survive a snapshot round-trip
//  2. Governance proposals survive a snapshot round-trip
//  3. Governance votes survive a snapshot round-trip
//  4. Combined round-trip: validator + proposal + vote state all intact
//  5. An empty (no validators/proposals) snapshot loads cleanly

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/validator"
)

func tempSnapshotPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "appstate.json")
}

// -------------------------------------------------------------------------
// Scenario 1: Bonded validators survive snapshot round-trip
// -------------------------------------------------------------------------
func TestSnapshot_ValidatorsPersisted(t *testing.T) {
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddBondValidator("val-snap-01", "snap", "aabb", 42_000).Build())
	if err != nil {
		t.Fatalf("bond validator: %v", err)
	}

	path := tempSnapshotPath(t)
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	v, ok := n2.GetValidatorInfo("val-snap-01")
	if !ok {
		t.Fatal("validator not found after snapshot load")
	}
	if v.Stake != 42_000 {
		t.Errorf("stake: want 42000 got %d", v.Stake)
	}
	if v.Status != validator.StatusBonded {
		t.Errorf("status: want BONDED got %s", v.Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Governance proposals survive snapshot round-trip
// -------------------------------------------------------------------------
func TestSnapshot_ProposalsPersisted(t *testing.T) {
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Snapshot proposal",
			VoteEndHeight: 50,
			MakerBps:      10,
			TakerBps:      20,
		}).Build())
	if err != nil {
		t.Fatalf("submit proposal: %v", err)
	}

	all := n.AllProposals()
	if len(all) == 0 {
		t.Fatal("no proposals before snapshot")
	}
	propId := all[0].ProposalId

	path := tempSnapshotPath(t)
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	p, ok := n2.GetProposal(propId)
	if !ok {
		t.Fatal("proposal not found after snapshot load")
	}
	if p.Status != governance.StatusVoting {
		t.Errorf("status: want VOTING got %s", p.Status)
	}
	if p.VoteEndHeight != 50 {
		t.Errorf("vote end height: want 50 got %d", p.VoteEndHeight)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Governance votes survive snapshot round-trip
// -------------------------------------------------------------------------
func TestSnapshot_VotesPersisted(t *testing.T) {
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Vote snapshot",
			VoteEndHeight: 50,
			MakerBps:      5,
			TakerBps:      10,
		}).Build())

	propId := n.AllProposals()[0].ProposalId

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddVote(propId, "val-alpha", "YES", 60_000).
		AddVote(propId, "val-beta", "NO", 40_000).
		Build())
	if err != nil {
		t.Fatalf("cast votes: %v", err)
	}

	path := tempSnapshotPath(t)
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	p, ok := n2.GetProposal(propId)
	if !ok {
		t.Fatal("proposal not found after snapshot load")
	}
	if p.TallyYes != 60_000 {
		t.Errorf("tally YES: want 60000 got %d", p.TallyYes)
	}
	if p.TallyNo != 40_000 {
		t.Errorf("tally NO: want 40000 got %d", p.TallyNo)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Combined round-trip — validator + proposal + votes all intact
// -------------------------------------------------------------------------
func TestSnapshot_CombinedRoundTrip(t *testing.T) {
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddBondValidator("val-combined", "combo", "ccdd", 100_000).
		AddSubmitProposal(fairbatch.SubmitProposalPayload{
			ProposalType:  "UpdateFeePolicy",
			Title:         "Combined test",
			VoteEndHeight: 100,
			MakerBps:      3,
			TakerBps:      7,
		}).Build())

	propId := n.AllProposals()[0].ProposalId

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddVote(propId, "val-combined", "YES", 100_000).
		Build())

	path := tempSnapshotPath(t)
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	// Validator check.
	v, ok := n2.GetValidatorInfo("val-combined")
	if !ok {
		t.Fatal("validator missing after load")
	}
	if v.Stake != 100_000 {
		t.Errorf("validator stake: want 100000 got %d", v.Stake)
	}

	// Proposal check.
	p, ok := n2.GetProposal(propId)
	if !ok {
		t.Fatal("proposal missing after load")
	}
	if p.TallyYes != 100_000 {
		t.Errorf("tally YES: want 100000 got %d", p.TallyYes)
	}

	// Height preserved.
	if n2.CurrentHeight() != n.CurrentHeight() {
		t.Errorf("height: want %d got %d", n.CurrentHeight(), n2.CurrentHeight())
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Clean load when snapshot has no validators/proposals
// -------------------------------------------------------------------------
func TestSnapshot_EmptyValidatorsProposals(t *testing.T) {
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)

	path := tempSnapshotPath(t)
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(n2.AllProposals()) != 0 {
		t.Errorf("expected 0 proposals, got %d", len(n2.AllProposals()))
	}
	if n2.TotalValidatorStake() != 0 {
		t.Errorf("expected 0 total stake, got %d", n2.TotalValidatorStake())
	}
}
