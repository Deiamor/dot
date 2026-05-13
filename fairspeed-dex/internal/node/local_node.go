package node

import (
	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/fee"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/validator"
)

type LocalNode struct {
	processor         *LocalBlockProcessor
	chain             []LocalBlock
	bus               *state.EventBus
	AppState          *state.AppState
	AssetKeeper       *asset.AssetKeeper
	positionTracker   *risk.PositionTracker
	insuranceFund     *risk.InsuranceFund
	validatorKeeper   *validator.ValidatorKeeper
	governanceKeeper  *governance.GovernanceKeeper
	feeCalc           *fee.FeeCalculator
	riskChecker       *risk.RiskChecker
}

func NewLocalNode() *LocalNode {
	return NewLocalNodeWithPolicy(risk.DefaultRiskPolicy)
}

// NewLocalNodeWithPolicy creates a LocalNode with a custom RiskPolicy.
func NewLocalNodeWithPolicy(policy risk.RiskPolicy) *LocalNode {
	appState := state.NewAppState()
	bus := state.NewEventBus()

	adapter := &busAdapter{bus: bus}

	assetKeeper := asset.NewAssetKeeper(appState)
	accountKeeper := account.NewAccountKeeper(appState, adapter)
	orderBookKeeper := clob.NewOrderBookKeeper(appState, appState, adapter)

	feeCalc := fee.NewFeeCalculator(fee.DefaultFeePolicy)
	positionTracker := risk.NewPositionTracker()
	insuranceFund := risk.NewInsuranceFund()
	settlementEngine := settlement.NewSettlementEngine(appState, feeCalc, adapter, insuranceFund, positionTracker)
	settlementKeeper := settlement.NewSettlementKeeper()

	riskChecker := risk.NewRiskChecker(policy, positionTracker)
	riskChecker.SetKYCStore(appState)        // AppState implements risk.KYCStore
	riskChecker.SetSanctionsStore(appState)  // AppState implements risk.SanctionsStore
	settlementEngine.SetAMLLimit(policy.AMLSingleTradeLimitNotional)
	matcher := clob.PricePriorityMatcher{}

	validatorKeeper := validator.NewValidatorKeeper(appState, adapter)
	governanceKeeper := governance.NewGovernanceKeeper(appState, adapter)

	n := &LocalNode{
		bus:              bus,
		AppState:         appState,
		AssetKeeper:      assetKeeper,
		positionTracker:  positionTracker,
		insuranceFund:    insuranceFund,
		validatorKeeper:  validatorKeeper,
		governanceKeeper: governanceKeeper,
		feeCalc:          feeCalc,
		riskChecker:      riskChecker,
	}

	processor := &LocalBlockProcessor{
		AccountKeeper:      accountKeeper,
		AssetKeeper:        assetKeeper,
		OrderBookKeeper:    orderBookKeeper,
		MatchingEngine:     matcher,
		SettlementEngine:   settlementEngine,
		SettlementKeeper:   settlementKeeper,
		RiskChecker:        riskChecker,
		ValidatorKeeper:    validatorKeeper,
		GovernanceKeeper:   governanceKeeper,
		GovernanceExecutor: n,
		SanctionsStore:     appState,
		EventBus:           bus,
		AppState:           appState,
	}
	n.processor = processor

	return n
}

func (n *LocalNode) SubmitBatch(batch fairbatch.FairBatch) (BlockResult, error) {
	nextHeight := n.AppState.CurrentHeight() + 1

	var parentHash string
	if len(n.chain) > 0 {
		parentHash = n.chain[len(n.chain)-1].Hash
	}

	proposal := ProposeBlock(nextHeight, batch, parentHash)
	result, err := n.processor.ProcessBlock(proposal.ProposedBlock)
	if err != nil {
		return result, err
	}

	n.chain = append(n.chain, proposal.ProposedBlock)
	return result, nil
}

func (n *LocalNode) CurrentHeight() int64 {
	return n.AppState.CurrentHeight()
}

func (n *LocalNode) Subscribe(eventType state.EventType, h state.Handler) {
	if eventType == state.EventAll {
		n.bus.SubscribeAll(h)
	} else {
		n.bus.Subscribe(eventType, h)
	}
}

