package state

import (
	"fmt"
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

// AppState is the single in-memory truth for all DEX state.
//
// Locking strategy:
//   - globalMu  protects accounts, sessions, balances, assets, orders, and
//     blockHeight — state that is shared across markets.
//   - marketMus holds one RWMutex per market (lazy-initialised via sync.Map).
//     Orderbook reads/writes use only the per-market lock, so concurrent API
//     reads on BTC-USDC do not block reads or writes on ETH-USDC.
//
// Rule: never hold a marketMu while acquiring globalMu, and vice versa.
// Each AppState method acquires and releases exactly one lock, so deadlock
// cannot occur.
type AppState struct {
	globalMu  sync.RWMutex
	marketMus sync.Map // map[marketId string] → *sync.RWMutex

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

// marketMu returns (or creates) the per-market RWMutex for marketId.
func (s *AppState) marketMu(marketId string) *sync.RWMutex {
	v, _ := s.marketMus.LoadOrStore(marketId, &sync.RWMutex{})
	return v.(*sync.RWMutex)
}

func (s *AppState) IncrementBlock() {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.BlockHeight++
}

func (s *AppState) CurrentHeight() int64 {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	return s.BlockHeight
}

// --- account.AccountStore ---

func (s *AppState) GetAccount(id string) (*account.NativeAccount, bool) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	a, ok := s.Accounts[id]
	return a, ok
}

func (s *AppState) SetAccount(a *account.NativeAccount) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.Accounts[a.AccountId] = a
}

func (s *AppState) GetSession(id string) (*account.TradingSession, bool) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	sess, ok := s.Sessions[id]
	return sess, ok
}

func (s *AppState) SetSession(sess *account.TradingSession) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.Sessions[sess.SessionId] = sess
}

func (s *AppState) AllSessions(accountId string) []*account.TradingSession {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
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
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	a, ok := s.Assets[id]
	return a, ok
}

func (s *AppState) SetAsset(a *asset.Asset) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.Assets[a.AssetId] = a
}

func (s *AppState) GetBalance(accountId, assetId string) *asset.Balance {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
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
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	if s.Balances[b.AccountId] == nil {
		s.Balances[b.AccountId] = make(map[string]*asset.Balance)
	}
	s.Balances[b.AccountId][b.AssetId] = b
}

func (s *AppState) Reserve(accountId, assetId string, amount int64) error {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
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
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
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

// GetOrderBook acquires only the per-market read lock, so reads on
// BTC-USDC do not block reads on ETH-USDC.
func (s *AppState) GetOrderBook(marketId string) (*clob.OrderBook, bool) {
	mu := s.marketMu(marketId)
	mu.RLock()
	defer mu.RUnlock()
	ob, ok := s.OrderBooks[marketId]
	return ob, ok
}

// SetOrderBook acquires only the per-market write lock.
func (s *AppState) SetOrderBook(ob *clob.OrderBook) {
	mu := s.marketMu(ob.MarketId)
	mu.Lock()
	defer mu.Unlock()
	s.OrderBooks[ob.MarketId] = ob
}

func (s *AppState) GetOrder(id string) (*clob.Order, bool) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	o, ok := s.Orders[id]
	return o, ok
}

func (s *AppState) SetOrder(o *clob.Order) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.Orders[o.OrderId] = o
}

func (s *AppState) AllOrders() []*clob.Order {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	orders := make([]*clob.Order, 0, len(s.Orders))
	for _, o := range s.Orders {
		orders = append(orders, o)
	}
	return orders
}
