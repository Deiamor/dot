package fairbatch

import (
	"crypto/rand"
	"fmt"
)

type TransactionType int

const (
	TxCreateAccount TransactionType = iota
	TxCreateSession
	TxDeposit
	TxSubmitOrder
	TxCancelOrder
)

type Transaction struct {
	TxType    TransactionType
	Payload   any
	Signature string
	AccountId string
	SessionId string
}

type FairBatch struct {
	BatchId      string
	BlockHeight  int64
	Transactions []Transaction
	Timestamp    int64
}

func newBatchID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("bat-%x", b)
}