func (n *LocalNode) RegisterAsset(a asset.Asset) {
	n.AssetKeeper.RegisterAsset(a)
}

func (n *LocalNode) GetBalance(accountId, assetId string) asset.Balance {
	return n.AssetKeeper.GetBalance(accountId, assetId)
}

func (n *LocalNode) GetTreasury(assetId string) asset.Balance {
	return n.AssetKeeper.GetBalance(fee.TreasuryAccountId, assetId)
}

// NotifyOrderReceived emits EventOrderReceived before the order enters a batch.
// Callers (API layer, tests) should call this when an order arrives externally.
func (n *LocalNode) NotifyOrderReceived(orderId, accountId, sessionId, marketId, side, txHash string, price, qty int64) {
	n.bus.Publish(state.Event{
		Type:        state.EventOrderReceived,
		BlockHeight: n.AppState.CurrentHeight(),
		Payload: state.OrderReceivedPayload{
			OrderId:   orderId,
			AccountId: accountId,
			SessionId: sessionId,
			MarketId:  marketId,
			Side:      side,
			Price:     price,
			Quantity:  qty,
			TxHash:    txHash,
		},
	})
}

// GetOrderBook returns the current in-memory orderbook for a market.
func (n *LocalNode) GetOrderBook(marketId string) (*clob.OrderBook, bool) {
	return n.AppState.GetOrderBook(marketId)
}

// AllTrades returns all trades ever executed on this node.
func (n *LocalNode) AllTrades() []settlement.TradeExecution {
	return n.processor.SettlementKeeper.AllTrades()
}

// TradesForMarket returns all trades for a specific market.
func (n *LocalNode) TradesForMarket(marketId string) []settlement.TradeExecution {
	all := n.processor.SettlementKeeper.AllTrades()
	var result []settlement.TradeExecution
	for _, t := range all {
		if t.MarketId == marketId {
			result = append(result, t)
		}
	}
	return result
}

// SubmitOrderImmediate builds a single-tx batch and processes it immediately.
// Block height is auto-incremented. Returns the order status and any trades.
func (n *LocalNode) SubmitOrderImmediate(o clob.Order) (BlockResult, error) {
	nextHeight := n.AppState.CurrentHeight() + 1
	batch := fairbatch.NewBatchBuilder(nextHeight).AddSubmitOrder(o).Build()
	return n.SubmitBatch(batch)
}

// Publish exposes the EventBus for direct event emission (used by API layer).
func (n *LocalNode) Publish(e state.Event) {
	n.bus.Publish(e)
}

// GetSession returns the TradingSession for sessionId.
func (n *LocalNode) GetSession(sessionId string) (*account.TradingSession, bool) {
	return n.AppState.GetSession(sessionId)
}

// GetAccountSequence returns the current sequence number for accountId (0 if not found).
func (n *LocalNode) GetAccountSequence(accountId string) uint64 {
	acc, ok := n.AppState.GetAccount(accountId)
	if !ok {
		return 0
	}
	return acc.AccountSequence
}

// GetKYCStatus returns the current KYC status for an account.
func (n *LocalNode) GetKYCStatus(accountId string) account.KYCStatus {
	return n.AppState.GetKYCStatus(accountId)
}

// ActiveValidatorSet returns all BONDED validators sorted by descending stake.
func (n *LocalNode) ActiveValidatorSet() []validator.Validator {
	return n.validatorKeeper.ActiveSet()
}

// GetValidatorStake returns the current stake for a validator (0 if not found).
func (n *LocalNode) GetValidatorStake(validatorId string) int64 {
	v, ok := n.validatorKeeper.GetValidator(validatorId)
	if !ok {
		return 0
	}
	return v.Stake
}

// GetValidatorInfo returns the full validator record for the given ID.
func (n *LocalNode) GetValidatorInfo(validatorId string) (validator.Validator, bool) {
	return n.validatorKeeper.GetValidator(validatorId)
}

