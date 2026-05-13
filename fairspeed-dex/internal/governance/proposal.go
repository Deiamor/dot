// Package governance implements on-chain parameter governance for fairspeed-dex.
//
// Validators submit proposals and cast stake-weighted votes. At the proposal's
// VoteEndHeight, the block processor tallies results and executes passed proposals
// immediately via a ParameterExecutor callback.
//
// Supported proposal types:
//   - UpdateFeePolicy:  change maker/taker fee basis points
//   - UpdateRiskPolicy: change order size / position / KYC / AML limits
//   - ListMarket:       add a new trading market
package governance

import (
	"crypto/rand"
	"fmt"
)

type ProposalStatus string

const (
	StatusVoting   ProposalStatus = "VOTING"
	StatusPassed   ProposalStatus = "PASSED"
	StatusRejected ProposalStatus = "REJECTED"
	StatusExecuted ProposalStatus = "EXECUTED"
)

type ProposalType string

const (
	TypeUpdateFeePolicy  ProposalType = "UpdateFeePolicy"
	TypeUpdateRiskPolicy ProposalType = "UpdateRiskPolicy"
	TypeListMarket       ProposalType = "ListMarket"
)

type VoteChoice string

const (
	VoteYes     VoteChoice = "YES"
	VoteNo      VoteChoice = "NO"
	VoteAbstain VoteChoice = "ABSTAIN"
)

// UpdateFeePolicyParams are the execution parameters for an UpdateFeePolicy proposal.
type UpdateFeePolicyParams struct {
	MakerBps int64
	TakerBps int64
}

// UpdateRiskPolicyParams are the execution parameters for an UpdateRiskPolicy proposal.
type UpdateRiskPolicyParams struct {
	MaxOrderQuantity            int64
	MinOrderQuantity            int64
	MaxDailyVolumePerSession    int64
	MaxPositionSize             int64
	RequireKYC                  bool
	AMLSingleTradeLimitNotional int64
}

// ListMarketParams are the execution parameters for a ListMarket proposal.
type ListMarketParams struct {
	MarketId   string
	BaseAsset  string
	QuoteAsset string
}

// Proposal represents a governance proposal.
type Proposal struct {
	ProposalId    string
	Type          ProposalType
	Title         string
	Description   string
	Status        ProposalStatus
	SubmitHeight  int64
	VoteEndHeight int64
	TallyYes      int64 // stake-weighted YES votes
	TallyNo       int64
	TallyAbstain  int64
	// Payload holds the typed execution parameters.
	// One of: UpdateFeePolicyParams, UpdateRiskPolicyParams, ListMarketParams.
	Payload any
}

// VoteRecord records one validator's vote on a proposal.
type VoteRecord struct {
	ProposalId  string
	ValidatorId string
	Choice      VoteChoice
	Stake       int64
}

func newProposalId() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("prop-%x", b)
}
