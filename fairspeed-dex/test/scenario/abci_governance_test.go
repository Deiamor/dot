package scenario_test

// Stage 8 ABCI extension tests — governance, validator, KYC paths.
//
// Verifies the new capabilities added to DEXApplication:
//  1. CheckTx accepts all new tx types (KYCApprove, BondValidator, SubmitProposal, Vote)
//  2. CheckTx rejects new tx types with invalid fields
//  3. FinalizeBlock: validator bond through ABCI → Query /validators
//  4. FinalizeBlock: full governance round-trip (submit → vote → tally) via ABCI
//  5. AppHash changes when validator set changes
//  6. Query /kyc/{accountId} returns KYC status
//  7. Query /governance/votes/{proposalId} returns recorded votes

import (
	"encoding/json"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/abci"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/validator"
)

// bootstrapABCINode returns both the LocalNode and DEXApplication so tests
// can inspect node state while driving through the ABCI path.
func bootstrapABCINode(t *testing.T) (*abci.DEXApplication, *node.LocalNode, string, string, string, string) {
	t.Helper()
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)
	app := abci.NewDEXApplication(n, "")
	return app, n, aliceId, bobId, aliceSess, bobSess
}

// finalizeAt is a helper that runs PrepareProposal → FinalizeBlock at height h.
func finalizeAt(t *testing.T, app *abci.DEXApplication, height int64, txs []fairbatch.Transaction) abci.ResponseFinalizeBlock {
	t.Helper()
	raw := make([][]byte, len(txs))
	for i, tx := range txs {
		b, err := abci.EncodeTx(tx)
		if err != nil {
			t.Fatalf("EncodeTx: %v", err)
		}
		raw[i] = b
	}
	prepared := app.PrepareProposal(abci.RequestPrepareProposal{Txs: raw, Height: height})
	return app.FinalizeBlock(abci.RequestFinalizeBlock{Txs: prepared.Txs, Height: height})
}

// -------------------------------------------------------------------------
// Scenario 1: CheckTx accepts valid new tx types and rejects invalid ones
// -------------------------------------------------------------------------
func TestABCI_CheckTx_NewTxTypes(t *testing.T) {
	t.Run("Valid", testABCI_CheckTx_NewTxTypes_Valid)
	t.Run("Invalid", testABCI_CheckTx_NewTxTypes_Invalid)
}

