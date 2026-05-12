package risk

import (
	"fmt"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

type RiskChecker struct {
	policy RiskPolicy
}

func NewRiskChecker(policy RiskPolicy) *RiskChecker {
	return &RiskChecker{policy: policy}
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
	return nil
}
