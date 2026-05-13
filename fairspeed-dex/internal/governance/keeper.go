package governance

import "fmt"

// ProposalStore is the persistence interface for proposals and votes.
// AppState implements this.
type ProposalStore interface {
	GetProposal(proposalId string) (Proposal, bool)
	SetProposal(p Proposal)
	AllProposals() []Proposal
	GetVotesForProposal(proposalId string) []VoteRecord
	AddVote(v VoteRecord)
}

// ParameterExecutor applies the parameters of a passed proposal.
// LocalNode implements this via FeeCalculator and RiskChecker setters.
type ParameterExecutor interface {
	UpdateFeePolicy(params UpdateFeePolicyParams) error
	UpdateRiskPolicy(params UpdateRiskPolicyParams) error
	ListMarket(params ListMarketParams) error
	UpdatePerpConfig(params UpdatePerpConfigParams) error
}

// EventPublisher is the subset of the event bus governance needs.
type EventPublisher interface {
	PublishProposalSubmitted(proposalId, proposalType, title string, blockHeight int64)
	PublishVoteCast(proposalId, validatorId, choice string, stake int64, blockHeight int64)
	PublishProposalPassed(proposalId string, blockHeight int64)
	PublishProposalRejected(proposalId string, blockHeight int64)
	PublishProposalExecuted(proposalId string, blockHeight int64)
}

// GovernanceKeeper manages proposals, votes, and parameter execution.
type GovernanceKeeper struct {
	store ProposalStore
	bus   EventPublisher
}

func NewGovernanceKeeper(store ProposalStore, bus EventPublisher) *GovernanceKeeper {
	return &GovernanceKeeper{store: store, bus: bus}
}

// SubmitProposal creates a new governance proposal in VOTING state.
func (k *GovernanceKeeper) SubmitProposal(
	proposalType ProposalType,
	title, description string,
	payload any,
	voteEndHeight int64,
	blockHeight int64,
) (Proposal, error) {
	if voteEndHeight <= blockHeight {
		return Proposal{}, fmt.Errorf("vote_end_height %d must be > current height %d", voteEndHeight, blockHeight)
	}
	p := Proposal{
		ProposalId:    newProposalId(),
		Type:          proposalType,
		Title:         title,
		Description:   description,
		Status:        StatusVoting,
		SubmitHeight:  blockHeight,
		VoteEndHeight: voteEndHeight,
		Payload:       payload,
	}
	k.store.SetProposal(p)
	k.bus.PublishProposalSubmitted(p.ProposalId, string(p.Type), p.Title, blockHeight)
	return p, nil
}

// CastVote records a stake-weighted vote. Re-voting replaces the previous vote.
func (k *GovernanceKeeper) CastVote(proposalId, validatorId, choice string, stake int64, blockHeight int64) error {
	p, ok := k.store.GetProposal(proposalId)
	if !ok {
		return fmt.Errorf("proposal %s not found", proposalId)
	}
	if p.Status != StatusVoting {
		return fmt.Errorf("proposal %s is not open for voting (status=%s)", proposalId, p.Status)
	}
	if blockHeight > p.VoteEndHeight {
		return fmt.Errorf("voting period for proposal %s ended at height %d", proposalId, p.VoteEndHeight)
	}
	vc := VoteChoice(choice)
	if vc != VoteYes && vc != VoteNo && vc != VoteAbstain {
		return fmt.Errorf("invalid vote choice %q: must be YES, NO, or ABSTAIN", choice)
	}
	// Undo any previous vote from this validator.
	for _, v := range k.store.GetVotesForProposal(proposalId) {
		if v.ValidatorId == validatorId {
			switch v.Choice {
			case VoteYes:
				p.TallyYes -= v.Stake
			case VoteNo:
				p.TallyNo -= v.Stake
			case VoteAbstain:
				p.TallyAbstain -= v.Stake
			}
			break
		}
	}
	k.store.AddVote(VoteRecord{ProposalId: proposalId, ValidatorId: validatorId, Choice: vc, Stake: stake})
	switch vc {
	case VoteYes:
		p.TallyYes += stake
	case VoteNo:
		p.TallyNo += stake
	case VoteAbstain:
		p.TallyAbstain += stake
	}
	k.store.SetProposal(p)
	k.bus.PublishVoteCast(proposalId, validatorId, choice, stake, blockHeight)
	return nil
}

// TallyAndExecute evaluates proposals whose VoteEndHeight equals blockHeight.
// Proposals with strict majority (>50% YES by stake) are executed; others rejected.
// Returns the IDs of proposals whose status changed this block.
func (k *GovernanceKeeper) TallyAndExecute(blockHeight int64, executor ParameterExecutor) []string {
	var changed []string
	for _, p := range k.store.AllProposals() {
		if p.Status != StatusVoting || p.VoteEndHeight != blockHeight {
			continue
		}
		totalVotes := p.TallyYes + p.TallyNo + p.TallyAbstain
		// Strict majority: YES must be > 50% of all votes cast.
		if totalVotes == 0 || p.TallyYes*2 <= totalVotes {
			p.Status = StatusRejected
			k.store.SetProposal(p)
			k.bus.PublishProposalRejected(p.ProposalId, blockHeight)
			changed = append(changed, p.ProposalId)
			continue
		}
		p.Status = StatusPassed
		k.store.SetProposal(p)
		k.bus.PublishProposalPassed(p.ProposalId, blockHeight)
		changed = append(changed, p.ProposalId)

		if executor != nil {
			_ = k.execute(p, executor, blockHeight)
		}
	}
	return changed
}

// GetProposal returns a proposal by ID.
func (k *GovernanceKeeper) GetProposal(proposalId string) (Proposal, bool) {
	return k.store.GetProposal(proposalId)
}

// AllProposals returns all proposals.
func (k *GovernanceKeeper) AllProposals() []Proposal {
	return k.store.AllProposals()
}

func (k *GovernanceKeeper) execute(p Proposal, executor ParameterExecutor, blockHeight int64) error {
	var err error
	switch p.Type {
	case TypeUpdateFeePolicy:
		if params, ok := p.Payload.(UpdateFeePolicyParams); ok {
			err = executor.UpdateFeePolicy(params)
		}
	case TypeUpdateRiskPolicy:
		if params, ok := p.Payload.(UpdateRiskPolicyParams); ok {
			err = executor.UpdateRiskPolicy(params)
		}
	case TypeListMarket:
		if params, ok := p.Payload.(ListMarketParams); ok {
			err = executor.ListMarket(params)
		}
	case TypeUpdatePerpConfig:
		if params, ok := p.Payload.(UpdatePerpConfigParams); ok {
			err = executor.UpdatePerpConfig(params)
		}
	}
	if err != nil {
		return err
	}
	p.Status = StatusExecuted
	k.store.SetProposal(p)
	k.bus.PublishProposalExecuted(p.ProposalId, blockHeight)
	return nil
}
