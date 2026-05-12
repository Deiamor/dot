package settlement

type SettlementKeeper struct {
	trades map[string]TradeExecution
}

func NewSettlementKeeper() *SettlementKeeper {
	return &SettlementKeeper{trades: make(map[string]TradeExecution)}
}

func (k *SettlementKeeper) RecordTrade(t TradeExecution) {
	k.trades[t.TradeId] = t
}

func (k *SettlementKeeper) GetTrade(id string) (TradeExecution, bool) {
	t, ok := k.trades[id]
	return t, ok
}

func (k *SettlementKeeper) TradesForBlock(blockHeight int64) []TradeExecution {
	var result []TradeExecution
	for _, t := range k.trades {
		if t.BlockHeight == blockHeight {
			result = append(result, t)
		}
	}
	return result
}

func (k *SettlementKeeper) AllTrades() []TradeExecution {
	result := make([]TradeExecution, 0, len(k.trades))
	for _, t := range k.trades {
		result = append(result, t)
	}
	return result
}
