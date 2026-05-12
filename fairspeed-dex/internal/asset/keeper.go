package asset

import "fmt"

type BalanceStore interface {
	GetBalance(accountId, assetId string) *Balance
	SetBalance(b *Balance)
	GetAsset(id string) (*Asset, bool)
	SetAsset(a *Asset)
	Reserve(accountId, assetId string, amount int64) error
	Release(accountId, assetId string, amount int64) error
}

type EventPublisher interface {
	Publish(eventType string, blockHeight int64, payload any)
}

type AssetKeeper struct {
	store BalanceStore
}

func NewAssetKeeper(store BalanceStore) *AssetKeeper {
	return &AssetKeeper{store: store}
}

func (k *AssetKeeper) RegisterAsset(a Asset) {
	k.store.SetAsset(&a)
}

func (k *AssetKeeper) GetAsset(id string) (Asset, bool) {
	a, ok := k.store.GetAsset(id)
	if !ok || a == nil {
		return Asset{}, false
	}
	return *a, true
}

func (k *AssetKeeper) GetBalance(accountId, assetId string) Balance {
	b := k.store.GetBalance(accountId, assetId)
	if b == nil {
		return Balance{AccountId: accountId, AssetId: assetId}
	}
	return *b
}

func (k *AssetKeeper) SetBalance(b Balance) {
	k.store.SetBalance(&b)
}

func (k *AssetKeeper) Deposit(accountId, assetId string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("deposit amount must be positive")
	}
	b := k.GetBalance(accountId, assetId)
	b.Available += amount
	k.store.SetBalance(&b)
	return nil
}

func (k *AssetKeeper) Reserve(accountId, assetId string, amount int64) error {
	return k.store.Reserve(accountId, assetId, amount)
}

func (k *AssetKeeper) Release(accountId, assetId string, amount int64) error {
	return k.store.Release(accountId, assetId, amount)
}

// TransferReserved moves amount from sender's Reserved to recipient's Available.
func (k *AssetKeeper) TransferReserved(fromAccountId, toAccountId, assetId string, amount int64) error {
	from := k.GetBalance(fromAccountId, assetId)
	if from.Reserved < amount {
		return fmt.Errorf("insufficient reserved: account=%s asset=%s reserved=%d required=%d",
			fromAccountId, assetId, from.Reserved, amount)
	}
	from.Reserved -= amount
	k.store.SetBalance(&from)

	to := k.GetBalance(toAccountId, assetId)
	to.Available += amount
	k.store.SetBalance(&to)
	return nil
}

// DeductAvailable subtracts from Available (used for fee collection).
func (k *AssetKeeper) DeductAvailable(accountId, assetId string, amount int64) error {
	b := k.GetBalance(accountId, assetId)
	if b.Available < amount {
		return fmt.Errorf("insufficient available: account=%s asset=%s available=%d required=%d",
			accountId, assetId, b.Available, amount)
	}
	b.Available -= amount
	k.store.SetBalance(&b)
	return nil
}

// CreditAvailable adds to Available (used for fee treasury or reward).
func (k *AssetKeeper) CreditAvailable(accountId, assetId string, amount int64) {
	b := k.GetBalance(accountId, assetId)
	b.Available += amount
	k.store.SetBalance(&b)
}
