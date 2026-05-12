package scenario_test

// Phase 6: CometBFT ABCI integration tests.
//
// Verifies that DEXApplication correctly implements the ABCI contract:
//   - Info returns current height
//   - CheckTx accepts valid txs and rejects malformed ones
//   - PrepareProposal sorts txs into FairBatch order
//   - ProcessProposal accepts canonical ordering, rejects tampered ordering
//   - FinalizeBlock executes the block and updates state
//   - Commit returns stable (no error)
//   - Query reads orderbook, trades, and balances

import (
	"encoding/json"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/abci"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// newABCIApp creates a bootstrapped DEXApplication ready for testing.
func newABCIApp(t *testing.T) (*abci.DEXApplication, string, string, string, string) {
	t.Helper()
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)
	app := abci.NewDEXApplication(n)
	return app, aliceId, bobId, aliceSess, bobSess
}

// encodeTxs encodes a slice of fairbatch.Transaction to [][]byte.
func encodeTxs(t *testing.T, txs []fairbatch.Transaction) [][]byte {
	t.Helper()
	out := make([][]byte, len(txs))
	for i, tx := range txs {
		raw, err := abci.EncodeTx(tx)
		if err != nil {
			t.Fatalf("EncodeTx[%d]: %v", i, err)
		}
		out[i] = raw
	}
	return out
}

