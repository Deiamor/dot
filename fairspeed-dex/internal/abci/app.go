package abci

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// DEXApplication implements Application and bridges the ABCI consensus
// protocol to the DEX LocalNode. In production, a CometBFT node calls
// these methods; in tests, we call them directly.
type DEXApplication struct {
	mu           sync.RWMutex
	node         *node.LocalNode
	lastAppHash  []byte
	snapshotPath string // empty = no persistence
}

var _ Application = (*DEXApplication)(nil)

// NewDEXApplication creates the ABCI application. Pass an empty snapshotPath
// to disable crash-recovery persistence (demo / test mode).
func NewDEXApplication(n *node.LocalNode, snapshotPath string) *DEXApplication {
	return &DEXApplication{node: n, snapshotPath: snapshotPath}
}

// Info returns the application version and the last committed block state.
// CometBFT calls this on startup to sync its view of the chain height.
func (a *DEXApplication) Info(_ RequestInfo) ResponseInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return ResponseInfo{
		AppVersion:       1,
		LastBlockHeight:  a.node.CurrentHeight(),
		LastBlockAppHash: a.lastAppHash,
	}
}

// InitChain is called once at genesis. We register the standard assets.
func (a *DEXApplication) InitChain(req RequestInitChain) ResponseInitChain {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.node.RegisterAsset(asset.BTC)
	a.node.RegisterAsset(asset.USDC)
	hash := a.computeAppHash()
	a.lastAppHash = hash
	return ResponseInitChain{AppHash: hash}
}

// CheckTx is the mempool gate: lightweight validation before a tx is gossiped.
// It does NOT modify state. For TxSubmitOrder, it also verifies the ed25519
// signature using the session key stored in AppState (best-effort — if the
// session key is empty, verification is skipped as in test/bootstrap mode).
func (a *DEXApplication) CheckTx(req RequestCheckTx) ResponseCheckTx {
	tx, err := DecodeTx(req.Tx)
	if err != nil {
		return ResponseCheckTx{Code: CodeError, Log: "decode: " + err.Error()}
	}
	if err := validateTxBasic(tx); err != nil {
		return ResponseCheckTx{Code: CodeError, Log: err.Error()}
	}

	// Signature verification for order submissions (best-effort with wire TxHash).
	if tx.TxType == fairbatch.TxSubmitOrder {
		p := tx.Payload.(fairbatch.SubmitOrderPayload)
		o := p.Order
		if sess, ok := a.node.GetSession(o.SessionId); ok && sess != nil && sess.SessionPublicKey != "" {
			if err := account.VerifyOrderSignature(tx.TxHash, o.Signature, sess.SessionPublicKey); err != nil {
				return ResponseCheckTx{Code: CodeError, Log: "invalid signature: " + err.Error()}
			}
		}
	}

	return ResponseCheckTx{Code: CodeOK}
}

// PrepareProposal is called on the current block proposer. It decodes all
// candidate transactions, applies FairBatch ordering (sort by TxHash), and
// returns the sorted byte slices. This guarantees deterministic execution
// order across all validators regardless of tx arrival order.
func (a *DEXApplication) PrepareProposal(req RequestPrepareProposal) ResponsePrepareProposal {
	txs, errs := decodeTxs(req.Txs)
	// Drop invalid txs silently (they would fail CheckTx too).
	var valid []fairbatch.Transaction
	for i, tx := range txs {
		if errs[i] == nil {
			valid = append(valid, tx)
		}
	}

	sorted, _ := fairbatch.SortAndHash(valid, req.Height)

	out := make([][]byte, 0, len(sorted))
	for _, tx := range sorted {
		raw, err := EncodeTx(tx)
		if err == nil {
			out = append(out, raw)
		}
	}
	return ResponsePrepareProposal{Txs: out}
}

