package scenario_test

// Stage 4: KYC/AML + compliance tests.
//
// Verifies:
//  1. New accounts start with PENDING KYC status
//  2. Orders from PENDING accounts are rejected when RequireKYC=true
//  3. TxKYCApprove sets status to APPROVED, orders then accepted
//  4. KYCStatusRevoked blocks trading again
//  5. KYCStatusExempt (RequireKYC=false) always allows trading
//  6. AML alert fires when trade notional exceeds threshold
//  7. GET /reports/trades returns compliance-grade trade records
//  8. GET /reports/kyc/{accountId} returns correct KYC status

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/api"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// bootstrapNodeWithKYC creates a node with RequireKYC=true.
func bootstrapNodeWithKYC(t *testing.T) (*node.LocalNode, string, string, string, string) {
	t.Helper()
	policy := risk.RiskPolicy{
		MaxOrderQuantity:         1_000_000,
		MinOrderQuantity:         1,
		MaxDailyVolumePerSession: 100_000_000,
		RequireKYC:               true,
	}
	return bootstrapNodeWithPolicy(t, policy)
}

// -------------------------------------------------------------------------
// Scenario 1: New account starts PENDING
// -------------------------------------------------------------------------
func TestKYC_NewAccount_StatusPending(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithKYC(t)

	status := n.GetKYCStatus(aliceId)
	if status != account.KYCStatusPending {
		t.Errorf("new account KYC status: want PENDING got %s", status)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Order rejected when KYC=PENDING and RequireKYC=true
// -------------------------------------------------------------------------
func TestKYC_OrderRejected_WhenPending(t *testing.T) {
	n, aliceId, bobId, _, bobSess := bootstrapNodeWithKYC(t)

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		rejectedId = p.OrderId
	})

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}

	_ = aliceId
	if rejectedId == "" {
		t.Error("expected ORDER_REJECTED for KYC-pending account, got none")
	}
}

// -------------------------------------------------------------------------
// Scenario 3: TxKYCApprove → APPROVED → order accepted
// -------------------------------------------------------------------------
func TestKYC_Approve_ThenOrderAccepted(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNodeWithKYC(t)

	// Approve both accounts.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddKYCApprove(aliceId, "APPROVED").
		AddKYCApprove(bobId, "APPROVED").
		Build())
	if err != nil {
		t.Fatalf("KYCApprove batch: %v", err)
	}

	if n.GetKYCStatus(aliceId) != account.KYCStatusApproved {
		t.Error("alice KYC not APPROVED after TxKYCApprove")
	}
	if n.GetKYCStatus(bobId) != account.KYCStatusApproved {
		t.Error("bob KYC not APPROVED after TxKYCApprove")
	}

	// Now orders should go through.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())
	if err != nil {
		t.Fatalf("sell after KYC: %v", err)
	}

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	res, err = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("buy after KYC: %v", err)
	}
	if res.TradeCount != 1 {
		t.Errorf("expected 1 trade after KYC approval, got %d", res.TradeCount)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: REVOKED account cannot trade
// -------------------------------------------------------------------------
func TestKYC_Revoke_BlocksTrading(t *testing.T) {
	n, aliceId, bobId, _, bobSess := bootstrapNodeWithKYC(t)

	// Approve both, then revoke alice.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddKYCApprove(aliceId, "APPROVED").
		AddKYCApprove(bobId, "APPROVED").
		Build())

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddKYCApprove(bobId, "REVOKED").Build())
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if n.GetKYCStatus(bobId) != account.KYCStatusRevoked {
		t.Error("bob KYC not REVOKED after TxKYCApprove REVOKED")
	}

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		p := e.Payload.(state.OrderRejectedPayload)
		rejectedId = p.OrderId
	})

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 6)
	sell.AccountSequence = n.GetAccountSequence(bobId)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(sell).Build())
	if err != nil {
		t.Fatalf("revoked submit batch: %v", err)
	}
	if rejectedId == "" {
		t.Error("expected ORDER_REJECTED for REVOKED account")
	}
}

// -------------------------------------------------------------------------
// Scenario 5: RequireKYC=false (default) — PENDING accounts can still trade
// -------------------------------------------------------------------------
func TestKYC_RequireKYC_False_PendingCanTrade(t *testing.T) {
	// Default policy has RequireKYC=false
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// PENDING by default, but RequireKYC=false → allowed
	if n.GetKYCStatus(aliceId) != account.KYCStatusPending {
		t.Errorf("alice not PENDING, got %s", n.GetKYCStatus(aliceId))
	}

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("buy with RequireKYC=false: %v", err)
	}
	if res.TradeCount != 1 {
		t.Errorf("expected trade with RequireKYC=false, got %d", res.TradeCount)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: AML alert fires when notional > threshold
// -------------------------------------------------------------------------
func TestKYC_AML_AlertFired(t *testing.T) {
	policy := risk.RiskPolicy{
		MaxOrderQuantity:            1_000_000,
		MinOrderQuantity:            1,
		MaxDailyVolumePerSession:    100_000_000,
		RequireKYC:                  false,
		AMLSingleTradeLimitNotional: 5_000, // alert if notional > 5,000
	}
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNodeWithPolicy(t, policy)

	var amlFired state.AMLAlertPayload
	n.Subscribe(state.EventAMLAlert, func(e state.Event) {
		amlFired = e.Payload.(state.AMLAlertPayload)
	})

	// Trade: price=10_000, qty=1 → notional=10_000 > threshold=5_000 → should alert.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("AML trade: %v", err)
	}
	if res.TradeCount != 1 {
		t.Errorf("expected 1 trade, got %d", res.TradeCount)
	}
	if amlFired.Notional == 0 {
		t.Error("expected AML alert, got none")
	}
	if amlFired.Notional != 10_000 {
		t.Errorf("AML notional: want 10000 got %d", amlFired.Notional)
	}
	if amlFired.Threshold != 5_000 {
		t.Errorf("AML threshold: want 5000 got %d", amlFired.Threshold)
	}
}

