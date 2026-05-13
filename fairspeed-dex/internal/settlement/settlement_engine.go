package settlement

import (
	"fmt"
	"strings"

	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fee"
)

type SettlementStore interface {
	GetBalance(accountId, assetId string) *asset.Balance
	SetBalance(b *asset.Balance)
	Reserve(accountId, assetId string, amount int64) error
	Release(accountId, assetId string, amount int64) error
}

type SettlementEventPublisher interface {
	PublishTradeExecuted(trade TradeExecution, blockHeight int64)
	PublishBalanceUpdated(accountId, assetId string, newAvailable, newReserved, blockHeight int64)
	PublishFeeCharged(accountId, tradeId, assetId string, amount int64, feeType string, blockHeight int64)
	PublishInsuranceFundDeposit(assetId string, amount int64, blockHeight int64)
	PublishPositionUpdated(accountId, marketId string, netQuantity int64, blockHeight int64)
	PublishAMLAlert(tradeId, marketId string, notional, threshold int64, blockHeight int64)
}

// InsuranceFundDepositor receives a portion of each taker fee.
// Implemented by risk.InsuranceFund; nil means disabled.
type InsuranceFundDepositor interface {
	Deposit(assetId string, amount int64)
}

// PositionUpdater records position changes after each trade.
// Implemented by risk.PositionTracker; nil means disabled.
type PositionUpdater interface {
	ApplyTrade(buyerAccountId, sellerAccountId, marketId string, qty int64)
}

type SettlementEngine struct {
	store     SettlementStore
	feeCalc   *fee.FeeCalculator
	bus       SettlementEventPublisher
	insurance InsuranceFundDepositor
	positions PositionUpdater
	// amlLimit fires an AML alert when a trade's notional (price×qty) exceeds this.
	// 0 = disabled.
	amlLimit int64
}

func NewSettlementEngine(
	store SettlementStore,
	feeCalc *fee.FeeCalculator,
	bus SettlementEventPublisher,
	insurance InsuranceFundDepositor,
	positions PositionUpdater,
) *SettlementEngine {
	return &SettlementEngine{store: store, feeCalc: feeCalc, bus: bus, insurance: insurance, positions: positions}
}

// SetAMLLimit configures the notional threshold above which trades trigger an AML alert.
func (e *SettlementEngine) SetAMLLimit(limit int64) {
	e.amlLimit = limit
}

// Settle applies each MatchResult to balances and returns the resulting TradeExecutions.
//
// Fee model (all fees in quote asset):
//   - From buyer's reserved quote: takerFee split → insurance share + treasury, remainder to seller
//   - From seller's received quote: makerFee → treasury
//   - Seller's reserved base goes to buyer in full
func (e *SettlementEngine) Settle(results []clob.MatchResult, blockHeight int64) ([]TradeExecution, error) {
	trades := make([]TradeExecution, 0, len(results))
	for _, r := range results {
		trade, err := e.settleTrade(r, blockHeight)
		if err != nil {
			return trades, fmt.Errorf("settling trade %s/%s: %w", r.MakerOrderId, r.TakerOrderId, err)
		}
		trades = append(trades, trade)
	}
	return trades, nil
}

func (e *SettlementEngine) settleTrade(r clob.MatchResult, blockHeight int64) (TradeExecution, error) {
	baseAsset, quoteAsset, err := parseMarket(r.MarketId)
	if err != nil {
		return TradeExecution{}, err
	}

	makerFee, takerFee := e.feeCalc.CalcFees(r.Price, r.Quantity)
	trade := NewTradeExecution(r, makerFee, takerFee)

	quoteAmount := r.Price * r.Quantity

	// Determine buyer and seller: whichever account has base asset reserved is the seller.
	makerHasBaseReserved := e.store.GetBalance(r.MakerAccountId, baseAsset).Reserved >= r.Quantity

	var sellerAccountId, buyerAccountId string
	if makerHasBaseReserved {
		sellerAccountId = r.MakerAccountId
		buyerAccountId = r.TakerAccountId
	} else {
		buyerAccountId = r.MakerAccountId
		sellerAccountId = r.TakerAccountId
	}

	// 1. Transfer base (BTC) from seller.Reserved → buyer.Available (full quantity, no fee).
	if err := e.transferReserved(sellerAccountId, buyerAccountId, baseAsset, r.Quantity, blockHeight); err != nil {
		return TradeExecution{}, fmt.Errorf("transferring base: %w", err)
	}

	// 2. From buyer.Reserved quote:
	//    - insuranceShare → insurance fund
	//    - remainder of takerFee → treasury
	//    - quoteAmount - takerFee → seller.Available
	if err := e.transferReservedNetFee(buyerAccountId, sellerAccountId, quoteAsset, quoteAmount, takerFee, trade.TradeId, string(fee.FeeTypeTaker), blockHeight); err != nil {
		return TradeExecution{}, fmt.Errorf("transferring quote: %w", err)
	}

	// 3. From seller.Available (just received):
	//    - makerFee → treasury
	if makerFee > 0 {
		if err := e.chargeFeeFromAvailable(sellerAccountId, quoteAsset, makerFee, trade.TradeId, string(fee.FeeTypeMaker), blockHeight); err != nil {
			return TradeExecution{}, fmt.Errorf("charging maker fee: %w", err)
		}
	}

	// 4. Update position tracker.
	if e.positions != nil {
		e.positions.ApplyTrade(buyerAccountId, sellerAccountId, r.MarketId, r.Quantity)
		e.bus.PublishPositionUpdated(buyerAccountId, r.MarketId, 0, blockHeight) // net computed by tracker
		e.bus.PublishPositionUpdated(sellerAccountId, r.MarketId, 0, blockHeight)
	}

	// 5. AML alert if notional breaches threshold.
	if e.amlLimit > 0 && quoteAmount > e.amlLimit {
		e.bus.PublishAMLAlert(trade.TradeId, r.MarketId, quoteAmount, e.amlLimit, blockHeight)
	}

	e.bus.PublishTradeExecuted(trade, blockHeight)
	return trade, nil
}

