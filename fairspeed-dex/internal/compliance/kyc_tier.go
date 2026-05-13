package compliance

import (
	"fmt"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
)

// TierLimit defines the maximum single-order notional allowed for a
// (tier, jurisdiction) combination. Notional = price × quantity.
type TierLimit struct {
	Tier                   account.KYCTier
	Jurisdiction           account.Jurisdiction
	MaxSingleOrderNotional int64 // 0 = unlimited
}

// DefaultTierLimits encodes jurisdiction-specific tier ceilings.
// Values are in quote-asset base units (e.g. USDC micro-units).
//
//   Tier 1 — basic ID: max 10,000 per trade
//   Tier 2 — enhanced: max 100,000 per trade
//   Tier 3 — institutional: unlimited (0)
//   US jurisdiction applies tighter Tier 1 limits (FinCEN CTR threshold).
var DefaultTierLimits = []TierLimit{
	// US jurisdiction — stricter Tier 1 (align with $10k FinCEN threshold).
	{account.KYCTier1, account.JurisdictionUS, 10_000},
	{account.KYCTier2, account.JurisdictionUS, 100_000},
	{account.KYCTier3, account.JurisdictionUS, 0},
	// EU jurisdiction (MiCA).
	{account.KYCTier1, account.JurisdictionEU, 10_000},
	{account.KYCTier2, account.JurisdictionEU, 100_000},
	{account.KYCTier3, account.JurisdictionEU, 0},
	// APAC jurisdiction.
	{account.KYCTier1, account.JurisdictionAPAC, 10_000},
	{account.KYCTier2, account.JurisdictionAPAC, 100_000},
	{account.KYCTier3, account.JurisdictionAPAC, 0},
	// Default fallback.
	{account.KYCTier1, account.JurisdictionDefault, 10_000},
	{account.KYCTier2, account.JurisdictionDefault, 100_000},
	{account.KYCTier3, account.JurisdictionDefault, 0},
}

// KYCTierChecker enforces per-tier notional limits based on account jurisdiction.
type KYCTierChecker struct {
	limits []TierLimit
}

func NewKYCTierChecker(limits []TierLimit) *KYCTierChecker {
	return &KYCTierChecker{limits: limits}
}

// DefaultKYCTierChecker returns a checker using DefaultTierLimits.
func DefaultKYCTierChecker() *KYCTierChecker {
	return NewKYCTierChecker(DefaultTierLimits)
}

// CheckOrder returns an error when the trade's notional exceeds the limit
// for the account's KYC tier and jurisdiction.
// notional = price × quantity (already computed by caller).
func (c *KYCTierChecker) CheckOrder(tier account.KYCTier, jurisdiction account.Jurisdiction, notional int64) error {
	if tier == account.KYCTierNone {
		// No tier set — KYC must be enforced separately (RequireKYC policy).
		return nil
	}
	j := jurisdiction
	if j == "" {
		j = account.JurisdictionDefault
	}
	limit := c.limitFor(tier, j)
	if limit == nil {
		// Unknown tier/jurisdiction combo — no limit.
		return nil
	}
	if limit.MaxSingleOrderNotional > 0 && notional > limit.MaxSingleOrderNotional {
		return fmt.Errorf("KYC tier %d (%s) limit exceeded: notional=%d max=%d",
			tier, j, notional, limit.MaxSingleOrderNotional)
	}
	return nil
}

func (c *KYCTierChecker) limitFor(tier account.KYCTier, jurisdiction account.Jurisdiction) *TierLimit {
	// Exact match first.
	for i := range c.limits {
		if c.limits[i].Tier == tier && c.limits[i].Jurisdiction == jurisdiction {
			return &c.limits[i]
		}
	}
	// Fallback to DEFAULT jurisdiction.
	for i := range c.limits {
		if c.limits[i].Tier == tier && c.limits[i].Jurisdiction == account.JurisdictionDefault {
			return &c.limits[i]
		}
	}
	return nil
}
