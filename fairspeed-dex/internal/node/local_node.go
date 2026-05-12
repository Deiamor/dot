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
	processor   *LocalBlockProcessor
	chain       []LocalBlock
	bus         *state.EventBus
	AppState    *state.AppState
	AssetKeeper *asset.AssetKeeper
}

func NewLocalNode() *LocalNode {
	appState := state.NewAppState()
	bus := state.NewEventBus()

	adapter := &busAdapter{bus: bus}

	assetKeeper := asset.NewAssetKeeper(appState)
	accountKeeper := account.NewAccountKeeper(appState, adapter)
	orderBookKeeper := clob.NewOrderBookKeeper(appState, appState, adapter)

	feeCalc := fee.NewFeeCalculator(fee.DefaultFeePolicy)
	settlementEngine := settlement.NewSettlementEngine(appState, feeCalc, adapter)
	settlementKeeper := settlement.NewSettlementKeeper()

	riskChecker := risk.NewRiskChecker(risk.DefaultRiskPolicy)
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
		processor:   processor,
		bus:         bus,
		AppState:    appState,
		AssetKeeper: assetKeeper,
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