func testABCI_CheckTx_NewTxTypes_Valid(t *testing.T) {
	app, _, aliceId, _, _, _ := bootstrapABCINode(t)

	cases := []struct {
		name string
		tx   fairbatch.Transaction
	}{
		{
			"KYCApprove",
			fairbatch.Transaction{
				TxType:    fairbatch.TxKYCApprove,
				AccountId: aliceId,
				Payload:   fairbatch.KYCApprovePayload{AccountId: aliceId, Status: "APPROVED"},
			},
		},
		{
			"BondValidator",
			fairbatch.Transaction{
				TxType: fairbatch.TxBondValidator,
				Payload: fairbatch.BondValidatorPayload{
					ValidatorId: "val-test01", Moniker: "test", PubKey: "aabbcc", StakeAmount: 10_000,
				},
			},
		},
		{
			"SubmitProposal",
			fairbatch.Transaction{
				TxType: fairbatch.TxSubmitProposal,
				Payload: fairbatch.SubmitProposalPayload{
					ProposalType:  "UpdateFeePolicy",
					Title:         "New fees",
					VoteEndHeight: 20,
					MakerBps:      10,
					TakerBps:      20,
				},
			},
		},
		{
			"Vote",
			fairbatch.Transaction{
				TxType: fairbatch.TxVote,
				Payload: fairbatch.VotePayload{
					ProposalId: "prop-fake", ValidatorId: "val-test01", Choice: "YES", Stake: 10_000,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := abci.EncodeTx(tc.tx)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			resp := app.CheckTx(abci.RequestCheckTx{Tx: raw, Type: abci.CheckTxNew})
			if resp.Code != abci.CodeOK {
				t.Errorf("CheckTx %s: expected OK, got code=%d log=%s", tc.name, resp.Code, resp.Log)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Scenario 2: CheckTx rejects invalid fields in new tx types (subtest)
// -------------------------------------------------------------------------
func testABCI_CheckTx_NewTxTypes_Invalid(t *testing.T) {
	app, _, _, _, _, _ := bootstrapABCINode(t)

	cases := []struct {
		name string
		tx   fairbatch.Transaction
	}{
		{
			"KYCApprove_EmptyAccountId",
			fairbatch.Transaction{
				TxType:  fairbatch.TxKYCApprove,
				Payload: fairbatch.KYCApprovePayload{AccountId: "", Status: "APPROVED"},
			},
		},
		{
			"KYCApprove_BadStatus",
			fairbatch.Transaction{
				TxType:  fairbatch.TxKYCApprove,
				Payload: fairbatch.KYCApprovePayload{AccountId: "acc-x", Status: "MAYBE"},
			},
		},
		{
			"BondValidator_ZeroStake",
			fairbatch.Transaction{
				TxType: fairbatch.TxBondValidator,
				Payload: fairbatch.BondValidatorPayload{
					ValidatorId: "val-x", Moniker: "x", PubKey: "aa", StakeAmount: 0,
				},
			},
		},
		{
			"SubmitProposal_EmptyTitle",
			fairbatch.Transaction{
				TxType: fairbatch.TxSubmitProposal,
				Payload: fairbatch.SubmitProposalPayload{
					ProposalType: "UpdateFeePolicy", Title: "", VoteEndHeight: 20,
				},
			},
		},
		{
			"SubmitProposal_BadType",
			fairbatch.Transaction{
				TxType: fairbatch.TxSubmitProposal,
				Payload: fairbatch.SubmitProposalPayload{
					ProposalType: "InvalidType", Title: "x", VoteEndHeight: 20,
				},
			},
		},
		{
			"Vote_BadChoice",
			fairbatch.Transaction{
				TxType: fairbatch.TxVote,
				Payload: fairbatch.VotePayload{
					ProposalId: "prop-x", ValidatorId: "val-x", Choice: "MAYBE",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := abci.EncodeTx(tc.tx)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			resp := app.CheckTx(abci.RequestCheckTx{Tx: raw, Type: abci.CheckTxNew})
			if resp.Code == abci.CodeOK {
				t.Errorf("CheckTx %s: expected error, got OK", tc.name)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Scenario 3: FinalizeBlock: bond validator → Query /validators
// -------------------------------------------------------------------------
func TestABCI_FinalizeBlock_ValidatorBond(t *testing.T) {
	app, _, _, _, _, _ := bootstrapABCINode(t)

	// Bond a validator at block 4 (bootstrapNode leaves height=3).
	result := finalizeAt(t, app, 4, []fairbatch.Transaction{
		{
			TxType: fairbatch.TxBondValidator,
			Payload: fairbatch.BondValidatorPayload{
				ValidatorId: "val-abc", Moniker: "alpha", PubKey: "aabb", StakeAmount: 50_000,
			},
		},
	})
	for i, r := range result.TxResults {
		if r.Code != abci.CodeOK {
			t.Errorf("tx[%d] failed: %s", i, r.Log)
		}
	}

	// Query /validators.
	resp := app.Query(abci.RequestQuery{Path: "/validators"})
	if resp.Code != abci.CodeOK {
		t.Fatalf("Query /validators: %s", resp.Log)
	}
	var vals []validator.Validator
	if err := json.Unmarshal(resp.Value, &vals); err != nil {
		t.Fatalf("decode validators: %v", err)
	}
	if len(vals) != 1 {
		t.Fatalf("expected 1 validator, got %d", len(vals))
	}
	if vals[0].ValidatorId != "val-abc" {
		t.Errorf("validator id: want val-abc got %s", vals[0].ValidatorId)
	}
	if vals[0].Stake != 50_000 {
		t.Errorf("stake: want 50000 got %d", vals[0].Stake)
	}

	// Query single validator.
	resp2 := app.Query(abci.RequestQuery{Path: "/validators/val-abc"})
	if resp2.Code != abci.CodeOK {
		t.Fatalf("Query /validators/val-abc: %s", resp2.Log)
	}
	var v validator.Validator
	_ = json.Unmarshal(resp2.Value, &v)
	if v.ValidatorId != "val-abc" {
		t.Errorf("single validator id: want val-abc got %s", v.ValidatorId)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Full governance round-trip via ABCI
// -------------------------------------------------------------------------
func TestABCI_FinalizeBlock_GovernanceFull(t *testing.T) {
	app, n, _, _, _, _ := bootstrapABCINode(t)

	// Block 4: submit proposal with VoteEndHeight=6.
	finalizeAt(t, app, 4, []fairbatch.Transaction{
		{
			TxType: fairbatch.TxSubmitProposal,
			Payload: fairbatch.SubmitProposalPayload{
				ProposalType: "UpdateFeePolicy", Title: "Raise fees",
				VoteEndHeight: 6, MakerBps: 10, TakerBps: 20,
			},
		},
	})

	// Get the proposal ID.
	all := n.AllProposals()
	if len(all) == 0 {
		t.Fatal("no proposals after SubmitProposal block")
	}
	propId := all[0].ProposalId

	// Block 5: cast a YES vote with majority stake.
	finalizeAt(t, app, 5, []fairbatch.Transaction{
		{
			TxType: fairbatch.TxVote,
			Payload: fairbatch.VotePayload{
				ProposalId: propId, ValidatorId: "val-alpha", Choice: "YES", Stake: 80_000,
			},
		},
	})

	// Block 6: empty block triggers tally at VoteEndHeight=6.
	finalizeAt(t, app, 6, nil)

	// Query proposal status.
	resp := app.Query(abci.RequestQuery{Path: "/governance/proposals/" + propId})
	if resp.Code != abci.CodeOK {
		t.Fatalf("Query proposal: %s", resp.Log)
	}
	var p governance.Proposal
	if err := json.Unmarshal(resp.Value, &p); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	if p.Status != governance.StatusExecuted {
		t.Errorf("proposal status: want EXECUTED got %s", p.Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: AppHash changes when validator set changes
// -------------------------------------------------------------------------
func TestABCI_AppHash_ChangesWithValidatorSet(t *testing.T) {
	// Node A: no validators.
	n1 := node.NewLocalNode()
	n1.RegisterAsset(asset.BTC)
	n1.RegisterAsset(asset.USDC)
	app1 := abci.NewDEXApplication(n1, "")
	r1 := app1.FinalizeBlock(abci.RequestFinalizeBlock{Txs: nil, Height: 1})

	// Node B: same height but with a bonded validator.
	n2 := node.NewLocalNode()
	n2.RegisterAsset(asset.BTC)
	n2.RegisterAsset(asset.USDC)
	app2 := abci.NewDEXApplication(n2, "")
	finalizeAt(t, app2, 1, []fairbatch.Transaction{
		{
			TxType: fairbatch.TxBondValidator,
			Payload: fairbatch.BondValidatorPayload{
				ValidatorId: "val-x", Moniker: "x", PubKey: "aa", StakeAmount: 10_000,
			},
		},
	})

	info1 := app1.Info(abci.RequestInfo{})
	_ = r1
	info2 := app2.Info(abci.RequestInfo{})

	if string(info1.LastBlockAppHash) == string(info2.LastBlockAppHash) {
		t.Error("AppHash should differ when validator set differs")
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Query /kyc/{accountId} returns correct status
// -------------------------------------------------------------------------
func TestABCI_Query_KYCStatus(t *testing.T) {
	app, _, aliceId, _, _, _ := bootstrapABCINode(t)

	resp := app.Query(abci.RequestQuery{Path: "/kyc/" + aliceId})
	if resp.Code != abci.CodeOK {
		t.Fatalf("Query /kyc: %s", resp.Log)
	}
	var result struct {
		AccountId string `json:"AccountId"`
		KYCStatus string `json:"KYCStatus"`
	}
	if err := json.Unmarshal(resp.Value, &result); err != nil {
		t.Fatalf("decode kyc response: %v", err)
	}
	if result.AccountId != aliceId {
		t.Errorf("account id: want %s got %s", aliceId, result.AccountId)
	}
	if result.KYCStatus != "PENDING" {
		t.Errorf("KYC status: want PENDING got %s", result.KYCStatus)
	}
}

// -------------------------------------------------------------------------
// Scenario 7: Query /governance/votes/{proposalId}
// -------------------------------------------------------------------------
func TestABCI_Query_GovernanceVotes(t *testing.T) {
	app, n, _, _, _, _ := bootstrapABCINode(t)

	// Submit proposal.
	finalizeAt(t, app, 4, []fairbatch.Transaction{
		{
			TxType: fairbatch.TxSubmitProposal,
			Payload: fairbatch.SubmitProposalPayload{
				ProposalType: "UpdateFeePolicy", Title: "Test",
				VoteEndHeight: 20, MakerBps: 5, TakerBps: 10,
			},
		},
	})
	propId := n.AllProposals()[0].ProposalId

	// Cast votes.
	finalizeAt(t, app, 5, []fairbatch.Transaction{
		{
			TxType: fairbatch.TxVote,
			Payload: fairbatch.VotePayload{
				ProposalId: propId, ValidatorId: "val-alpha", Choice: "YES", Stake: 60_000,
			},
		},
		{
			TxType: fairbatch.TxVote,
			Payload: fairbatch.VotePayload{
				ProposalId: propId, ValidatorId: "val-beta", Choice: "NO", Stake: 40_000,
			},
		},
	})

	// Query votes.
	resp := app.Query(abci.RequestQuery{Path: "/governance/votes/" + propId})
	if resp.Code != abci.CodeOK {
		t.Fatalf("Query /governance/votes: %s", resp.Log)
	}
	var votes []governance.VoteRecord
	if err := json.Unmarshal(resp.Value, &votes); err != nil {
		t.Fatalf("decode votes: %v", err)
	}
	if len(votes) != 2 {
		t.Fatalf("expected 2 votes, got %d", len(votes))
	}

	// Also verify proposals list.
	resp2 := app.Query(abci.RequestQuery{Path: "/governance/proposals"})
	if resp2.Code != abci.CodeOK {
		t.Fatalf("Query /governance/proposals: %s", resp2.Log)
	}
	var props []governance.Proposal
	_ = json.Unmarshal(resp2.Value, &props)
	if len(props) < 1 {
		t.Error("expected at least 1 proposal")
	}
}
