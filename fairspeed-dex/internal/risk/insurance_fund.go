package risk

import "sync"

// InsuranceFeeShareBps is the fraction of the taker fee credited to the insurance fund.
// 2000 bps = 20% of the taker fee (not 20% of notional).
const InsuranceFeeShareBps int64 = 2000

// InsuranceFund accumulates a share of taker fees to cover liquidation shortfalls.
// All balances are in quote-asset units (e.g., USDC).
type InsuranceFund struct {
	mu       sync.RWMutex
	balances map[string]int64 // assetId → available balance
}

func NewInsuranceFund() *InsuranceFund {
	return &InsuranceFund{balances: make(map[string]int64)}
}

// Deposit adds amount to the fund for the given asset.
func (f *InsuranceFund) Deposit(assetId string, amount int64) {
	if amount <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.balances[assetId] += amount
}

// Balance returns the current insurance fund balance for an asset.
func (f *InsuranceFund) Balance(assetId string) int64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.balances[assetId]
}

// Drawdown withdraws up to amount from the fund; returns the actual amount withdrawn.
func (f *InsuranceFund) Drawdown(assetId string, amount int64) int64 {
	if amount <= 0 {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	available := f.balances[assetId]
	if amount > available {
		amount = available
	}
	f.balances[assetId] -= amount
	return amount
}

// InsuranceShare returns the portion of takerFee that goes to the insurance fund.
// The remainder goes to the treasury.
func InsuranceShare(takerFee int64) int64 {
	return takerFee * InsuranceFeeShareBps / 10_000
}
