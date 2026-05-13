package clob

type MatchResult struct {
	MakerOrderId   string
	TakerOrderId   string
	MakerAccountId string
	TakerAccountId string
	MarketId       string
	Price          int64
	Quantity       int64
	BlockHeight    int64
	TakerSide      OrderSide // BUY if taker is the buyer, SELL if taker is the seller
}

type MatchingEngine interface {
	MatchOrder(incoming *Order, ob *OrderBook, blockHeight int64) []MatchResult
}

type PricePriorityMatcher struct{}

func (m PricePriorityMatcher) MatchOrder(incoming *Order, ob *OrderBook, blockHeight int64) []MatchResult {
	if incoming.TimeInForce == TimeInForceFok {
		available := m.availableLiquidity(incoming, ob)
		if available < incoming.RemainingQuantity {
			incoming.Status = OrderStatusRejected
			return nil
		}
	}
	return m.matchContinuous(incoming, ob, blockHeight)
}

func (m PricePriorityMatcher) availableLiquidity(incoming *Order, ob *OrderBook) int64 {
	var total int64
	if incoming.IsBuy() {
		for _, price := range ob.SortedAskPrices() {
			if price > incoming.Price {
				break
			}
			total += ob.Asks[price].TotalQuantity
		}
	} else {
		for _, price := range ob.SortedBidPrices() {
			if price < incoming.Price {
				break
			}
			total += ob.Bids[price].TotalQuantity
		}
	}
	return total
}

func (m PricePriorityMatcher) matchContinuous(incoming *Order, ob *OrderBook, blockHeight int64) []MatchResult {
	var results []MatchResult

	if incoming.IsBuy() {
		for _, askPrice := range ob.SortedAskPrices() {
			if incoming.RemainingQuantity <= 0 {
				break
			}
			if askPrice > incoming.Price {
				break
			}
			level := ob.Asks[askPrice]
			results = append(results, m.drainLevel(incoming, level, ob, blockHeight)...)
			if level.IsEmpty() {
				delete(ob.Asks, askPrice)
			}
		}
	} else {
		for _, bidPrice := range ob.SortedBidPrices() {
			if incoming.RemainingQuantity <= 0 {
				break
			}
			if bidPrice < incoming.Price {
				break
			}
			level := ob.Bids[bidPrice]
			results = append(results, m.drainLevel(incoming, level, ob, blockHeight)...)
			if level.IsEmpty() {
				delete(ob.Bids, bidPrice)
			}
		}
	}

	if incoming.RemainingQuantity > 0 {
		switch incoming.TimeInForce {
		case TimeInForceIoc, TimeInForceFok:
			incoming.Status = OrderStatusCancelled
		case TimeInForceGtc:
			if incoming.Quantity > incoming.RemainingQuantity {
				incoming.Status = OrderStatusPartiallyFilled
			}
		}
	} else {
		incoming.Status = OrderStatusFilled
	}

	return results
}

func (m PricePriorityMatcher) drainLevel(incoming *Order, level *PriceLevel, ob *OrderBook, blockHeight int64) []MatchResult {
	var results []MatchResult
	for !level.IsEmpty() && incoming.RemainingQuantity > 0 {
		maker := level.Peek()
		tradeQty := minInt64(incoming.RemainingQuantity, maker.RemainingQuantity)

		incoming.RemainingQuantity -= tradeQty
		maker.RemainingQuantity -= tradeQty
		level.TotalQuantity -= tradeQty

		if maker.RemainingQuantity == 0 {
			maker.Status = OrderStatusFilled
			level.Dequeue()
			delete(ob.OrdersById, maker.OrderId)
		} else {
			maker.Status = OrderStatusPartiallyFilled
		}

		results = append(results, MatchResult{
			MakerOrderId:   maker.OrderId,
			TakerOrderId:   incoming.OrderId,
			MakerAccountId: maker.AccountId,
			TakerAccountId: incoming.AccountId,
			MarketId:       incoming.MarketId,
			Price:          maker.Price,
			Quantity:       tradeQty,
			BlockHeight:    blockHeight,
			TakerSide:      incoming.Side,
		})
	}
	return results
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
