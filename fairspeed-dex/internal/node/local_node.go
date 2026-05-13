package node

import (
	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/fee"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

type LocalNode struct {
	processor      *LocalBlockProcessor
	chain          []LocalBlock
	bus            *state.EventBus
	AppState       *state.AppState
	AssetKeeper    *asset.AssetKeeper
	positionTracker *risk.PositionTracker
	insuranceFund  *risk.InsuranceFund
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
	matcher := clob.PricePriorityMatcher{}

	processor := &LocalBlockProcessor{
		AccountKeeper:    accountKeeper,
		AssetKeeper:      assetKeeper,
		OrderBookKeeper:  orderBookKeeper,
		MatchingEngine:   matcher,
		SettlementEngine: settlementEngine,
		SettlementKeeper: settlementKeeper,
		RiskChecker:      riskChecker,
		EventBus:         bus,
		AppState:         appState,
	}

	return &LocalNode{
		processor:       processor,
		bus:             bus,
		AppState:        appState,
		AssetKeeper:     assetKeeper,
		positionTracker: positionTracker,
		insuranceFund:   insuranceFund,
	}
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
