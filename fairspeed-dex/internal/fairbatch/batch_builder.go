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
// Tier and Jurisdiction are optional — zero values leave existing values unchanged.
type KYCApprovePayload struct {
	AccountId    string
	Status       string // "APPROVED" | "REVOKED" | "EXEMPT"
	Tier         int    // 0 = unchanged, 1/2/3 = set tier
	Jurisdiction string // "" = unchanged; "US"|"EU"|"APAC"|"DEFAULT"
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
// tier 0 means unchanged; jurisdiction "" means unchanged.
func (b *BatchBuilder) AddKYCApprove(accountId, status string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxKYCApprove,
		AccountId: accountId,
		Payload:   KYCApprovePayload{AccountId: accountId, Status: status},
	})
	return b
}

// AddKYCApproveWithTier is like AddKYCApprove but also sets KYC tier and jurisdiction.
func (b *BatchBuilder) AddKYCApproveWithTier(accountId, status string, tier int, jurisdiction string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxKYCApprove,
		AccountId: accountId,
		Payload: KYCApprovePayload{
			AccountId:    accountId,
			Status:       status,
			Tier:         tier,
			Jurisdiction: jurisdiction,
		},
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

// HaltMarketPayload suspends all order submission in a market.
type HaltMarketPayload struct {
	MarketId string
	Reason   string
}

// ResumeMarketPayload lifts a previously imposed market halt.
type ResumeMarketPayload struct {
	MarketId string
}

// AddHaltMarket appends a market-halt transaction to the batch.
func (b *BatchBuilder) AddHaltMarket(marketId, reason string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxHaltMarket,
		Payload: HaltMarketPayload{MarketId: marketId, Reason: reason},
	})
	return b
}

// AddResumeMarket appends a market-resume transaction to the batch.
func (b *BatchBuilder) AddResumeMarket(marketId string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxResumeMarket,
		Payload: ResumeMarketPayload{MarketId: marketId},
	})
	return b
}

// SubmitPricePayload carries a validator's mark price submission for a market.
type SubmitPricePayload struct {
	MarketId    string
	ValidatorId string
	Price       int64
}

// AddSubmitPrice appends a validator price oracle submission to the batch.
func (b *BatchBuilder) AddSubmitPrice(marketId, validatorId string, price int64) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxSubmitPrice,
		Payload: SubmitPricePayload{MarketId: marketId, ValidatorId: validatorId, Price: price},
	})
	return b
}


// WithdrawRequestPayload requests a large withdrawal that requires a timelock delay.
type WithdrawRequestPayload struct {
	AccountId       string
	AssetId         string
	Amount          int64
	AccountSequence uint64
	Signature       string
}

// AddWithdrawRequest appends a timelocked withdrawal request to the batch.
func (b *BatchBuilder) AddWithdrawRequest(accountId, assetId string, amount int64, seq uint64, sig string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxWithdrawRequest,
		AccountId: accountId,
		Payload: WithdrawRequestPayload{
			AccountId:       accountId,
			AssetId:         assetId,
			Amount:          amount,
			AccountSequence: seq,
			Signature:       sig,
		},
	})
	return b
}

// RegisterPerpMarketPayload registers a new perpetual futures market with its config.
type RegisterPerpMarketPayload struct {
	MarketId              string
	BaseAsset             string
	QuoteAsset            string
	InitialMarginBps      int64
	MaintenanceMarginBps  int64
	MaxLeverage           int64
	FundingIntervalBlocks int64
	MaxFundingRateBps     int64
}

// AddRegisterPerpMarket appends a perpetual market registration to the batch.
func (b *BatchBuilder) AddRegisterPerpMarket(p RegisterPerpMarketPayload) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxRegisterPerpMarket,
		Payload: p,
	})
	return b
}

// SubmitConditionalOrderPayload wraps a ConditionalOrder for inclusion in a batch.
type SubmitConditionalOrderPayload struct {
	Order clob.ConditionalOrder
}

// CancelConditionalOrderPayload cancels a pending conditional order by ID.
type CancelConditionalOrderPayload struct {
	OrderId   string
	AccountId string
}

// AddSubmitConditionalOrder appends a conditional order submission to the batch.
func (b *BatchBuilder) AddSubmitConditionalOrder(o clob.ConditionalOrder) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxSubmitConditionalOrder,
		Payload: SubmitConditionalOrderPayload{Order: o},
	})
	return b
}

// AddCancelConditionalOrder appends a conditional order cancellation to the batch.
func (b *BatchBuilder) AddCancelConditionalOrder(orderId, accountId string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:  TxCancelConditionalOrder,
		Payload: CancelConditionalOrderPayload{OrderId: orderId, AccountId: accountId},
	})
	return b
}

// BridgeAttestPayload carries a validator's attestation for a cross-chain deposit.
type BridgeAttestPayload struct {
	DepositId   string
	AccountId   string // destination account on the DEX
	AssetId     string
	Amount      int64
	SourceChain string
	ValidatorId string
}

// AddBridgeAttest appends a cross-chain bridge attestation to the batch.
func (b *BatchBuilder) AddBridgeAttest(depositId, accountId, assetId string, amount int64, sourceChain, validatorId string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType: TxBridgeAttest,
		Payload: BridgeAttestPayload{
			DepositId:   depositId,
			AccountId:   accountId,
			AssetId:     assetId,
			Amount:      amount,
			SourceChain: sourceChain,
			ValidatorId: validatorId,
		},
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
