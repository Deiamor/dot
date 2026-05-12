package account

import "fmt"

type AccountStore interface {
	GetAccount(id string) (*NativeAccount, bool)
	SetAccount(a *NativeAccount)
	GetSession(id string) (*TradingSession, bool)
	SetSession(s *TradingSession)
	AllSessions(accountId string) []*TradingSession
}

type EventPublisher interface {
	PublishAccountCreated(accountId, ownerAddress string, blockHeight int64)
	PublishSessionCreated(sessionId, accountId string, blockHeight int64)
}

type AccountKeeper struct {
	store AccountStore
	bus   EventPublisher
}

func NewAccountKeeper(store AccountStore, bus EventPublisher) *AccountKeeper {
	return &AccountKeeper{store: store, bus: bus}
}

func (k *AccountKeeper) CreateAccount(ownerAddress, rootPublicKey, withdrawalPublicKey string, blockHeight int64) (NativeAccount, error) {
	acc := NewNativeAccount(ownerAddress, rootPublicKey, withdrawalPublicKey)
	k.store.SetAccount(&acc)
	k.bus.PublishAccountCreated(acc.AccountId, acc.OwnerAddress, blockHeight)
	return acc, nil
}

func (k *AccountKeeper) GetAccount(id string) (NativeAccount, error) {
	acc, ok := k.store.GetAccount(id)
	if !ok {
		return NativeAccount{}, fmt.Errorf("account not found: %s", id)
	}
	return *acc, nil
}

func (k *AccountKeeper) CreateSession(accountId string, opts SessionOptions, blockHeight int64) (TradingSession, error) {
	_, ok := k.store.GetAccount(accountId)
	if !ok {
		return TradingSession{}, fmt.Errorf("account not found: %s", accountId)
	}
	sess := NewTradingSession(accountId, opts)
	k.store.SetSession(&sess)
	k.bus.PublishSessionCreated(sess.SessionId, sess.AccountId, blockHeight)
	return sess, nil
}

func (k *AccountKeeper) GetSession(id string) (TradingSession, error) {
	sess, ok := k.store.GetSession(id)
	if !ok {
		return TradingSession{}, fmt.Errorf("session not found: %s", id)
	}
	return *sess, nil
}

func (k *AccountKeeper) ValidateSession(sessionId, marketId string, blockHeight int64) (*TradingSession, error) {
	sess, ok := k.store.GetSession(sessionId)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionId)
	}
	if !sess.IsEnabled {
		return nil, fmt.Errorf("session disabled: %s", sessionId)
	}
	if sess.IsExpiredAt(blockHeight) {
		return nil, fmt.Errorf("session expired: %s", sessionId)
	}
	if !sess.CanTradeMarket(marketId) {
		return nil, fmt.Errorf("session %s not allowed for market %s", sessionId, marketId)
	}
	return sess, nil
}

func (k *AccountKeeper) IncrementSequence(accountId string) (uint64, error) {
	acc, ok := k.store.GetAccount(accountId)
	if !ok {
		return 0, fmt.Errorf("account not found: %s", accountId)
	}
	acc.AccountSequence++
	k.store.SetAccount(acc)
	return acc.AccountSequence, nil
}