// ProcessProposal is called on every validator to verify the proposer's block.
// It checks that the proposed tx ordering matches the canonical FairBatch sort.
// A single out-of-order tx causes the entire block to be rejected, which will
// trigger a new round and a new proposer.
func (a *DEXApplication) ProcessProposal(req RequestProcessProposal) ResponseProcessProposal {
	txs, errs := decodeTxs(req.Txs)
	for _, err := range errs {
		if err != nil {
			return ResponseProcessProposal{Status: ProcessProposalReject}
		}
	}

	// Recompute TxHash from payload (do not trust what's on the wire).
	for i := range txs {
		txs[i].TxHash = fairbatch.ComputeTxHash(txs[i], req.Height)
	}

	// Compute what the canonical ordering should be.
	canonical, _ := fairbatch.SortAndHash(txs, req.Height)

	// Verify proposed order == canonical order by comparing TxHashes positionally.
	if len(canonical) != len(txs) {
		return ResponseProcessProposal{Status: ProcessProposalReject}
	}
	for i := range canonical {
		if canonical[i].TxHash != txs[i].TxHash {
			return ResponseProcessProposal{Status: ProcessProposalReject}
		}
	}
	return ResponseProcessProposal{Status: ProcessProposalAccept}
}

// FinalizeBlock executes the block. It reconstructs a FairBatch from the
// (already sorted) proposed txs and calls LocalNode.SubmitBatch.
// The block height is provided by CometBFT and must equal CurrentHeight+1.
func (a *DEXApplication) FinalizeBlock(req RequestFinalizeBlock) ResponseFinalizeBlock {
	a.mu.Lock()
	defer a.mu.Unlock()

	txs, errs := decodeTxs(req.Txs)
	results := make([]*ExecTxResult, len(req.Txs))
	for i, err := range errs {
		if err != nil {
			results[i] = &ExecTxResult{Code: CodeError, Log: err.Error()}
		} else {
			results[i] = &ExecTxResult{Code: CodeOK}
		}
	}

	// Only re-hash (don't re-sort) — ordering was already verified by ProcessProposal.
	sorted, batchHash := fairbatch.SortAndHash(txs, req.Height)

	batch := fairbatch.FairBatch{
		BatchId:      fmt.Sprintf("blk-%d", req.Height),
		BlockHeight:  req.Height,
		Transactions: sorted,
		BatchHash:    batchHash,
	}

	blockResult, err := a.node.SubmitBatch(batch)
	if err != nil {
		// Return a failed block — in real CometBFT this would panic or halt.
		return ResponseFinalizeBlock{
			TxResults: results,
			AppHash:   a.lastAppHash,
		}
	}

	// Mark individual tx results from block outcome.
	for i := range results {
		if results[i].Code == CodeOK {
			_ = blockResult // all txs processed; individual errors not surfaced here
		}
	}

	hash := a.computeAppHash()
	a.lastAppHash = hash
	return ResponseFinalizeBlock{TxResults: results, AppHash: hash}
}

// Commit signals that the block has been committed. Persists a state snapshot
// to disk when snapshotPath is configured.
func (a *DEXApplication) Commit(_ RequestCommit) ResponseCommit {
	if a.snapshotPath != "" {
		if err := a.node.SaveSnapshot(a.snapshotPath); err != nil {
			fmt.Printf("snapshot save failed: %v\n", err)
		}
	}
	return ResponseCommit{RetainHeight: 0}
}

