package risk

import (
	"fmt"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
)

// KYCStore allows the risk checker to read KYC status without importing AppState directly.
type KYCStore interface {
	GetKYCStatus(accountId string) account.KYCStatus
}

// SanctionsStore allows the risk checker to check sanctions without importing AppState.
type SanctionsStore interface {
	IsSanctioned(accountId string) bool
}

// AccountTierStore lets the risk checker read KYC tier and jurisdiction without
// importing AppState directly.
type AccountTierStore interface {
	GetAccountTier(accountId string) (account.KYCTier, account.Jurisdiction)
}

// RateLimitStore lets the risk checker enforce per-block order counts without
// importing AppState directly.
type RateLimitStore interface {
	IncrementOrderCount(accountId string) int64
}

// PerpConfigStore lets the risk checker access perpetual market parameters.
type PerpConfigStore interface {
	GetPerpConfig(marketId string) (*clob.PerpConfig, bool)
}

type RiskChecker struct {
	policy           RiskPolicy
	tracker          *PositionTracker
	kycStore         KYCStore                   // nil when RequireKYC == false
	sanctionsStore   SanctionsStore             // nil when no sanctions list is configured
	kycTierChecker   *compliance.KYCTierChecker // nil when tier limits are not configured
	accountTierStore AccountTierStore           // nil when kycTierChecker is nil
	rateLimitStore   RateLimitStore             // nil when MaxOrdersPerBlock == 0
	perpStore        PerpConfigStore            // nil when perp markets not configured
}

func NewRiskChecker(policy RiskPolicy, tracker *PositionTracker) *RiskChecker {
	return &RiskChecker{policy: policy, tracker: tracker}
}

// SetKYCStore wires the KYC store used when RequireKYC == true.
func (r *RiskChecker) SetKYCStore(store KYCStore) {
	r.kycStore = store
}

// SetSanctionsStore wires the sanctions store. When set, every order from a
// sanctioned account is rejected regardless of other risk parameters.
func (r *RiskChecker) SetSanctionsStore(store SanctionsStore) {
	r.sanctionsStore = store
}

// SetKYCTierChecker wires the KYC tier limit checker. Requires accountTierStore
// so the checker can look up the account's tier and jurisdiction.
func (r *RiskChecker) SetKYCTierChecker(checker *compliance.KYCTierChecker, store AccountTierStore) {
	r.kycTierChecker = checker
	r.accountTierStore = store
}

// SetRateLimitStore wires the per-block order counter store.
func (r *RiskChecker) SetRateLimitStore(store RateLimitStore) {
	r.rateLimitStore = store
}

// SetPerpConfigStore wires the perpetual market config store.
func (r *RiskChecker) SetPerpConfigStore(store PerpConfigStore) {
	r.perpStore = store
}

// SetPolicy replaces the active risk policy. Called by governance on proposal execution.
func (r *RiskChecker) SetPolicy(p RiskPolicy) {
	r.policy = p
}

// WithdrawTimelockBlocks returns the configured timelock delay for large withdrawals.
func (r *RiskChecker) WithdrawTimelockBlocks() int64 {
	return r.policy.WithdrawTimelockBlocks
}

// WithdrawTimelockThreshold returns the amount above which a timelock applies.
func (r *RiskChecker) WithdrawTimelockThreshold() int64 {
	return r.policy.WithdrawTimelockThreshold
}

func (r *RiskChecker) CheckOrder(o *clob.Order, sess *account.TradingSession, blockHeight int64) error {
	if o.Quantity < r.policy.MinOrderQuantity {
		return fmt.Errorf("order quantity %d below minimum %d", o.Quantity, r.policy.MinOrderQuantity)
	}
	if o.Quantity > r.policy.MaxOrderQuantity {
		return fmt.Errorf("order quantity %d exceeds maximum %d", o.Quantity, r.policy.MaxOrderQuantity)
	}
	if sess.MaxOrderAmount > 0 && o.Quantity > sess.MaxOrderAmount {
		return fmt.Errorf("order quantity %d exceeds session limit %d", o.Quantity, sess.MaxOrderAmount)
	}
	if !sess.IsEnabled {
		return fmt.Errorf("session %s is disabled", sess.SessionId)
	}
	if sess.IsExpiredAt(blockHeight) {
		return fmt.Errorf("session %s expired at block %d", sess.SessionId, sess.ExpiresAtBlockHeight)
	}
	if !sess.CanTradeMarket(o.MarketId) {
		return fmt.Errorf("session %s not allowed for market %s", sess.SessionId, o.MarketId)
	}
	if err := r.checkSanctions(o.AccountId); err != nil {
		return err
	}
	if err := r.checkKYC(o.AccountId); err != nil {
		return err
	}
	if err := r.checkKYCTierLimit(o.AccountId, o.Price*o.Quantity); err != nil {
		return err
	}
	if err := r.checkRateLimit(o.AccountId); err != nil {
		return err
	}
	if err := r.checkPositionLimit(o); err != nil {
		return err
	}
	if err := r.checkPerpLeverage(o); err != nil {
		return err
	}
	if err := r.checkReduceOnly(o); err != nil {
		return err
	}
	if err := r.checkPerpKYC(o); err != nil {
		return err
	}
	return nil
}