// transferReserved moves amount from sender's Reserved → recipient's Available.
func (e *SettlementEngine) transferReserved(fromId, toId, assetId string, amount, blockHeight int64) error {
	from := e.store.GetBalance(fromId, assetId)
	if from.Reserved < amount {
		return fmt.Errorf("insufficient reserved: account=%s asset=%s reserved=%d required=%d",
			fromId, assetId, from.Reserved, amount)
	}
	updatedFrom := &asset.Balance{
		AccountId: fromId,
		AssetId:   assetId,
		Available: from.Available,
		Reserved:  from.Reserved - amount,
	}
	e.store.SetBalance(updatedFrom)

	to := e.store.GetBalance(toId, assetId)
	updatedTo := &asset.Balance{
		AccountId: toId,
		AssetId:   assetId,
		Available: to.Available + amount,
		Reserved:  to.Reserved,
	}
	e.store.SetBalance(updatedTo)

	e.bus.PublishBalanceUpdated(fromId, assetId, updatedFrom.Available, updatedFrom.Reserved, blockHeight)
	e.bus.PublishBalanceUpdated(toId, assetId, updatedTo.Available, updatedTo.Reserved, blockHeight)
	return nil
}

// transferReservedNetFee splits buyer's reserved:
//   - insuranceShare of takerFee → insurance fund
//   - takerFee - insuranceShare → treasury
//   - quoteAmount - takerFee → seller.Available
func (e *SettlementEngine) transferReservedNetFee(fromId, toId, assetId string, quoteAmount, feeAmount int64, tradeId, feeType string, blockHeight int64) error {
	from := e.store.GetBalance(fromId, assetId)
	if from.Reserved < quoteAmount {
		return fmt.Errorf("insufficient reserved: account=%s asset=%s reserved=%d required=%d",
			fromId, assetId, from.Reserved, quoteAmount)
	}

	netToSeller := quoteAmount - feeAmount

	// Split taker fee between insurance and treasury.
	var insuranceAmount int64
	if e.insurance != nil {
		insuranceAmount = feeAmount * 2000 / 10_000 // 20% of taker fee
	}
	treasuryAmount := feeAmount - insuranceAmount

	updatedFrom := &asset.Balance{
		AccountId: fromId,
		AssetId:   assetId,
		Available: from.Available,
		Reserved:  from.Reserved - quoteAmount,
	}
	e.store.SetBalance(updatedFrom)

	// Credit seller.
	to := e.store.GetBalance(toId, assetId)
	updatedTo := &asset.Balance{
		AccountId: toId,
		AssetId:   assetId,
		Available: to.Available + netToSeller,
		Reserved:  to.Reserved,
	}
	e.store.SetBalance(updatedTo)

	// Credit treasury with remaining taker fee.
	if treasuryAmount > 0 {
		treasury := e.store.GetBalance(fee.TreasuryAccountId, assetId)
		updatedTreasury := &asset.Balance{
			AccountId: fee.TreasuryAccountId,
			AssetId:   assetId,
			Available: treasury.Available + treasuryAmount,
			Reserved:  treasury.Reserved,
		}
		e.store.SetBalance(updatedTreasury)
	}

	// Credit insurance fund.
	if insuranceAmount > 0 {
		e.insurance.Deposit(assetId, insuranceAmount)
		e.bus.PublishInsuranceFundDeposit(assetId, insuranceAmount, blockHeight)
	}

	if feeAmount > 0 {
		e.bus.PublishFeeCharged(fromId, tradeId, assetId, feeAmount, feeType, blockHeight)
	}

	e.bus.PublishBalanceUpdated(fromId, assetId, updatedFrom.Available, updatedFrom.Reserved, blockHeight)
	e.bus.PublishBalanceUpdated(toId, assetId, updatedTo.Available, updatedTo.Reserved, blockHeight)
	return nil
}

// chargeFeeFromAvailable deducts feeAmount from account's Available balance and credits treasury.
func (e *SettlementEngine) chargeFeeFromAvailable(accountId, assetId string, feeAmount int64, tradeId, feeType string, blockHeight int64) error {
	b := e.store.GetBalance(accountId, assetId)
	if b.Available < feeAmount {
		feeAmount = b.Available
	}
	updated := &asset.Balance{
		AccountId: accountId,
		AssetId:   assetId,
		Available: b.Available - feeAmount,
		Reserved:  b.Reserved,
	}
	e.store.SetBalance(updated)

	treasury := e.store.GetBalance(fee.TreasuryAccountId, assetId)
	updatedTreasury := &asset.Balance{
		AccountId: fee.TreasuryAccountId,
		AssetId:   assetId,
		Available: treasury.Available + feeAmount,
		Reserved:  treasury.Reserved,
	}
	e.store.SetBalance(updatedTreasury)

	e.bus.PublishFeeCharged(accountId, tradeId, assetId, feeAmount, feeType, blockHeight)
	e.bus.PublishBalanceUpdated(accountId, assetId, updated.Available, updated.Reserved, blockHeight)
	return nil
}

func parseMarket(marketId string) (baseAsset, quoteAsset string, err error) {
	parts := strings.SplitN(marketId, "-", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid marketId format: %s (expected BASE-QUOTE)", marketId)
	}
	return parts[0], parts[1], nil
}
