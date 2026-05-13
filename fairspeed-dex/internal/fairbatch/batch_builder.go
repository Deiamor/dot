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

type WithdrawPayload struct {
	AccountId       string
	AssetId         string
	Amount          int64
	AccountSequence uint64
	Signature       string // signed by WithdrawalPublicKey over TxHash
}

// KYCApprovePayload is submitted by a privileged admin to approve or revoke an account's KYC.
type KYCApprovePayload struct {
	AccountId string
	Status    string // "APPROVED" | "REVOKED" | "EXEMPT"
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

func (b *BatchBuilder) AddWithdraw(accountId, assetId string, amount int64, seq uint64, sig string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxWithdraw,
		AccountId: accountId,
		Payload: WithdrawPayload{
			AccountId:       accountId,
			AssetId:         assetId,
			Amount:          amount,
			AccountSequence: seq,
			Signature:       sig,
		},
	})
	return b
}

// AddKYCApprove adds an admin KYC status update transaction to the batch.
// status must be one of: "APPROVED", "REVOKED", "EXEMPT".
func (b *BatchBuilder) AddKYCApprove(accountId, status string) *BatchBuilder {
	b.txs = append(b.txs, Transaction{
		TxType:    TxKYCApprove,
		AccountId: accountId,
		Payload:   KYCApprovePayload{AccountId: accountId, Status: status},
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
