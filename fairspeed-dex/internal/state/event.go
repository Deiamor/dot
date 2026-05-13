package state

type EventType string

const (
	EventAccountCreated   EventType = "ACCOUNT_CREATED"
	EventSessionCreated   EventType = "SESSION_CREATED"
	EventDepositSimulated EventType = "DEPOSIT_SIMULATED"

	// Order lifecycle events (in order of occurrence)
	EventOrderReceived  EventType = "ORDER_RECEIVED"  // order arrived at the node (pre-block)
	EventOrderIncluded  EventType = "ORDER_INCLUDED"  // order included in a block batch
	EventOrderSubmitted EventType = "ORDER_SUBMITTED" // order processed by block (resting or immediate)
	EventOrderRejected  EventType = "ORDER_REJECTED"  // order rejected (risk, session, or FOK)
	EventOrderCancelled EventType = "ORDER_CANCELLED"
	EventOrderExpired   EventType = "ORDER_EXPIRED"
	EventTradeExecuted  EventType = "TRADE_EXECUTED"
	EventBalanceUpdated EventType = "BALANCE_UPDATED"
	EventFeeCharged     EventType = "FEE_CHARGED"
	EventBlockProcessed        EventType = "BLOCK_PROCESSED"
	EventWithdrawal            EventType = "WITHDRAWAL"
	EventInsuranceFundDeposit  EventType = "INSURANCE_FUND_DEPOSIT"
	EventPositionUpdated       EventType = "POSITION_UPDATED"
	EventKYCStatusUpdated      EventType = "KYC_STATUS_UPDATED"
	EventAMLAlert              EventType = "AML_ALERT"
	EventValidatorBonded       EventType = "VALIDATOR_BONDED"
	EventValidatorSlashed      EventType = "VALIDATOR_SLASHED"
	EventValidatorUnbonded     EventType = "VALIDATOR_UNBONDED"
	EventProposalSubmitted     EventType = "PROPOSAL_SUBMITTED"
	EventVoteCast              EventType = "VOTE_CAST"
	EventProposalPassed        EventType = "PROPOSAL_PASSED"
	EventProposalRejected      EventType = "PROPOSAL_REJECTED"
	EventProposalExecuted      EventType = "PROPOSAL_EXECUTED"

	EventMarketHalted     EventType = "MARKET_HALTED"
	EventMarketResumed    EventType = "MARKET_RESUMED"
	EventMarkPriceUpdated       EventType = "MARK_PRICE_UPDATED"
	EventWithdrawRequested      EventType = "WITHDRAW_REQUESTED"
	EventWithdrawFinalized      EventType = "WITHDRAW_FINALIZED"
	EventFeeDistributed         EventType = "FEE_DISTRIBUTED"

	EventBridgeAttested  EventType = "BRIDGE_ATTESTED"
	EventBridgeCompleted EventType = "BRIDGE_COMPLETED"

	EventPerpMarketRegistered   EventType = "PERP_MARKET_REGISTERED"
	EventPerpPositionUpdated    EventType = "PERP_POSITION_UPDATED"
	EventFundingSettled         EventType = "FUNDING_SETTLED"
	EventLiquidationTriggered       EventType = "LIQUIDATION_TRIGGERED"
	EventLiquidationFilled          EventType = "LIQUIDATION_FILLED"
	EventInsuranceDrawdown          EventType = "INSURANCE_DRAWDOWN"
	EventSocializedLoss             EventType = "SOCIALIZED_LOSS"
	EventConditionalOrderSubmitted  EventType = "CONDITIONAL_ORDER_SUBMITTED"
	EventConditionalOrderTriggered  EventType = "CONDITIONAL_ORDER_TRIGGERED"
	EventConditionalOrderExpired    EventType = "CONDITIONAL_ORDER_EXPIRED"
	EventConditionalOrderCancelled  EventType = "CONDITIONAL_ORDER_CANCELLED"

	EventPointsAwarded    EventType = "POINTS_AWARDED"
	EventReferralRecorded EventType = "REFERRAL_RECORDED"
	EventTGEAllocated     EventType = "TGE_ALLOCATED"

	EventIndexPriceUpdated EventType = "INDEX_PRICE_UPDATED"

	EventAll EventType = "*"
)

