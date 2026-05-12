package clob

import "sort"

type OrderBook struct {
	MarketId   string
	Bids       map[int64]*PriceLevel
	Asks       map[int64]*PriceLevel
	OrdersById map[string]*Order
}

func NewOrderBook(marketId string) *OrderBook {
	return &OrderBook{
		MarketId:   marketId,
		Bids:       make(map[int64]*PriceLevel),
		Asks:       make(map[int64]*PriceLevel),
		OrdersById: make(map[string]*Order),
	}
}

func (ob *OrderBook) AddOrder(o *Order) {
	ob.OrdersById[o.OrderId] = o
	if o.IsBuy() {
		if ob.Bids[o.Price] == nil {
			ob.Bids[o.Price] = NewPriceLevel(o.Price)
		}
		ob.Bids[o.Price].Enqueue(o)
	} else {
		if ob.Asks[o.Price] == nil {
			ob.Asks[o.Price] = NewPriceLevel(o.Price)
		}
		ob.Asks[o.Price].Enqueue(o)
	}
}

func (ob *OrderBook) RemoveOrder(orderId string) {
	o, ok := ob.OrdersById[orderId]
	if !ok {
		return
	}
	delete(ob.OrdersById, orderId)
	if o.IsBuy() {
		if pl := ob.Bids[o.Price]; pl != nil {
			pl.RemoveOrder(orderId)
			if pl.IsEmpty() {
				delete(ob.Bids, o.Price)
			}
		}
	} else {
		if pl := ob.Asks[o.Price]; pl != nil {
			pl.RemoveOrder(orderId)
			if pl.IsEmpty() {
				delete(ob.Asks, o.Price)
			}
		}
	}
}

func (ob *OrderBook) GetOrder(orderId string) (*Order, bool) {
	o, ok := ob.OrdersById[orderId]
	return o, ok
}

func (ob *OrderBook) SortedBidPrices() []int64 {
	prices := make([]int64, 0, len(ob.Bids))
	for p := range ob.Bids {
		prices = append(prices, p)
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i] > prices[j] })
	return prices
}

func (ob *OrderBook) SortedAskPrices() []int64 {
	prices := make([]int64, 0, len(ob.Asks))
	for p := range ob.Asks {
		prices = append(prices, p)
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
	return prices
}

func (ob *OrderBook) BestBid() (int64, bool) {
	prices := ob.SortedBidPrices()
	if len(prices) == 0 {
		return 0, false
	}
	return prices[0], true
}

func (ob *OrderBook) BestAsk() (int64, bool) {
	prices := ob.SortedAskPrices()
	if len(prices) == 0 {
		return 0, false
	}
	return prices[0], true
}

func (ob *OrderBook) BestBidLevel() *PriceLevel {
	price, ok := ob.BestBid()
	if !ok {
		return nil
	}
	return ob.Bids[price]
}

func (ob *OrderBook) BestAskLevel() *PriceLevel {
	price, ok := ob.BestAsk()
	if !ok {
		return nil
	}
	return ob.Asks[price]
}
