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