// SlashValidator slashes a validator for the given reason ("DOUBLE_SIGN" or "FRONT_RUN").
// Returns the amount slashed.
func (n *LocalNode) SlashValidator(validatorId, reason string) (int64, error) {
	height := n.AppState.CurrentHeight()
	switch reason {
	case "DOUBLE_SIGN":
		return n.validatorKeeper.SlashDoubleSign(validatorId, height)
	case "FRONT_RUN":
		return n.validatorKeeper.SlashFrontRun(validatorId, height)
	default:
		return n.validatorKeeper.SlashDoubleSign(validatorId, height)
	}
}

// SlashDoubleSign directly satisfies the evidence.Slasher interface.
func (n *LocalNode) SlashDoubleSign(validatorId string, blockHeight int64) (int64, error) {
	return n.validatorKeeper.SlashDoubleSign(validatorId, blockHeight)
}

// SlashFrontRun directly satisfies the evidence.Slasher interface.
func (n *LocalNode) SlashFrontRun(validatorId string, blockHeight int64) (int64, error) {
	return n.validatorKeeper.SlashFrontRun(validatorId, blockHeight)
}

// TotalValidatorStake returns the sum of all bonded stake.
func (n *LocalNode) TotalValidatorStake() int64 {
	return n.validatorKeeper.TotalStake()
}

// --- governance.ParameterExecutor ---

// UpdateFeePolicy applies an approved fee policy change immediately.
func (n *LocalNode) UpdateFeePolicy(params governance.UpdateFeePolicyParams) error {
	n.feeCalc.SetPolicy(fee.FeeBps{MakerBps: params.MakerBps, TakerBps: params.TakerBps})
	return nil
}

// UpdateRiskPolicy applies an approved risk policy change immediately.
func (n *LocalNode) UpdateRiskPolicy(params governance.UpdateRiskPolicyParams) error {
	n.riskChecker.SetPolicy(risk.RiskPolicy{
		MaxOrderQuantity:            params.MaxOrderQuantity,
		MinOrderQuantity:            params.MinOrderQuantity,
		MaxDailyVolumePerSession:    params.MaxDailyVolumePerSession,
		MaxPositionSize:             params.MaxPositionSize,
		RequireKYC:                  params.RequireKYC,
		AMLSingleTradeLimitNotional: params.AMLSingleTradeLimitNotional,
	})
	return nil
}

// ListMarket registers the base and quote assets for a new market.
func (n *LocalNode) ListMarket(params governance.ListMarketParams) error {
	if params.BaseAsset != "" {
		n.AssetKeeper.RegisterAsset(asset.Asset{AssetId: params.BaseAsset, Symbol: params.BaseAsset, Decimals: 8})
	}
	if params.QuoteAsset != "" {
		n.AssetKeeper.RegisterAsset(asset.Asset{AssetId: params.QuoteAsset, Symbol: params.QuoteAsset, Decimals: 6})
	}
	return nil
}

// IsSanctioned returns true if the account is on the on-chain sanctions list.
func (n *LocalNode) IsSanctioned(accountId string) bool {
	return n.AppState.IsSanctioned(accountId)
}

// AllSanctions returns all current on-chain sanction entries.
func (n *LocalNode) AllSanctions() []compliance.SanctionEntry {
	return n.AppState.AllSanctions()
}

// AllProposals returns all governance proposals.
func (n *LocalNode) AllProposals() []governance.Proposal {
	return n.governanceKeeper.AllProposals()
}

// GetProposal returns a proposal by ID.
func (n *LocalNode) GetProposal(proposalId string) (governance.Proposal, bool) {
	return n.governanceKeeper.GetProposal(proposalId)
}

// GetInsuranceFundBalance returns the current insurance fund balance for an asset.
func (n *LocalNode) GetInsuranceFundBalance(assetId string) int64 {
	return n.insuranceFund.Balance(assetId)
}

// GetPosition returns the net position for an account in a market.
func (n *LocalNode) GetPosition(accountId, marketId string) risk.NetPosition {
	return n.positionTracker.Get(accountId, marketId)
}

// SaveSnapshot persists the current state to the given file path.
func (n *LocalNode) SaveSnapshot(path string) error {
	return n.AppState.SaveSnapshot(path)
}

// LoadSnapshot restores state from a previously saved snapshot file.
func (n *LocalNode) LoadSnapshot(path string) error {
	return n.AppState.LoadSnapshot(path)
}
