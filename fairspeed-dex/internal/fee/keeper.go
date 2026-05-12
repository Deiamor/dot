package fee

import "fmt"

const TreasuryAccountId = "TREASURY"

type BalanceStore interface {
	GetBalance(accountId, assetId string) interface{ GetAvailable() int64 }
}

type FeeStore interface {
	DeductAvailable(accountId, assetId string, amount int64) error
	CreditAvailable(accountId, assetId string, amount int64)
}

type FeeEventPublisher interface {
	PublishFeeCharged(accountId, tradeId, assetId string, amount int64, feeType string, blockHeight int64)
}

type FeeKeeper struct {
	calc  *FeeCalculator
	store FeeStore
	bus   FeeEventPublisher
}

func NewFeeKeeper(calc *FeeCalculator, store FeeStore, bus FeeEventPublisher) *FeeKeeper {
	return &FeeKeeper{calc: calc, store: store, bus: bus}
}

func (k *FeeKeeper) ChargeFees(makerAccountId, takerAccountId, tradeId, feeAssetId string, makerFee, takerFee int64, blockHeight int64) error {
	if makerFee > 0 {
		if err := k.store.DeductAvailable(makerAccountId, feeAssetId, makerFee); err != nil {
			return fmt.Errorf("charging maker fee: %w", err)
		}
		k.store.CreditAvailable(TreasuryAccountId, feeAssetId, makerFee)
		k.bus.PublishFeeCharged(makerAccountId, tradeId, feeAssetId, makerFee, string(FeeTypeMaker), blockHeight)
	}
	if takerFee > 0 {
		if err := k.store.DeductAvailable(takerAccountId, feeAssetId, takerFee); err != nil {
			return fmt.Errorf("charging taker fee: %w", err)
		}
		k.store.CreditAvailable(TreasuryAccountId, feeAssetId, takerFee)
		k.bus.PublishFeeCharged(takerAccountId, tradeId, feeAssetId, takerFee, string(FeeTypeTaker), blockHeight)
	}
	return nil
}