// Query reads state without modifying it. Supported paths:
//
//	/orderbook/{marketId}              → JSON OrderBook L2 snapshot
//	/trades/{marketId}                 → JSON []TradeExecution
//	/balance/{accountId}/{assetId}     → JSON Balance
func (a *DEXApplication) Query(req RequestQuery) ResponseQuery {
	path := strings.TrimPrefix(req.Path, "/")
	parts := strings.SplitN(path, "/", 3)

	switch parts[0] {
	case "orderbook":
		if len(parts) < 2 {
			return errQuery("usage: /orderbook/{marketId}")
		}
		ob, ok := a.node.GetOrderBook(parts[1])
		if !ok {
			return errQuery("orderbook not found: " + parts[1])
		}
		val, _ := json.Marshal(ob)
		return ResponseQuery{Code: CodeOK, Value: val}

	case "trades":
		if len(parts) < 2 {
			return errQuery("usage: /trades/{marketId}")
		}
		trades := a.node.TradesForMarket(parts[1])
		val, _ := json.Marshal(trades)
		return ResponseQuery{Code: CodeOK, Value: val}

	case "balance":
		if len(parts) < 3 {
			return errQuery("usage: /balance/{accountId}/{assetId}")
		}
		bal := a.node.GetBalance(parts[1], parts[2])
		val, _ := json.Marshal(bal)
		return ResponseQuery{Code: CodeOK, Value: val}

	default:
		return errQuery("unknown path: " + req.Path)
	}
}

// computeAppHash returns a SHA-256 of the current chain state.
// In production this would be a Merkle root over all state trees.
func (a *DEXApplication) computeAppHash() []byte {
	height := a.node.CurrentHeight()
	trades := a.node.AllTrades()

	// Sort trade IDs for determinism.
	ids := make([]string, len(trades))
	for i, t := range trades {
		ids[i] = t.TradeId
	}
	sort.Strings(ids)

	data := fmt.Sprintf("h=%d:trades=%d:%s", height, len(trades), strings.Join(ids, ","))
	h := sha256.Sum256([]byte(data))
	return h[:]
}

// ---- helpers ------------------------------------------------------------

func decodeTxs(rawTxs [][]byte) ([]fairbatch.Transaction, []error) {
	txs := make([]fairbatch.Transaction, len(rawTxs))
	errs := make([]error, len(rawTxs))
	for i, raw := range rawTxs {
		tx, err := DecodeTx(raw)
		txs[i] = tx
		errs[i] = err
	}
	return txs, errs
}

func validateTxBasic(tx fairbatch.Transaction) error {
	switch tx.TxType {
	case fairbatch.TxCreateAccount:
		p := tx.Payload.(fairbatch.CreateAccountPayload)
		if p.OwnerAddress == "" {
			return fmt.Errorf("CreateAccount: missing owner address")
		}
	case fairbatch.TxCreateSession:
		p := tx.Payload.(fairbatch.CreateSessionPayload)
		if p.AccountId == "" {
			return fmt.Errorf("CreateSession: missing account ID")
		}
	case fairbatch.TxDeposit:
		p := tx.Payload.(fairbatch.DepositPayload)
		if p.AccountId == "" || p.AssetId == "" || p.Amount <= 0 {
			return fmt.Errorf("Deposit: invalid fields")
		}
	case fairbatch.TxSubmitOrder:
		p := tx.Payload.(fairbatch.SubmitOrderPayload)
		o := p.Order
		if o.AccountId == "" || o.SessionId == "" || o.MarketId == "" {
			return fmt.Errorf("SubmitOrder: missing required fields")
		}
		if o.Price <= 0 || o.Quantity <= 0 {
			return fmt.Errorf("SubmitOrder: price and quantity must be positive")
		}
	case fairbatch.TxCancelOrder:
		p := tx.Payload.(fairbatch.CancelOrderPayload)
		if p.OrderId == "" || p.AccountId == "" {
			return fmt.Errorf("CancelOrder: missing order or account ID")
		}
	case fairbatch.TxWithdraw:
		p := tx.Payload.(fairbatch.WithdrawPayload)
		if p.AccountId == "" || p.AssetId == "" || p.Amount <= 0 {
			return fmt.Errorf("Withdraw: invalid fields")
		}
	}
	return nil
}

func errQuery(msg string) ResponseQuery {
	return ResponseQuery{Code: CodeError, Log: msg}
}
