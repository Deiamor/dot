package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/oracle"
	"github.com/byunghee1994/fairspeed-dex/internal/validator"
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
	Validators  map[string]*validator.Validator
	Proposals   map[string]*governance.Proposal
	Votes       map[string][]governance.VoteRecord // keyed by proposalId
	Sanctions   map[string]*compliance.SanctionEntry
	Markets          map[string]*clob.MarketInfo                       // per-market status (halt/resume)
	OraclePrices     map[string]map[string]*oracle.PriceSubmission     // marketId → validatorId → submission
	OrdersThisBlock  map[string]int64                                  // accountId → orders submitted this block
	BlockHeight      int64
}

func NewAppState() *AppState {
	return &AppState{
		Accounts:   make(map[string]*account.NativeAccount),
		Sessions:   make(map[string]*account.TradingSession),
		Balances:   make(map[string]map[string]*asset.Balance),
		Assets:     make(map[string]*asset.Asset),
		Orders:     make(map[string]*clob.Order),
		OrderBooks: make(map[string]*clob.OrderBook),
		Validators: make(map[string]*validator.Validator),
		Proposals:  make(map[string]*governance.Proposal),
		Votes:      make(map[string][]governance.VoteRecord),
		Sanctions:       make(map[string]*compliance.SanctionEntry),
		Markets:         make(map[string]*clob.MarketInfo),
		OraclePrices:    make(map[string]map[string]*oracle.PriceSubmission),
		OrdersThisBlock: make(map[string]int64),
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
	s.OrdersThisBlock = make(map[string]int64) // reset per-block order counts
}

// IncrementOrderCount increments the order count for accountId in the current block
// and returns the new total.
func (s *AppState) IncrementOrderCount(accountId string) int64 {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.OrdersThisBlock[accountId]++
	return s.OrdersThisBlock[accountId]
}

// GetOrderCount returns the number of orders submitted by accountId in the current block.
func (s *AppState) GetOrderCount(accountId string) int64 {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	return s.OrdersThisBlock[accountId]
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

// GetKYCStatus implements risk.KYCStore.
func (s *AppState) GetKYCStatus(accountId string) account.KYCStatus {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	a, ok := s.Accounts[accountId]
	if !ok {
		return account.KYCStatusPending
	}
	return a.KYCStatus
}

// --- validator.ValidatorStore ---

func (s *AppState) GetValidator(validatorId string) (validator.Validator, bool) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	v, ok := s.Validators[validatorId]
	if !ok {
		return validator.Validator{}, false
	}
	return *v, true
}

func (s *AppState) SetValidator(v validator.Validator) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	cp := v
	s.Validators[v.ValidatorId] = &cp
}

func (s *AppState) AllValidators() []validator.Validator {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	result := make([]validator.Validator, 0, len(s.Validators))
	for _, v := range s.Validators {
		result = append(result, *v)
	}
	return result
}

// --- governance.ProposalStore ---

func (s *AppState) GetProposal(proposalId string) (governance.Proposal, bool) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	p, ok := s.Proposals[proposalId]
	if !ok {
		return governance.Proposal{}, false
	}
	return *p, true
}

func (s *AppState) SetProposal(p governance.Proposal) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	cp := p
	s.Proposals[p.ProposalId] = &cp
}

func (s *AppState) AllProposals() []governance.Proposal {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	result := make([]governance.Proposal, 0, len(s.Proposals))
	for _, p := range s.Proposals {
		result = append(result, *p)
	}
	return result
}

func (s *AppState) GetVotesForProposal(proposalId string) []governance.VoteRecord {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	votes := s.Votes[proposalId]
	out := make([]governance.VoteRecord, len(votes))
	copy(out, votes)
	return out
}

func (s *AppState) AddVote(v governance.VoteRecord) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	// Replace existing vote from same validator if present.
	existing := s.Votes[v.ProposalId]
	for i, ev := range existing {
		if ev.ValidatorId == v.ValidatorId {
			existing[i] = v
			s.Votes[v.ProposalId] = existing
			return
		}
	}
	s.Votes[v.ProposalId] = append(existing, v)
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

// GetAccountTier returns the KYC tier and jurisdiction for an account.
// Returns (KYCTierNone, JurisdictionDefault) when account is not found.
func (s *AppState) GetAccountTier(accountId string) (account.KYCTier, account.Jurisdiction) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	acc, ok := s.Accounts[accountId]
	if !ok {
		return account.KYCTierNone, account.JurisdictionDefault
	}
	return acc.KYCTier, acc.Jurisdiction
}

// ---- SanctionsStore ---------------------------------------------------------

