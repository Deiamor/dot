package risk

import (
	"fmt"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

// KYCStore allows the risk checker to read KYC status without importing AppState directly.
type KYCStore interface {
	GetKYCStatus(accountId string) account.KYCStatus
}

type RiskChecker struct {
	policy   RiskPolicy
	tracker  *PositionTracker
	kycStore KYCStore // nil when RequireKYC == false
}

func NewRiskChecker(policy RiskPolicy, tracker *PositionTracker) *RiskChecker {
	return &RiskChecker{policy: policy, tracker: tracker}
}

// SetKYCStore wires the KYC store used when RequireKYC == true.
func (r *RiskChecker) SetKYCStore(store KYCStore) {
	r.kycStore = store
}

// SetPolicy replaces the active risk policy. Called by governance on proposal execution.
func (r *RiskChecker) SetPolicy(p RiskPolicy) {
	r.policy = p
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
	if err := r.checkKYC(o.AccountId); err != nil {
		return err
	}
	if err := r.checkPositionLimit(o); err != nil {
		return err
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
