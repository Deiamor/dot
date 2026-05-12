package fairbatch

import (
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

type CreateAccountPayload struct {
	OwnerAddress        string
	RootPublicKey       string
	WithdrawalPublicKey string
}

type CreateSessionPayload struct {
	AccountId string
	Opts      account.SessionOptions
}

type DepositPayload struct {
	AccountId string
	AssetId   string
	Amount    int64
}

type SubmitOrderPayload struct {
	Order clob.Order
}

type CancelOrderPayload struct {
	OrderId   string
	AccountId string
}

type BatchBuilder struct {
	blockHeight int64
	txs         []Transaction
}

func NewBatchBuilder(blockHeight int64) *BatchBuilder {
	return &BatchBuilder{blockHeight: blockHeight}
}

func (b *BatchBuilder) AddCreateAccount(ownerAddr, rootKey, withdrawKey string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType: TxCreateAccount,
		Payload: CreateAccountPayload{
			OwnerAddress:        ownerAddr,
			RootPublicKey:       rootKey,
			WithdrawalPublicKey: withdrawKey,
		},
	})
	return b
}

func (b *BatchBuilder) AddCreateSession(accountId string, opts account.SessionOptions) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxCreateSession,
		AccountId: accountId,
		Payload:   CreateSessionPayload{AccountId: accountId, Opts: opts},
	})
	return b
}

func (b *BatchBuilder) AddDeposit(accountId, assetId string, amount int64) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxDeposit,
		AccountId: accountId,
		Payload:   DepositPayload{AccountId: accountId, AssetId: assetId, Amount: amount},
	})
	return b
}

func (b *BatchBuilder) AddSubmitOrder(o clob.Order) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxSubmitOrder,
		AccountId: o.AccountId,
		SessionId: o.SessionId,
		Payload:   SubmitOrderPayload{Order: o},
	})
	return b
}

func (b *BatchBuilder) AddCancelOrder(orderId, accountId string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxCancelOrder,
		AccountId: accountId,
		Payload:   CancelOrderPayload{OrderId: orderId, AccountId: accountId},
	})
	return b
}

func (b *BatchBuilder) Build() FairBatch {
	sorted, batchHash := SortAndHash(b.txs, b.blockHeight)
	return FairBatch{
		BatchId:      newBatchID(),
		BlockHeight:  b.blockHeight,
		Transactions: sorted,
		BatchHash:    batchHash,
		Timestamp:    time.Now().UnixNano(),
	}
}