// checkReduceOnly rejects ReduceOnly orders that would increase the position
// rather than reduce it.
// - ReduceOnly SELL is valid only when the account holds a LONG (NetQuantity > 0).
// - ReduceOnly BUY  is valid only when the account holds a SHORT (NetQuantity < 0).
func (r *RiskChecker) checkReduceOnly(o *clob.Order) error {
	if !o.ReduceOnly || r.tracker == nil {
		return nil
	}
	pos := r.tracker.Get(o.AccountId, o.MarketId)
	if o.Side == clob.OrderSideSell && pos.NetQuantity <= 0 {
		return fmt.Errorf("reduce-only SELL rejected: no long position to reduce (netQty=%d)", pos.NetQuantity)
	}
	if o.Side == clob.OrderSideBuy && pos.NetQuantity >= 0 {
		return fmt.Errorf("reduce-only BUY rejected: no short position to reduce (netQty=%d)", pos.NetQuantity)
	}
	return nil
}

// checkPerpLeverage rejects orders on PERP markets where the requested leverage
// exceeds the market's MaxLeverage. Skipped for SPOT markets or when o.Leverage==0.
func (r *RiskChecker) checkPerpLeverage(o *clob.Order) error {
	if r.perpStore == nil || o.Leverage == 0 {
		return nil
	}
	cfg, ok := r.perpStore.GetPerpConfig(o.MarketId)
	if !ok {
		return nil // SPOT market — no leverage check
	}
	if o.Leverage > cfg.MaxLeverage {
		return fmt.Errorf("leverage %d exceeds max leverage %d for market %s",
			o.Leverage, cfg.MaxLeverage, o.MarketId)
	}
	return nil
}

// checkRateLimit enforces MaxOrdersPerBlock per account. The order count is
// incremented atomically so each rejected order still counts toward the limit.
func (r *RiskChecker) checkRateLimit(accountId string) error {
	if r.policy.MaxOrdersPerBlock == 0 || r.rateLimitStore == nil {
		return nil
	}
	count := r.rateLimitStore.IncrementOrderCount(accountId)
	if count > r.policy.MaxOrdersPerBlock {
		return fmt.Errorf("rate limit exceeded: account=%s orders_this_block=%d limit=%d",
			accountId, count, r.policy.MaxOrdersPerBlock)
	}
	return nil
}

// checkKYCTierLimit enforces per-tier notional limits (price × qty).
func (r *RiskChecker) checkKYCTierLimit(accountId string, notional int64) error {
	if r.kycTierChecker == nil || r.accountTierStore == nil {
		return nil
	}
	tier, jurisdiction := r.accountTierStore.GetAccountTier(accountId)
	return r.kycTierChecker.CheckOrder(tier, jurisdiction, notional)
}

// checkSanctions returns an error when the account is on the on-chain sanctions list.
// Sanctions are always enforced regardless of RiskPolicy settings.
func (r *RiskChecker) checkSanctions(accountId string) error {
	if r.sanctionsStore == nil {
		return nil
	}
	if r.sanctionsStore.IsSanctioned(accountId) {
		return fmt.Errorf("account %s is sanctioned and cannot trade", accountId)
	}
	return nil
}

// checkKYC returns an error when KYC enforcement is on and the account is not approved.
func (r *RiskChecker) checkKYC(accountId string) error {
	if !r.policy.RequireKYC || r.kycStore == nil {
		return nil
	}
	status := r.kycStore.GetKYCStatus(accountId)
	if status == account.KYCStatusApproved || status == account.KYCStatusExempt {
		return nil
	}
	return fmt.Errorf("KYC not approved: account=%s status=%s", accountId, status)
}

// checkPerpKYC enforces KYC for PERP market orders when RequirePerpKYC is set,
// independent of the global RequireKYC flag. This supports stricter compliance
// requirements for leverage products.
func (r *RiskChecker) checkPerpKYC(o *clob.Order) error {
	if !r.policy.RequirePerpKYC || r.perpStore == nil || r.kycStore == nil {
		return nil
	}
	if _, isPerp := r.perpStore.GetPerpConfig(o.MarketId); !isPerp {
		return nil // SPOT market — handled by checkKYC
	}
	status := r.kycStore.GetKYCStatus(o.AccountId)
	if status == account.KYCStatusApproved || status == account.KYCStatusExempt {
		return nil
	}
	return fmt.Errorf("KYC required for PERP trading: account=%s status=%s", o.AccountId, status)
}

// checkPositionLimit rejects an order that would push the account's net position
// beyond MaxPositionSize (absolute value). Skipped when MaxPositionSize == 0.
func (r *RiskChecker) checkPositionLimit(o *clob.Order) error {
	if r.policy.MaxPositionSize == 0 {
		return nil
	}
	current := r.tracker.Get(o.AccountId, o.MarketId)
	var projected int64
	if o.Side == clob.OrderSideBuy {
		projected = current.NetQuantity + o.Quantity
	} else {
		projected = current.NetQuantity - o.Quantity
	}
	if projected > r.policy.MaxPositionSize || projected < -r.policy.MaxPositionSize {
		return fmt.Errorf("position limit exceeded: account=%s market=%s projected=%d limit=%d",
			o.AccountId, o.MarketId, projected, r.policy.MaxPositionSize)
	}
	return nil
}
