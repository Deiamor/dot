package state

import (
	"fmt"
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

type AppState struct {
	mu          sync.RWMutex
	Accounts    map[string]*account.NativeAccount
	Sessions    map[string]*account.TradingSession
	Balances    map[string]map[string]*asset.Balance
	Assets      map[string]*asset.Asset
	Orders      map[string]*clob.Order
	OrderBooks  map[string]*clob.OrderBook
	BlockHeight int64
}

func NewAppState() *AppState {
	return &AppState{
		Accounts:   make(map[string]*account.NativeAccount),
		Sessions:   make(map[string]*account.TradingSession),
		Balances:   make(map[string]map[string]*asset.Balance),
		Assets:     make(map[string]*asset.Asset),
		Orders:     make(map[string]*clob.Order),
		OrderBooks: make(map[string]*clob.OrderBook),
	}
}

func (s *AppState) IncrementBlock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.BlockHeight++
}

func (s *AppState) CurrentHeight() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.BlockHeight
}

// --- account.AccountStore ---

func (s *AppState) GetAccount(id string) (*account.NativeAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.Accounts[id]
	return a, ok
}

func (s *AppState) SetAccount(a *account.NativeAccount) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Accounts[a.AccountId] = a
}

func (s *AppState) GetSession(id string) (*account.TradingSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.Sessions[id]
	return sess, ok
}

func (s *AppState) SetSession(sess *account.TradingSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sessions[sess.SessionId] = sess
}

func (s *AppState) AllSessions(accountId string) []*account.TradingSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*account.TradingSession
	for _, sess := range s.Sessions {
		if sess.AccountId == accountId {
			result = append(result, sess)
		}
	}
	return result
}

// --- asset.BalanceStore ---

func (s *AppState) GetAsset(id string) (*asset.Asset, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.Assets[id]
	return a, ok
}

func (s *AppState) SetAsset(a *asset.Asset) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Assets[a.AssetId] = a
}

func (s *AppState) GetBalance(accountId, assetId string) *asset.Balance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getBalanceLocked(accountId, assetId)
}

func (s *AppState) getBalanceLocked(accountId, assetId string) *asset.Balance {
	if s.Balances[accountId] == nil {
		return &asset.Balance{AccountId: accountId, AssetId: assetId}
	}
	b, ok := s.Balances[accountId][assetId]
	if !ok {
		return &asset.Balance{AccountId: accountId, AssetId: assetId}
	}
	return b
}

func (s *AppState) SetBalance(b *asset.Balance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Balances[b.AccountId] == nil {
		s.Balances[b.AccountId] = make(map[string]*asset.Balance)
	}
	s.Balances[b.AccountId][b.AssetId] = b
}

func (s *AppState) Reserve(accountId, assetId string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.getBalanceLocked(accountId, assetId)
	if b.Available < amount {
		return fmt.Errorf("insufficient balance: account=%s asset=%s available=%d required=%d",
			accountId, assetId, b.Available, amount)
	}
	updated := &asset.Balance{
		AccountId: accountId,
		AssetId:   assetId,
		Available: b.Available - amount,
		Reserved:  b.Reserved + amount,
	}
	if s.Balances[accountId] == nil {
		s.Balances[accountId] = make(map[string]*asset.Balance)
	}
	s.Balances[accountId][assetId] = updated
	return nil
}

func (s *AppState) Release(accountId, assetId string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.getBalanceLocked(accountId, assetId)
	if b.Reserved < amount {
		return fmt.Errorf("insufficient reserved: account=%s asset=%s reserved=%d required=%d",
			accountId, assetId, b.Reserved, amount)
	}
	updated := &asset.Balance{
		AccountId: accountId,
		AssetId:   assetId,
		Available: b.Available + amount,
		Reserved:  b.Reserved - amount,
	}
	if s.Balances[accountId] == nil {
		s.Balances[accountId] = make(map[string]*asset.Balance)
	}
	s.Balances[accountId][assetId] = updated
	return nil
}

// --- clob.OrderStore ---

func (s *AppState) GetOrderBook(marketId string) (*clob.OrderBook, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ob, ok := s.OrderBooks[marketId]
	return ob, ok
}

func (s *AppState) SetOrderBook(ob *clob.OrderBook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OrderBooks[ob.MarketId] = ob
}

func (s *AppState) GetOrder(id string) (*clob.Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.Orders[id]
	return o, ok
}

func (s *AppState) SetOrder(o *clob.Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Orders[o.OrderId] = o
}

func (s *AppState) AllOrders() []*clob.Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	orders := make([]*clob.Order, 0, len(s.Orders))
	for _, o := range s.Orders {
		orders = append(orders, o)
	}
	return orders
}
