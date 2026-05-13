package fairbatch

import (
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

type CreateAccountPayload struct {
	OwnerAddress        string
	RootPublicKey       string
	WithdrawalPublicKey string
}

type CreateSessionPayload struct {
	AccountId string
	Opts      account.SessionOptions
}

type DepositPayload struct {
	AccountId string
	AssetId   string
	Amount    int64
}

type SubmitOrderPayload struct {
	Order clob.Order
}

type CancelOrderPayload struct {
	OrderId   string
	AccountId string
}

type WithdrawPayload struct {
	AccountId       string
	AssetId         string
	Amount          int64
	AccountSequence uint64
	Signature       string // signed by WithdrawalPublicKey over TxHash
}

// KYCApprovePayload is submitted by a privileged admin to approve or revoke an account's KYC.
type KYCApprovePayload struct {
	AccountId string
	Status    string // "APPROVED" | "REVOKED" | "EXEMPT"
}

// SubmitProposalPayload carries all parameters for a governance proposal.
// Only the fields relevant to ProposalType are used on execution.
type SubmitProposalPayload struct {
	ProposalType string
	Title        string
	Description  string
	VoteEndHeight int64
	// Fee policy params (ProposalType="UpdateFeePolicy")
	MakerBps int64
	TakerBps int64
	// Risk policy params (ProposalType="UpdateRiskPolicy")
	MaxOrderQuantity            int64
	MinOrderQuantity            int64
	MaxDailyVolumePerSession    int64
	MaxPositionSize             int64
	RequireKYC                  bool
	AMLSingleTradeLimitNotional int64
	// Market listing params (ProposalType="ListMarket")
	MarketId   string
	BaseAsset  string
	QuoteAsset string
}

// VotePayload casts a stake-weighted vote on a governance proposal.
type VotePayload struct {
	ProposalId  string
	ValidatorId string
	Choice      string // "YES" | "NO" | "ABSTAIN"
	Stake       int64
}

// BondValidatorPayload bonds stake to join the active validator set.
type BondValidatorPayload struct {
	ValidatorId string
	Moniker     string
	PubKey      string // ed25519 public key hex
	StakeAmount int64
}

// UnbondValidatorPayload initiates graceful exit from the validator set.
type UnbondValidatorPayload struct {
	ValidatorId string
}

type BatchBuilder struct {
	blockHeight int64
	txs         []Transaction
}

func NewBatchBuilder(blockHeight int64) *BatchBuilder {
	return &BatchBuilder{blockHeight: blockHeight}
}

func (b *BatchBuilder) AddCreateAccount(ownerAddr, rootKey, withdrawKey string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType: TxCreateAccount,
		Payload: CreateAccountPayload{
			OwnerAddress:        ownerAddr,
			RootPublicKey:       rootKey,
			WithdrawalPublicKey: withdrawKey,
		},
	})
	return b
}

func (b *BatchBuilder) AddCreateSession(accountId string, opts account.SessionOptions) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxCreateSession,
		AccountId: accountId,
		Payload:   CreateSessionPayload{AccountId: accountId, Opts: opts},
	})
	return b
}

func (b *BatchBuilder) AddDeposit(accountId, assetId string, amount int64) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxDeposit,
		AccountId: accountId,
		Payload:   DepositPayload{AccountId: accountId, AssetId: assetId, Amount: amount},
	})
	return b
}

func (b *BatchBuilder) AddSubmitOrder(o clob.Order) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxSubmitOrder,
		AccountId: o.AccountId,
		SessionId: o.SessionId,
		Payload:   SubmitOrderPayload{Order: o},
	})
	return b
}

func (b *BatchBuilder) AddCancelOrder(orderId, accountId string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxCancelOrder,
		AccountId: accountId,
		Payload:   CancelOrderPayload{OrderId: orderId, AccountId: accountId},
	})
	return b
}

func (b *BatchBuilder) AddWithdraw(accountId, assetId string, amount int64, seq uint64, sig string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxWithdraw,
		AccountId: accountId,
		Payload: WithdrawPayload{
			AccountId:       accountId,
			AssetId:         assetId,
			Amount:          amount,
			AccountSequence: seq,
			Signature:       sig,
		},
	})
	return b
}

// AddKYCApprove adds an admin KYC status update transaction to the batch.
// status must be one of: "APPROVED", "REVOKED", "EXEMPT".
func (b *BatchBuilder) AddKYCApprove(accountId, status string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxKYCApprove,
		AccountId: accountId,
		Payload:   KYCApprovePayload{AccountId: accountId, Status: status},
	})
	return b
}

// AddSubmitProposal adds a governance proposal transaction to the batch.
func (b *BatchBuilder) AddSubmitProposal(p SubmitProposalPayload) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxSubmitProposal,
		Payload: p,
	})
	return b
}

// AddVote adds a governance vote transaction to the batch.
func (b *BatchBuilder) AddVote(proposalId, validatorId, choice string, stake int64) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType: TxVote,
		Payload: VotePayload{
			ProposalId:  proposalId,
			ValidatorId: validatorId,
			Choice:      choice,
			Stake:       stake,
		},
	})
	return b
}

// AddBondValidator adds a validator bond transaction to the batch.
func (b *BatchBuilder) AddBondValidator(validatorId, moniker, pubKey string, stake int64) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType: TxBondValidator,
		Payload: BondValidatorPayload{
			ValidatorId: validatorId,
			Moniker:     moniker,
			PubKey:      pubKey,
			StakeAmount: stake,
		},
	})
	return b
}

// AddUnbondValidator adds a validator unbond transaction to the batch.
func (b *BatchBuilder) AddUnbondValidator(validatorId string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxUnbondValidator,
		Payload: UnbondValidatorPayload{ValidatorId: validatorId},
	})
	return b
}

// SanctionAccountPayload adds an account to the on-chain sanctions list.
type SanctionAccountPayload struct {
	AccountId string
	Reason    string // free-text reason (e.g. "OFAC SDN list")
	ListName  string // canonical list name (e.g. "OFAC", "UN")
}

// UnsanctionAccountPayload removes an account from the on-chain sanctions list.
type UnsanctionAccountPayload struct {
	AccountId string
}

// AddSanctionAccount adds an account to the on-chain sanctions list.
func (b *BatchBuilder) AddSanctionAccount(accountId, reason, listName string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType: TxSanctionAccount,
		Payload: SanctionAccountPayload{
			AccountId: accountId,
			Reason:    reason,
			ListName:  listName,
		},
	})
	return b
}

// AddUnsanctionAccount removes an account from the on-chain sanctions list.
func (b *BatchBuilder) AddUnsanctionAccount(accountId string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxUnsanctionAccount,
		Payload: UnsanctionAccountPayload{AccountId: accountId},
	})
	return b
}

func (b *BatchBuilder) Build() FairBatch {
	sorted, batchHash := SortAndHash(b.txs, b.blockHeight)
	return FairBatch{
		BatchId:      newBatchID(),
		BlockHeight:  b.blockHeight,
		Transactions: sorted,
		BatchHash:    batchHash,
		Timestamp:    time.Now().UnixNano(),
	}
}
