package points

import (
	"sort"
)

// blocksPerDay is the number of blocks assumed per 24h period for daily cap resets.
// At 1s block time this is 86400; testnet uses 600 (10-min epochs).
const blocksPerDay int64 = 86_400

// PointsStore is the persistence interface for points data.
type PointsStore interface {
	GetAccountPoints(accountId string) *AccountPoints
	SetAccountPoints(p *AccountPoints)
	AllAccountPoints() []*AccountPoints
	GetRegistrationOrder() []string           // accounts in registration order
	AppendRegistrationOrder(accountId string) // append to registration order
	TotalAccounts() int64
}

// PointsKeeper manages trade points and referral accounting.
type PointsKeeper struct {
	store PointsStore
}

// NewPointsKeeper creates a new PointsKeeper backed by the given store.
func NewPointsKeeper(store PointsStore) *PointsKeeper {
	return &PointsKeeper{store: store}
}

// RecordTrade awards trade points to accountId and propagates referral bonus to referrer.
// blockHeight is used to enforce the daily points cap (resets every blocksPerDay blocks).
func (k *PointsKeeper) RecordTrade(accountId string, notional int64, isPerp, isMaker bool) {
	k.recordTradeInternal(accountId, notional, isPerp, isMaker, 0)
}

// RecordTradePair awards points for both sides of a trade, refusing to award either side if
// makerAccountId == takerAccountId (self-trade / wash-trade detection).
// blockHeight is used for daily cap enforcement.
func (k *PointsKeeper) RecordTradePair(makerAccountId, takerAccountId string, notional int64, isPerp bool, blockHeight int64) {
	if makerAccountId == takerAccountId {
		// Wash trade: zero points for self-trading accounts
		return
	}
	k.recordTradeInternal(makerAccountId, notional, isPerp, true, blockHeight)
	k.recordTradeInternal(takerAccountId, notional, isPerp, false, blockHeight)
}

func (k *PointsKeeper) recordTradeInternal(accountId string, notional int64, isPerp, isMaker bool, blockHeight int64) {
	ap := k.getOrCreate(accountId)

	pts := TradePoints(notional, isPerp, isMaker)

	// Apply early-bird multiplier
	if ap.IsEarlyBird {
		pts = pts * EarlyBirdMultiplier
	}

	// Daily cap: only enforce when blockHeight > 0 (not in legacy/no-context calls).
	if blockHeight > 0 {
		currentDay := blockHeight / blocksPerDay
		if currentDay != ap.LastSettledDay {
			ap.DailyPoints = 0
			ap.LastSettledDay = currentDay
		}
		remaining := MaxDailyPoints - ap.DailyPoints
		if remaining <= 0 {
			return
		}
		if pts > remaining {
			pts = remaining
		}
		ap.DailyPoints += pts
	}
	ap.TradePoints += pts
	ap.TotalPoints += pts
	k.store.SetAccountPoints(ap)

	// Award referral bonus to referrer if set
	if ap.ReferrerId != "" {
		referralPts := pts * ReferralPercent / 100
		if referralPts > 0 {
			ref := k.getOrCreate(ap.ReferrerId)
			ref.ReferralPoints += referralPts
			ref.TotalPoints += referralPts
			k.store.SetAccountPoints(ref)
		}
	}
}

// RegisterAccount marks an account for early-bird if within the limit, and sets its referrer.
func (k *PointsKeeper) RegisterAccount(accountId, referrerId string) {
	ap := k.store.GetAccountPoints(accountId)
	if ap != nil {
		// Already registered
		return
	}
	// New registration
	ap = &AccountPoints{
		AccountId: accountId,
	}

	// Check early-bird eligibility (first EarlyBirdLimit accounts)
	total := k.store.TotalAccounts()
	if total < EarlyBirdLimit {
		ap.IsEarlyBird = true
	}

	if referrerId != "" && referrerId != accountId {
		ap.ReferrerId = referrerId
	}

	k.store.SetAccountPoints(ap)
	k.store.AppendRegistrationOrder(accountId)
}

// SetReferrer sets the referrer for accountId (only if not already set).
func (k *PointsKeeper) SetReferrer(accountId, referrerId string) {
	if referrerId == "" || referrerId == accountId {
		return
	}
	ap := k.getOrCreate(accountId)
	if ap.ReferrerId != "" {
		return // already set, no-op
	}
	ap.ReferrerId = referrerId
	k.store.SetAccountPoints(ap)
}

// TGEAllocation computes FAIR token allocation for accountId based on points.
// totalFAIRForCommunity is the total FAIR pool for the community bucket.
func (k *PointsKeeper) TGEAllocation(accountId string, totalFAIRForCommunity int64) int64 {
	ap := k.store.GetAccountPoints(accountId)
	if ap == nil || ap.TotalPoints == 0 {
		return 0
	}

	// Sum all points across all accounts
	all := k.store.AllAccountPoints()
	var totalPoints int64
	for _, a := range all {
		totalPoints += a.TotalPoints
	}
	if totalPoints == 0 {
		return 0
	}

	return totalFAIRForCommunity * ap.TotalPoints / totalPoints
}

// Leaderboard returns top N accounts by total points (descending).
func (k *PointsKeeper) Leaderboard(n int) []*AccountPoints {
	all := k.store.AllAccountPoints()
	sort.Slice(all, func(i, j int) bool {
		return all[i].TotalPoints > all[j].TotalPoints
	})
	if n > len(all) {
		n = len(all)
	}
	return all[:n]
}

// getOrCreate returns the AccountPoints for accountId, creating a new entry if not found.
func (k *PointsKeeper) getOrCreate(accountId string) *AccountPoints {
	ap := k.store.GetAccountPoints(accountId)
	if ap == nil {
		ap = &AccountPoints{AccountId: accountId}
	}
	return ap
}