func (s *AppState) IsSanctioned(accountId string) bool {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	_, ok := s.Sanctions[accountId]
	return ok
}

func (s *AppState) AddSanction(entry compliance.SanctionEntry) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.Sanctions[entry.AccountId] = &entry
}

func (s *AppState) RemoveSanction(accountId string) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	delete(s.Sanctions, accountId)
}

func (s *AppState) AllSanctions() []compliance.SanctionEntry {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	result := make([]compliance.SanctionEntry, 0, len(s.Sanctions))
	for _, e := range s.Sanctions {
		result = append(result, *e)
	}
	return result
}

// ---- circuit breaker / market halt ------------------------------------------

// GetMarketStatus returns the operational status of marketId.
// Returns MarketStatusActive when no explicit entry exists.
func (s *AppState) GetMarketStatus(marketId string) clob.MarketStatus {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	if info, ok := s.Markets[marketId]; ok {
		return info.Status
	}
	return clob.MarketStatusActive
}

// GetMarketInfo returns the full MarketInfo for a market (nil if not set).
func (s *AppState) GetMarketInfo(marketId string) (*clob.MarketInfo, bool) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	info, ok := s.Markets[marketId]
	return info, ok
}

// HaltMarket sets a market to HALTED status with the given reason.
func (s *AppState) HaltMarket(marketId, reason string, height int64) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.Markets[marketId] = &clob.MarketInfo{
		MarketId:       marketId,
		Status:         clob.MarketStatusHalted,
		HaltReason:     reason,
		HaltedAtHeight: height,
	}
}

// ResumeMarket sets a market back to ACTIVE status.
func (s *AppState) ResumeMarket(marketId string) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	s.Markets[marketId] = &clob.MarketInfo{
		MarketId: marketId,
		Status:   clob.MarketStatusActive,
	}
}

// ---- oracle / mark price ----------------------------------------------------

// SetOraclePrice stores a validator's price submission for a market.
func (s *AppState) SetOraclePrice(sub oracle.PriceSubmission) {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()
	if s.OraclePrices[sub.MarketId] == nil {
		s.OraclePrices[sub.MarketId] = make(map[string]*oracle.PriceSubmission)
	}
	cp := sub
	s.OraclePrices[sub.MarketId][sub.ValidatorId] = &cp
}

// GetMarkPrice returns the median of all validator price submissions for marketId.
// Returns 0 when no prices have been submitted.
func (s *AppState) GetMarkPrice(marketId string) int64 {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	subs, ok := s.OraclePrices[marketId]
	if !ok || len(subs) == 0 {
		return 0
	}
	prices := make([]int64, 0, len(subs))
	for _, sub := range subs {
		prices = append(prices, sub.Price)
	}
	return oracle.MedianPrice(prices)
}

// AllOraclePrices returns all price submissions for a market (latest per validator).
func (s *AppState) AllOraclePrices(marketId string) []oracle.PriceSubmission {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	subs := s.OraclePrices[marketId]
	result := make([]oracle.PriceSubmission, 0, len(subs))
	for _, sub := range subs {
		result = append(result, *sub)
	}
	return result
}

// ---- snapshot persistence ---------------------------------------------------

type appStateSnapshot struct {
	BlockHeight int64                                    `json:"block_height"`
	Accounts    map[string]*account.NativeAccount        `json:"accounts"`
	Sessions    map[string]*account.TradingSession       `json:"sessions"`
	Balances    map[string]map[string]*asset.Balance     `json:"balances"`
	Assets      map[string]*asset.Asset                  `json:"assets"`
	Orders      map[string]*clob.Order                   `json:"orders"`
	OrderBooks  map[string]*clob.OrderBook               `json:"order_books"`
	Validators  map[string]*validator.Validator          `json:"validators,omitempty"`
	Proposals   map[string]*governance.Proposal          `json:"proposals,omitempty"`
	Votes       map[string][]governance.VoteRecord       `json:"votes,omitempty"`
	Sanctions    map[string]*compliance.SanctionEntry               `json:"sanctions,omitempty"`
	Markets      map[string]*clob.MarketInfo                        `json:"markets,omitempty"`
	OraclePrices map[string]map[string]*oracle.PriceSubmission      `json:"oracle_prices,omitempty"`
}

