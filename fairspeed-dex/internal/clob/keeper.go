package clob

import "fmt"

type OrderStore interface {
	GetOrderBook(marketId string) (*OrderBook, bool)
	SetOrderBook(ob *OrderBook)
	GetOrder(id string) (*Order, bool)
	SetOrder(o *Order)
	AllOrders() []*Order
	RecordOrderHistory(o *Order)
}

type ReserveStore interface {
	Reserve(accountId, assetId string, amount int64) error
	Release(accountId, assetId string, amount int64) error
}

type OrderEventPublisher interface {
	PublishOrderSubmitted(orderId, accountId, marketId, status string, blockHeight int64)
	PublishOrderCancelled(orderId, accountId string, blockHeight int64)
	PublishOrderExpired(orderId, accountId string, blockHeight int64)
}

type OrderBookKeeper struct {
	orderStore   OrderStore
	reserveStore ReserveStore
	bus          OrderEventPublisher
}

func NewOrderBookKeeper(orderStore OrderStore, reserveStore ReserveStore, bus OrderEventPublisher) *OrderBookKeeper {
	return &OrderBookKeeper{orderStore: orderStore, reserveStore: reserveStore, bus: bus}
}

func (k *OrderBookKeeper) GetOrCreateOrderBook(marketId string) *OrderBook {
	ob, ok := k.orderStore.GetOrderBook(marketId)
	if !ok || ob == nil {
		ob = NewOrderBook(marketId)
		k.orderStore.SetOrderBook(ob)
	}
	return ob
}

// SubmitOrder reserves collateral and adds the order to the book.
// Call this only for resting (GTC) orders after matching has already occurred.
func (k *OrderBookKeeper) SubmitOrder(o Order, blockHeight int64) error {
	assetId, amount := reserveAssetForOrder(o)
	if amount > 0 {
		if err := k.reserveStore.Reserve(o.AccountId, assetId, amount); err != nil {
			return fmt.Errorf("reserving collateral for order %s: %w", o.OrderId, err)
		}
	}
	k.orderStore.SetOrder(&o)
	ob := k.GetOrCreateOrderBook(o.MarketId)
	stored, _ := k.orderStore.GetOrder(o.OrderId)
	if stored != nil {
		ob.AddOrder(stored)
	} else {
		ob.AddOrder(&o)
	}
	k.orderStore.SetOrderBook(ob)
	k.bus.PublishOrderSubmitted(o.OrderId, o.AccountId, o.MarketId, string(o.Status), blockHeight)
	return nil
}

// ReserveForOrder reserves collateral without adding to the book (used before matching).
func (k *OrderBookKeeper) ReserveForOrder(o Order) error {
	assetId, amount := reserveAssetForOrder(o)
	if amount > 0 {
		return k.reserveStore.Reserve(o.AccountId, assetId, amount)
	}
	return nil
}

// ReleaseForOrder releases collateral for remaining quantity (used after partial/full fill).
func (k *OrderBookKeeper) ReleaseForOrder(o Order, releasedQty int64) error {
	assetId, _ := reserveAssetForOrder(o)
	var amount int64
	if o.IsBuy() {
		amount = o.Price * releasedQty
	} else {
		amount = releasedQty
	}
	if amount > 0 {
		return k.reserveStore.Release(o.AccountId, assetId, amount)
	}
	return nil
}

func (k *OrderBookKeeper) CancelOrder(orderId, accountId string, blockHeight int64) error {
	o, ok := k.orderStore.GetOrder(orderId)
	if !ok {
		return fmt.Errorf("order not found: %s", orderId)
	}
	if o.AccountId != accountId {
		return fmt.Errorf("order %s does not belong to account %s", orderId, accountId)
	}

	assetId, _ := reserveAssetForOrder(*o)
	var reservedAmount int64
	if o.IsBuy() {
		reservedAmount = o.Price * o.RemainingQuantity
	} else {
		reservedAmount = o.RemainingQuantity
	}
	if reservedAmount > 0 {
		_ = k.reserveStore.Release(accountId, assetId, reservedAmount)
	}

	ob, ok := k.orderStore.GetOrderBook(o.MarketId)
	if ok && ob != nil {
		ob.RemoveOrder(orderId)
		k.orderStore.SetOrderBook(ob)
	}

	o.Status = OrderStatusCancelled
	k.orderStore.SetOrder(o)
	k.orderStore.RecordOrderHistory(o)
	k.bus.PublishOrderCancelled(orderId, accountId, blockHeight)
	return nil
}

func (k *OrderBookKeeper) ExpireOrders(blockHeight int64) []Order {
	var expired []Order
	for _, o := range k.orderStore.AllOrders() {
		if o.ExpireBlockHeight > 0 && o.ExpireBlockHeight <= blockHeight && o.Status == OrderStatusOpen {
			assetId, _ := reserveAssetForOrder(*o)
			var reservedAmount int64
			if o.IsBuy() {
				reservedAmount = o.Price * o.RemainingQuantity
			} else {
				reservedAmount = o.RemainingQuantity
			}
			if reservedAmount > 0 {
				_ = k.reserveStore.Release(o.AccountId, assetId, reservedAmount)
			}
			ob, ok := k.orderStore.GetOrderBook(o.MarketId)
			if ok && ob != nil {
				ob.RemoveOrder(o.OrderId)
				k.orderStore.SetOrderBook(ob)
			}
			o.Status = OrderStatusExpired
			k.orderStore.SetOrder(o)
			k.orderStore.RecordOrderHistory(o)
			k.bus.PublishOrderExpired(o.OrderId, o.AccountId, blockHeight)
			expired = append(expired, *o)
		}
	}
	return expired
}

func (k *OrderBookKeeper) UpdateOrderStatus(o *Order) {
	k.orderStore.SetOrder(o)
}

// reserveAssetForOrder returns the asset and amount that must be reserved for an order.
// BUY: reserve quoteAsset = price * quantity
// SELL: reserve baseAsset = quantity
func reserveAssetForOrder(o Order) (assetId string, amount int64) {
	parts := splitMarket(o.MarketId)
	if o.IsBuy() {
		return parts[1], o.Price * o.RemainingQuantity
	}
	return parts[0], o.RemainingQuantity
}

func splitMarket(marketId string) [2]string {
	return SplitMarket(marketId)
}

// SplitMarket splits "BASE-QUOTE" into [2]string{"BASE", "QUOTE"}.
// Returns [marketId, ""] for markets without a dash.
func SplitMarket(marketId string) [2]string {
	for i := 0; i < len(marketId); i++ {
		if marketId[i] == '-' {
			return [2]string{marketId[:i], marketId[i+1:]}
		}
	}
	return [2]string{marketId, ""}
}
