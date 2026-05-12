package account

type TradingSession struct {
	SessionId            string
	AccountId            string
	SessionPublicKey     string
	AllowedMarkets       map[string]bool
	MaxOrderAmount       int64
	MaxDailyVolume       int64
	ExpiresAtBlockHeight int64
	CanWithdraw          bool
	CanTransfer          bool
	IsEnabled            bool
}

type SessionOptions struct {
	SessionPublicKey     string
	AllowedMarkets       []string
	MaxOrderAmount       int64
	MaxDailyVolume       int64
	ExpiresAtBlockHeight int64
	CanWithdraw          bool
	CanTransfer          bool
}

func NewTradingSession(accountId string, opts SessionOptions) TradingSession {
	markets := make(map[string]bool, len(opts.AllowedMarkets))
	for _, m := range opts.AllowedMarkets {
		markets[m] = true
	}
	return TradingSession{
		SessionId:            newID("ses"),
		AccountId:            accountId,
		SessionPublicKey:     opts.SessionPublicKey,
		AllowedMarkets:       markets,
		MaxOrderAmount:       opts.MaxOrderAmount,
		MaxDailyVolume:       opts.MaxDailyVolume,
		ExpiresAtBlockHeight: opts.ExpiresAtBlockHeight,
		CanWithdraw:          opts.CanWithdraw,
		CanTransfer:          opts.CanTransfer,
		IsEnabled:            true,
	}
}

func (s TradingSession) IsExpiredAt(blockHeight int64) bool {
	if s.ExpiresAtBlockHeight == 0 {
		return false
	}
	return blockHeight >= s.ExpiresAtBlockHeight
}

func (s TradingSession) CanTradeMarket(marketId string) bool {
	if len(s.AllowedMarkets) == 0 {
		return true
	}
	return s.AllowedMarkets[marketId]
}