// -------------------------------------------------------------------------
// Scenario 7: GET /reports/trades returns compliance-grade records
// -------------------------------------------------------------------------
func TestKYC_ComplianceReport_Trades(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)
	srv := api.NewServer(n, "")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Execute a trade.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())

	// Fetch compliance report.
	resp, err := http.Get(ts.URL + "/reports/trades")
	if err != nil {
		t.Fatalf("GET /reports/trades: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var records []compliance.TradeRecord
	_ = json.NewDecoder(resp.Body).Decode(&records)
	if len(records) < 1 {
		t.Fatalf("expected >=1 compliance record, got %d", len(records))
	}
	r := records[0]
	if r.Notional != 10_000 {
		t.Errorf("compliance record notional: want 10000 got %d", r.Notional)
	}
	if r.TotalFee != 7 { // makerFee=2 + takerFee=5
		t.Errorf("compliance record total fee: want 7 got %d", r.TotalFee)
	}
}

// -------------------------------------------------------------------------
// Scenario 8: GET /reports/kyc/{accountId} returns correct status
// -------------------------------------------------------------------------
func TestKYC_ReportEndpoint_KYCStatus(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNodeWithKYC(t)
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)
	srv := api.NewServer(n, "")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Initially PENDING.
	resp, _ := http.Get(ts.URL + "/reports/kyc/" + aliceId)
	var kycResp api.KYCStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&kycResp)
	resp.Body.Close()
	if kycResp.KYCStatus != "PENDING" {
		t.Errorf("initial KYC status: want PENDING got %s", kycResp.KYCStatus)
	}

	// Approve.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddKYCApprove(aliceId, "APPROVED").Build())

	resp, _ = http.Get(ts.URL + "/reports/kyc/" + aliceId)
	_ = json.NewDecoder(resp.Body).Decode(&kycResp)
	resp.Body.Close()
	if kycResp.KYCStatus != "APPROVED" {
		t.Errorf("after approve KYC status: want APPROVED got %s", kycResp.KYCStatus)
	}
}

// -------------------------------------------------------------------------
// Scenario 9: RequirePerpKYC=true — PERP orders rejected without KYC approval
// even when global RequireKYC=false.
// -------------------------------------------------------------------------
func TestKYC_PerpOrderRejectedWithoutKYC(t *testing.T) {
	// RequirePerpKYC=true, but RequireKYC=false — SPOT trading is still allowed
	// for PENDING accounts while PERP trading requires KYC.
	policy := risk.RiskPolicy{
		MaxOrderQuantity:         1_000_000,
		MinOrderQuantity:         1,
		MaxDailyVolumePerSession: 100_000_000,
		RequireKYC:               false, // SPOT still unrestricted
		RequirePerpKYC:           true,  // PERP requires KYC
	}
	n, aliceId, _, aliceSess, _ := bootstrapNodeWithPolicy(t, policy)

	// Deposit USDC and register a PERP market.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddDeposit(aliceId, "USDC", 1_000_000).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 100,
			MaxFundingRateBps:     200,
		}).Build())

	// Create a PERP-enabled session for Alice.
	var perpSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == aliceId && perpSess == "" {
			perpSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddCreateSession(aliceId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC-PERP"},
			MaxOrderAmount: 1_000_000,
		}).Build())
	_ = aliceSess // original BTC-USDC session unused here

	// Alice is PENDING — PERP order should be rejected.
	if n.GetKYCStatus(aliceId) != account.KYCStatusPending {
		t.Fatalf("expected PENDING KYC, got %s", n.GetKYCStatus(aliceId))
	}

	var rejectedOrderId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedOrderId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	perpOrder := clob.NewLimitOrder(aliceId, perpSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	perpOrder.AccountSequence = n.GetAccountSequence(aliceId)
	if _, err := n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(perpOrder).Build()); err != nil {
		t.Fatalf("submit perp order: %v", err)
	}
	if rejectedOrderId == "" {
		t.Error("expected PERP order to be rejected for PENDING KYC (RequireKYC=false globally)")
	}

	// After KYC approval the PERP order should be accepted.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddKYCApprove(aliceId, "APPROVED").Build())
	rejectedOrderId = ""

	perpOrder2 := clob.NewLimitOrder(aliceId, perpSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	perpOrder2.AccountSequence = n.GetAccountSequence(aliceId)
	if _, err := n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(perpOrder2).Build()); err != nil {
		t.Fatalf("submit perp order post-KYC: %v", err)
	}
	if rejectedOrderId != "" {
		t.Error("expected PERP order to be accepted after KYC approval")
	}
	if n.GetKYCStatus(aliceId) != account.KYCStatusApproved {
		t.Errorf("expected APPROVED after KYCApprove, got %s", n.GetKYCStatus(aliceId))
	}
}
