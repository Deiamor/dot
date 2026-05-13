package compliance

// AMLConfig configures Anti-Money Laundering thresholds.
// All monetary values are in quote-asset base units (e.g. USDC micro-units).
type AMLConfig struct {
	// SingleTradeLimitNotional fires an alert when a single trade's notional
	// (price × quantity) exceeds this amount. 0 means disabled.
	SingleTradeLimitNotional int64
}

// DefaultAMLConfig disables AML checks (for test/bootstrap environments).
var DefaultAMLConfig = AMLConfig{SingleTradeLimitNotional: 0}

// AMLAlert is raised when a trade breaches a compliance threshold.
type AMLAlert struct {
	TradeId   string
	MarketId  string
	Notional  int64
	Threshold int64
	Reason    string
}

// AMLDetector checks trades for suspicious activity patterns.
type AMLDetector struct {
	config AMLConfig
}

func NewAMLDetector(config AMLConfig) *AMLDetector {
	return &AMLDetector{config: config}
}

// CheckTrade returns a non-nil AMLAlert if the trade breaches any threshold.
func (d *AMLDetector) CheckTrade(tradeId, marketId string, price, qty int64) *AMLAlert {
	notional := price * qty
	if d.config.SingleTradeLimitNotional > 0 && notional > d.config.SingleTradeLimitNotional {
		return &AMLAlert{
			TradeId:   tradeId,
			MarketId:  marketId,
			Notional:  notional,
			Threshold: d.config.SingleTradeLimitNotional,
			Reason:    "single trade notional exceeds AML threshold",
		}
	}
	return nil
}
