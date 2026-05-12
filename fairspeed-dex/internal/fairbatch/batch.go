package fairbatch

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
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
	TxHash    string // deterministic hash of the transaction content
}

type FairBatch struct {
	BatchId      string
	BlockHeight  int64
	Transactions []Transaction
	BatchHash    string // hash of all tx hashes in sorted order
	Timestamp    int64
}

// SortAndHash computes TxHash for each transaction, sorts by TxHash,
// then computes BatchHash. Returns a new sorted slice.
func SortAndHash(txs []Transaction, blockHeight int64) (sorted []Transaction, batchHash string) {
	hashed := make([]Transaction, len(txs))
	for i, tx := range txs {
		tx.TxHash = computeTxHash(tx, blockHeight)
		hashed[i] = tx
	}
	sort.Slice(hashed, func(i, j int) bool {
		return hashed[i].TxHash < hashed[j].TxHash
	})
	batchHash = computeBatchHash(hashed, blockHeight)
	return hashed, batchHash
}

// computeTxHash produces a deterministic hash from the non-random fields of a transaction.
// Random order IDs are excluded; only the semantic content is hashed.
func computeTxHash(tx Transaction, blockHeight int64) string {
	var data string
	data += strconv.Itoa(int(tx.TxType))
	data += ":" + tx.AccountId
	data += ":" + tx.SessionId
	data += ":" + tx.Signature

	switch p := tx.Payload.(type) {
	case CreateAccountPayload:
		data += ":" + p.OwnerAddress + ":" + p.RootPublicKey + ":" + p.WithdrawalPublicKey
	case CreateSessionPayload:
		data += ":" + p.AccountId
		for _, m := range p.Opts.AllowedMarkets {
			data += ":" + m
		}
		data += ":" + strconv.FormatInt(p.Opts.MaxOrderAmount, 10)
	case DepositPayload:
		data += ":" + p.AccountId + ":" + p.AssetId + ":" + strconv.FormatInt(p.Amount, 10)
	case SubmitOrderPayload:
		o := p.Order
		data += ":" + o.AccountId + ":" + o.SessionId + ":" + o.MarketId
		data += ":" + string(o.Side) + ":" + string(o.OrderType)
		data += ":" + strconv.FormatInt(o.Price, 10)
		data += ":" + strconv.FormatInt(o.Quantity, 10)
		data += ":" + string(o.TimeInForce)
		data += ":" + o.ClientOrderId
		data += ":" + strconv.FormatUint(o.AccountSequence, 10)
		data += ":" + o.Signature
	case CancelOrderPayload:
		data += ":" + p.OrderId + ":" + p.AccountId
	}

	data += ":" + strconv.FormatInt(blockHeight, 10)
	sum := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", sum)
}

func computeBatchHash(sorted []Transaction, blockHeight int64) string {
	combined := strconv.FormatInt(blockHeight, 10)
	for _, tx := range sorted {
		combined += ":" + tx.TxHash
	}
	sum := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", sum)
}

func newBatchID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("bat-%x", b)
}