type Event struct {
	Type        EventType
	BlockHeight int64
	Payload     any
}

type AccountCreatedPayload struct {
	AccountId    string
	OwnerAddress string
}

type SessionCreatedPayload struct {
	SessionId string
	AccountId string
}

type DepositSimulatedPayload struct {
	AccountId string
	AssetId   string
	Amount    int64
}

type OrderReceivedPayload struct {
	OrderId   string
	AccountId string
	SessionId string
	MarketId  string
	Side      string
	Price     int64
	Quantity  int64
	TxHash    string
}

type OrderIncludedPayload struct {
	OrderId     string
	AccountId   string
	MarketId    string
	BlockHeight int64
	TxHash      string
}

type OrderRejectedPayload struct {
	OrderId   string
	AccountId string
	MarketId  string
	Reason    string
}

type OrderSubmittedPayload struct {
	OrderId   string
	AccountId string
	MarketId  string
	Status    string
}

type OrderCancelledPayload struct {
	OrderId   string
	AccountId string
}

type OrderExpiredPayload struct {
	OrderId   string
	AccountId string
}

type TradeExecutedPayload struct {
	TradeId        string
	MarketId       string
	MakerOrderId   string
	TakerOrderId   string
	MakerAccountId string
	TakerAccountId string
	Price          int64
	Quantity       int64
	MakerFeeAmount int64
	TakerFeeAmount int64
}

type BalanceUpdatedPayload struct {
	AccountId    string
	AssetId      string
	NewAvailable int64
	NewReserved  int64
}

type FeeChargedPayload struct {
	AccountId string
	TradeId   string
	AssetId   string
	Amount    int64
	FeeType   string
}

type BlockProcessedPayload struct {
	BlockHeight int64
	TxCount     int
	TradeCount  int
}

type WithdrawalPayload struct {
	AccountId string
	AssetId   string
	Amount    int64
}

type InsuranceFundDepositPayload struct {
	AssetId string
	Amount  int64
}

type PositionUpdatedPayload struct {
	AccountId   string
	MarketId    string
	NetQuantity int64
}

type KYCStatusUpdatedPayload struct {
	AccountId string
	Status    string
}

type AMLAlertPayload struct {
	TradeId   string
	MarketId  string
	Notional  int64
	Threshold int64
}

type ValidatorBondedPayload struct {
	ValidatorId string
	Moniker     string
	Stake       int64
}

type ValidatorSlashedPayload struct {
	ValidatorId    string
	Reason         string
	SlashedAmount  int64
	RemainingStake int64
}

type ValidatorUnbondedPayload struct {
	ValidatorId string
}

type ProposalSubmittedPayload struct {
	ProposalId   string
	ProposalType string
	Title        string
}

type VoteCastPayload struct {
	ProposalId  string
	ValidatorId string
	Choice      string
	Stake       int64
}

type ProposalPassedPayload struct {
	ProposalId string
}

type ProposalRejectedPayload struct {
	ProposalId string
}

type ProposalExecutedPayload struct {
	ProposalId string
}

type MarketHaltedPayload struct {
	MarketId    string
	Reason      string
	BlockHeight int64
}

type MarketResumedPayload struct {
	MarketId    string
	BlockHeight int64
}

type MarkPriceUpdatedPayload struct {
	MarketId    string
	MarkPrice   int64
	BlockHeight int64
}

type IndexPriceUpdatedPayload struct {
	MarketId    string
	IndexPrice  int64
	ValidatorId string
	Source      string
	BlockHeight int64
}

type WithdrawRequestedPayload struct {
	WithdrawalId  string
	AccountId     string
	AssetId       string
	Amount        int64
	ReadyAtHeight int64
}