// SaveSnapshot serialises the current state to disk atomically (write-then-rename).
// Safe to call from Commit: FinalizeBlock cannot run concurrently (DEXApplication
// mutex ensures exclusivity), so no concurrent writes to OrderBooks occur here.
func (s *AppState) SaveSnapshot(path string) error {
	s.globalMu.Lock()
	snap := appStateSnapshot{
		BlockHeight: s.BlockHeight,
		Accounts:    make(map[string]*account.NativeAccount, len(s.Accounts)),
		Sessions:    make(map[string]*account.TradingSession, len(s.Sessions)),
		Balances:    make(map[string]map[string]*asset.Balance, len(s.Balances)),
		Assets:      make(map[string]*asset.Asset, len(s.Assets)),
		Orders:      make(map[string]*clob.Order, len(s.Orders)),
		OrderBooks:  make(map[string]*clob.OrderBook, len(s.OrderBooks)),
		Validators:  make(map[string]*validator.Validator, len(s.Validators)),
		Proposals:   make(map[string]*governance.Proposal, len(s.Proposals)),
		Votes:       make(map[string][]governance.VoteRecord, len(s.Votes)),
		Sanctions:   make(map[string]*compliance.SanctionEntry, len(s.Sanctions)),
		Markets:      make(map[string]*clob.MarketInfo, len(s.Markets)),
		OraclePrices: make(map[string]map[string]*oracle.PriceSubmission, len(s.OraclePrices)),
	}
	for k, v := range s.Accounts {
		snap.Accounts[k] = v
	}
	for k, v := range s.Sessions {
		snap.Sessions[k] = v
	}
	for k, v := range s.Assets {
		snap.Assets[k] = v
	}
	for k, v := range s.Orders {
		snap.Orders[k] = v
	}
	for k, bals := range s.Balances {
		snap.Balances[k] = make(map[string]*asset.Balance, len(bals))
		for assetId, b := range bals {
			snap.Balances[k][assetId] = b
		}
	}
	// OrderBooks: accessed directly while holding globalMu.Lock().
	// Concurrent writes to OrderBooks only happen in FinalizeBlock, which is
	// blocked by the DEXApplication mutex during Commit.
	for k, v := range s.OrderBooks {
		snap.OrderBooks[k] = v
	}
	for k, v := range s.Validators {
		snap.Validators[k] = v
	}
	for k, v := range s.Proposals {
		snap.Proposals[k] = v
	}
	for k, vv := range s.Votes {
		cp := make([]governance.VoteRecord, len(vv))
		copy(cp, vv)
		snap.Votes[k] = cp
	}
	for k, v := range s.Sanctions {
		snap.Sanctions[k] = v
	}
	for k, v := range s.Markets {
		snap.Markets[k] = v
	}
	for mkt, subs := range s.OraclePrices {
		cp := make(map[string]*oracle.PriceSubmission, len(subs))
		for vid, sub := range subs {
			cp[vid] = sub
		}
		snap.OraclePrices[mkt] = cp
	}
	s.globalMu.Unlock()

	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir for snapshot: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write snapshot tmp: %w", err)
	}
	return os.Rename(tmp, path)
}

// LoadSnapshot reads state from a snapshot file written by SaveSnapshot.
// Returns nil (fresh start) if the file does not exist.
// Must be called before any concurrent access to AppState.
func (s *AppState) LoadSnapshot(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read snapshot: %w", err)
	}
	var snap appStateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("unmarshal snapshot: %w", err)
	}

	s.globalMu.Lock()
	defer s.globalMu.Unlock()

	s.BlockHeight = snap.BlockHeight
	if snap.Accounts != nil {
		s.Accounts = snap.Accounts
	}
	if snap.Sessions != nil {
		s.Sessions = snap.Sessions
	}
	if snap.Balances != nil {
		s.Balances = snap.Balances
	}
	if snap.Assets != nil {
		s.Assets = snap.Assets
	}
	if snap.Orders != nil {
		s.Orders = snap.Orders
	}
	if snap.OrderBooks != nil {
		s.OrderBooks = snap.OrderBooks
		// Rebuild OrdersById indices from PriceLevels so pointer identity is
		// consistent, then sync the global Orders map to the same pointers.
		for _, ob := range s.OrderBooks {
			ob.RebuildIndex()
			for id, o := range ob.OrdersById {
				s.Orders[id] = o
			}
		}
	}
	if snap.Validators != nil {
		s.Validators = snap.Validators
	}
	if snap.Proposals != nil {
		s.Proposals = snap.Proposals
	}
	if snap.Votes != nil {
		s.Votes = snap.Votes
	}
	if snap.Sanctions != nil {
		s.Sanctions = snap.Sanctions
	}
	if snap.Markets != nil {
		s.Markets = snap.Markets
	}
	if snap.OraclePrices != nil {
		s.OraclePrices = snap.OraclePrices
	}
	return nil
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
