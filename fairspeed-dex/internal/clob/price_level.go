package clob

type PriceLevel struct {
	Price         int64
	OrderQueue    []*Order
	TotalQuantity int64
}

func NewPriceLevel(price int64) *PriceLevel {
	return &PriceLevel{Price: price}
}

func (pl *PriceLevel) Enqueue(o *Order) {
	pl.OrderQueue = append(pl.OrderQueue, o)
	pl.TotalQuantity += o.RemainingQuantity
}

func (pl *PriceLevel) Dequeue() *Order {
	if len(pl.OrderQueue) == 0 {
		return nil
	}
	o := pl.OrderQueue[0]
	pl.OrderQueue = pl.OrderQueue[1:]
	pl.TotalQuantity -= o.RemainingQuantity
	if pl.TotalQuantity < 0 {
		pl.TotalQuantity = 0
	}
	return o
}

func (pl *PriceLevel) Peek() *Order {
	if len(pl.OrderQueue) == 0 {
		return nil
	}
	return pl.OrderQueue[0]
}

func (pl *PriceLevel) IsEmpty() bool {
	return len(pl.OrderQueue) == 0
}

func (pl *PriceLevel) RemoveOrder(orderId string) bool {
	for i, o := range pl.OrderQueue {
		if o.OrderId == orderId {
			pl.TotalQuantity -= o.RemainingQuantity
			if pl.TotalQuantity < 0 {
				pl.TotalQuantity = 0
			}
			pl.OrderQueue = append(pl.OrderQueue[:i], pl.OrderQueue[i+1:]...)
			return true
		}
	}
	return false
}

func (pl *PriceLevel) UpdateQuantity(orderId string, filledQty int64) {
	for _, o := range pl.OrderQueue {
		if o.OrderId == orderId {
			o.RemainingQuantity -= filledQty
			pl.TotalQuantity -= filledQty
			if o.RemainingQuantity == 0 {
				pl.RemoveOrder(orderId)
			}
			return
		}
	}
}