type WithdrawFinalizedPayload struct {
	WithdrawalId string
	AccountId    string
	AssetId      string
	Amount       int64
	BlockHeight  int64
}

// FeeDistributedPayload describes a single fee distribution to one validator.
type FeeDistributedPayload struct {
	ValidatorId string
	AssetId     string
	Amount      int64
	BlockHeight int64
}

// PerpPositionUpdatedPayload is emitted when a PERP position changes.
type PerpPositionUpdatedPayload struct {
	AccountId       string
	MarketId        string
	NetQuantity     int64
	AvgEntryPrice   int64
	AllocatedMargin int64
	BlockHeight     int64
}

// PerpMarketRegisteredPayload is emitted when a perpetual market is registered.
type PerpMarketRegisteredPayload struct {
	MarketId              string
	BaseAsset             string
	QuoteAsset            string
	InitialMarginBps      int64
	MaintenanceMarginBps  int64
	MaxLeverage           int64
	FundingIntervalBlocks int64
	MaxFundingRateBps     int64
}

// BridgeAttestedPayload is emitted when a validator attests a cross-chain deposit.
type BridgeAttestedPayload struct {
	DepositId      string
	ValidatorId    string
	AttestedStake  int64
	TotalStake     int64
	BlockHeight    int64
}

// BridgeCompletedPayload is emitted when a bridge deposit reaches ⅔ quorum and funds are credited.
type BridgeCompletedPayload struct {
	DepositId   string
	AccountId   string
	AssetId     string
	Amount      int64
	BlockHeight int64
}

// ConditionalOrderSubmittedPayload is emitted when a conditional order is accepted.
type ConditionalOrderSubmittedPayload struct {
	OrderId   string
	AccountId string
	MarketId  string
}

// ConditionalOrderTriggeredPayload is emitted when a conditional order's condition is met.
type ConditionalOrderTriggeredPayload struct {
	OrderId   string
	AccountId string
	MarketId  string
	MarkPrice int64
}

// ConditionalOrderExpiredPayload is emitted when an order expires without triggering.
type ConditionalOrderExpiredPayload struct {
	OrderId   string
	AccountId string
	MarketId  string
}

// ConditionalOrderCancelledPayload is emitted when a conditional order is cancelled.
type ConditionalOrderCancelledPayload struct {
	OrderId   string
	AccountId string
}

// LiquidationTriggeredPayload is emitted when a position breaches maintenance margin.
type LiquidationTriggeredPayload struct {
	AccountId         string
	MarketId          string
	NetQuantity       int64
	MarkPrice         int64
	MaintenanceMargin int64
	BlockHeight       int64
}

// LiquidationFilledPayload is emitted when a liquidation is completed.
type LiquidationFilledPayload struct {
	AccountId   string
	MarketId    string
	FilledQty   int64
	FilledPrice int64
	PnL         int64
	BlockHeight int64
}

// InsuranceDrawdownPayload is emitted when the insurance fund absorbs a liquidation loss.
type InsuranceDrawdownPayload struct {
	MarketId    string
	Amount      int64
	Remaining   int64
	BlockHeight int64
}

// SocializedLossPayload is emitted when insurance fund is exhausted during liquidation.
type SocializedLossPayload struct {
	MarketId    string
	LossAmount  int64
	BlockHeight int64
}

// FundingSettledPayload is emitted after each funding epoch settlement.
type FundingSettledPayload struct {
	MarketId            string
	RateBps             int64 // signed: positive = longs pay, negative = shorts pay
	MarkPrice           int64
	TotalLongsPaid      int64
	TotalShortsReceived int64
	BlockHeight         int64
}

// PointsAwardedPayload is emitted when points are awarded to an account.
type PointsAwardedPayload struct {
	AccountId   string
	Points      int64
	TotalPoints int64
	Reason      string // "TRADE" | "REFERRAL" | "BONUS"
}

// TGEAllocatedPayload is emitted when a TGE allocation is computed for an account.
type TGEAllocatedPayload struct {
	AccountId  string
	FAIRAmount int64
	Points     int64
}