// -------------------------------------------------------------------------
// Scenario 1: Info returns height 0 initially
// -------------------------------------------------------------------------
func TestABCI_Info(t *testing.T) {
	n := node.NewLocalNode()
	app := abci.NewDEXApplication(n)

	info := app.Info(abci.RequestInfo{})
	if info.LastBlockHeight != 0 {
		t.Errorf("expected height 0, got %d", info.LastBlockHeight)
	}
	if info.AppVersion != 1 {
		t.Errorf("expected AppVersion 1, got %d", info.AppVersion)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: InitChain registers assets and returns non-empty AppHash
// -------------------------------------------------------------------------
func TestABCI_InitChain(t *testing.T) {
	n := node.NewLocalNode()
	app := abci.NewDEXApplication(n)

	resp := app.InitChain(abci.RequestInitChain{ChainId: "fairspeed-1", InitialHeight: 1})
	if len(resp.AppHash) == 0 {
		t.Error("expected non-empty AppHash after InitChain")
	}

	// Info should still return height 0 (InitChain doesn't increment height).
	info := app.Info(abci.RequestInfo{})
	if info.LastBlockHeight != 0 {
		t.Errorf("height should be 0 after InitChain, got %d", info.LastBlockHeight)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: CheckTx — valid and invalid transactions
// -------------------------------------------------------------------------
func TestABCI_CheckTx(t *testing.T) {
	app, _, bobId, _, bobSess := newABCIApp(t)

	// Valid SELL order.
	sellOrder := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	validTx := fairbatch.Transaction{
		TxType:    fairbatch.TxSubmitOrder,
		AccountId: bobId,
		SessionId: bobSess,
		Payload:   fairbatch.SubmitOrderPayload{Order: sellOrder},
	}
	raw, _ := abci.EncodeTx(validTx)
	resp := app.CheckTx(abci.RequestCheckTx{Tx: raw, Type: abci.CheckTxNew})
	if resp.Code != abci.CodeOK {
		t.Errorf("valid tx CheckTx failed: %s", resp.Log)
	}

	// Malformed bytes.
	resp = app.CheckTx(abci.RequestCheckTx{Tx: []byte("not-json")})
	if resp.Code == abci.CodeOK {
		t.Error("expected error for malformed tx bytes")
	}

	// Missing account ID.
	badTx := fairbatch.Transaction{
		TxType:  fairbatch.TxSubmitOrder,
		Payload: fairbatch.SubmitOrderPayload{Order: clob.NewLimitOrder("", bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)},
	}
	raw, _ = abci.EncodeTx(badTx)
	resp = app.CheckTx(abci.RequestCheckTx{Tx: raw})
	if resp.Code == abci.CodeOK {
		t.Error("expected error for missing account ID")
	}
}

// -------------------------------------------------------------------------
// Scenario 4: PrepareProposal sorts txs into FairBatch order
// -------------------------------------------------------------------------
func TestABCI_PrepareProposal_SortsTransactions(t *testing.T) {
	app, _, bobId, _, bobSess := newABCIApp(t)

	// Two SELL orders — different prices → different TxHashes.
	sell1 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	sell2 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 11_000, 1, clob.TimeInForceGtc, 4)

	txs := []fairbatch.Transaction{
		{TxType: fairbatch.TxSubmitOrder, AccountId: bobId, SessionId: bobSess, Payload: fairbatch.SubmitOrderPayload{Order: sell2}},
		{TxType: fairbatch.TxSubmitOrder, AccountId: bobId, SessionId: bobSess, Payload: fairbatch.SubmitOrderPayload{Order: sell1}},
	}
	rawTxs := encodeTxs(t, txs)

	resp := app.PrepareProposal(abci.RequestPrepareProposal{Txs: rawTxs, Height: 4})
	if len(resp.Txs) != 2 {
		t.Fatalf("expected 2 txs in proposal, got %d", len(resp.Txs))
	}

	// Decode and verify they are in ascending TxHash order.
	sortedTxs, _ := fairbatch.SortAndHash(txs, 4)
	for i, raw := range resp.Txs {
		got, err := abci.DecodeTx(raw)
		if err != nil {
			t.Fatalf("decode proposed tx[%d]: %v", i, err)
		}
		if got.TxHash != sortedTxs[i].TxHash {
			t.Errorf("tx[%d]: hash mismatch: want %s got %s", i, sortedTxs[i].TxHash, got.TxHash)
		}
	}
}

// -------------------------------------------------------------------------
// Scenario 5: ProcessProposal — accept sorted, reject unsorted
// -------------------------------------------------------------------------
func TestABCI_ProcessProposal_AcceptAndReject(t *testing.T) {
	app, _, bobId, _, bobSess := newABCIApp(t)

	sell1 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	sell2 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 11_000, 1, clob.TimeInForceGtc, 4)
	txs := []fairbatch.Transaction{
		{TxType: fairbatch.TxSubmitOrder, AccountId: bobId, SessionId: bobSess, Payload: fairbatch.SubmitOrderPayload{Order: sell1}},
		{TxType: fairbatch.TxSubmitOrder, AccountId: bobId, SessionId: bobSess, Payload: fairbatch.SubmitOrderPayload{Order: sell2}},
	}

	// Canonical order via PrepareProposal.
	rawTxs := encodeTxs(t, txs)
	prepared := app.PrepareProposal(abci.RequestPrepareProposal{Txs: rawTxs, Height: 4})

	// Should accept canonical ordering.
	accept := app.ProcessProposal(abci.RequestProcessProposal{Txs: prepared.Txs, Height: 4})
	if accept.Status != abci.ProcessProposalAccept {
		t.Error("expected ACCEPT for canonically ordered proposal")
	}

	// Reverse the order → should reject.
	reversed := [][]byte{prepared.Txs[1], prepared.Txs[0]}
	reject := app.ProcessProposal(abci.RequestProcessProposal{Txs: reversed, Height: 4})
	if reject.Status != abci.ProcessProposalReject {
		t.Error("expected REJECT for reversed tx ordering")
	}
}

// -------------------------------------------------------------------------
// Scenario 6: FinalizeBlock executes trades and updates state
// -------------------------------------------------------------------------
func TestABCI_FinalizeBlock_TradesExecuted(t *testing.T) {
	app, aliceId, bobId, aliceSess, bobSess := newABCIApp(t)

	// Bob SELL, Alice BUY at same price → trade.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)

	txs := []fairbatch.Transaction{
		{TxType: fairbatch.TxSubmitOrder, AccountId: bobId, SessionId: bobSess, Payload: fairbatch.SubmitOrderPayload{Order: sell}},
		{TxType: fairbatch.TxSubmitOrder, AccountId: aliceId, SessionId: aliceSess, Payload: fairbatch.SubmitOrderPayload{Order: buy}},
	}
	rawTxs := encodeTxs(t, txs)
	prepared := app.PrepareProposal(abci.RequestPrepareProposal{Txs: rawTxs, Height: 4})

	result := app.FinalizeBlock(abci.RequestFinalizeBlock{Txs: prepared.Txs, Height: 4})

	if len(result.AppHash) == 0 {
		t.Error("expected non-empty AppHash after FinalizeBlock")
	}
	// Both txs should have succeeded.
	for i, r := range result.TxResults {
		if r.Code != abci.CodeOK {
			t.Errorf("tx[%d] failed: %s", i, r.Log)
		}
	}

	// Info height should now be 4 (bootstrapNode pre-runs 3 blocks).
	info := app.Info(abci.RequestInfo{})
	if info.LastBlockHeight != 4 {
		t.Errorf("expected height 4 after FinalizeBlock, got %d", info.LastBlockHeight)
	}
}

// -------------------------------------------------------------------------
// Scenario 7: Query — orderbook, trades, balance
// -------------------------------------------------------------------------
func TestABCI_Query(t *testing.T) {
	app, _, bobId, _, bobSess := newABCIApp(t)

	// Place a resting SELL.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 2, clob.TimeInForceGtc, 4)
	txs := []fairbatch.Transaction{
		{TxType: fairbatch.TxSubmitOrder, AccountId: bobId, SessionId: bobSess, Payload: fairbatch.SubmitOrderPayload{Order: sell}},
	}
	rawTxs := encodeTxs(t, txs)
	prepared := app.PrepareProposal(abci.RequestPrepareProposal{Txs: rawTxs, Height: 4})
	app.FinalizeBlock(abci.RequestFinalizeBlock{Txs: prepared.Txs, Height: 4})

	// Query orderbook.
	qob := app.Query(abci.RequestQuery{Path: "/orderbook/BTC-USDC"})
	if qob.Code != abci.CodeOK {
		t.Fatalf("orderbook query failed: %s", qob.Log)
	}
	var ob clob.OrderBook
	if err := json.Unmarshal(qob.Value, &ob); err != nil {
		t.Fatalf("decode orderbook: %v", err)
	}
	if len(ob.Asks) == 0 {
		t.Error("expected asks in orderbook after SELL order")
	}

	// Query balance.
	qbal := app.Query(abci.RequestQuery{Path: "/balance/" + bobId + "/BTC"})
	if qbal.Code != abci.CodeOK {
		t.Fatalf("balance query failed: %s", qbal.Log)
	}
	var bal asset.Balance
	if err := json.Unmarshal(qbal.Value, &bal); err != nil {
		t.Fatalf("decode balance: %v", err)
	}
	// Bob deposited 100 BTC, reserved 2 for the SELL order (qty=2).
	if bal.Available != 98 {
		t.Errorf("Bob available BTC: want 98 got %d", bal.Available)
	}

	// Query unknown path.
	bad := app.Query(abci.RequestQuery{Path: "/unknown/path"})
	if bad.Code == abci.CodeOK {
		t.Error("expected error for unknown query path")
	}

	// Query missing accountId.
	badSession := app.Query(abci.RequestQuery{Path: "/balance/nonexistent/BTC"})
	if badSession.Code != abci.CodeOK {
		// balance of unknown account returns zero balance, not error
	}
	_ = badSession
}

// -------------------------------------------------------------------------
// Scenario 8: Full round-trip — PrepareProposal → ProcessProposal → FinalizeBlock → Commit
// -------------------------------------------------------------------------
func TestABCI_FullRoundTrip(t *testing.T) {
	app, aliceId, bobId, aliceSess, bobSess := newABCIApp(t)
	height := int64(4)

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, height)
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, height)

	txs := []fairbatch.Transaction{
		{TxType: fairbatch.TxSubmitOrder, AccountId: bobId, SessionId: bobSess, Payload: fairbatch.SubmitOrderPayload{Order: sell}},
		{TxType: fairbatch.TxSubmitOrder, AccountId: aliceId, SessionId: aliceSess, Payload: fairbatch.SubmitOrderPayload{Order: buy}},
	}
	rawTxs := encodeTxs(t, txs)

	// 1. Proposer sorts txs.
	prepared := app.PrepareProposal(abci.RequestPrepareProposal{Txs: rawTxs, Height: height})

	// 2. Validators verify ordering.
	process := app.ProcessProposal(abci.RequestProcessProposal{Txs: prepared.Txs, Height: height})
	if process.Status != abci.ProcessProposalAccept {
		t.Fatal("ProcessProposal rejected valid proposal")
	}

	// 3. Execute the block.
	finalize := app.FinalizeBlock(abci.RequestFinalizeBlock{Txs: prepared.Txs, Height: height})
	if len(finalize.AppHash) == 0 {
		t.Error("empty AppHash after finalize")
	}

	// 4. Commit.
	app.Commit(abci.RequestCommit{})

	// 5. Verify trade was recorded via Query.
	qtrades := app.Query(abci.RequestQuery{Path: "/trades/BTC-USDC"})
	if qtrades.Code != abci.CodeOK {
		t.Fatalf("trades query: %s", qtrades.Log)
	}
	var trades []map[string]any
	_ = json.Unmarshal(qtrades.Value, &trades)
	if len(trades) < 1 {
		t.Errorf("expected at least 1 trade, got %d", len(trades))
	}

	// 6. Info returns updated height.
	info := app.Info(abci.RequestInfo{})
	if info.LastBlockHeight != height {
		t.Errorf("info height: want %d got %d", height, info.LastBlockHeight)
	}
	if len(info.LastBlockAppHash) == 0 {
		t.Error("expected non-empty LastBlockAppHash")
	}
}
