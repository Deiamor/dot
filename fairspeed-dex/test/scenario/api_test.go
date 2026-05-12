package scenario_test

// Phase 4: REST API + SSE streaming integration tests.
//
// Tests run against an httptest.Server wrapping a real LocalNode.
// Verifies: order submission, orderbook snapshot, trade history, SSE events.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/api"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// newTestServer creates a real LocalNode bootstrapped with Alice + Bob,
// wraps it in an API Server and returns an httptest.Server ready to receive requests.
func newTestServer(t *testing.T) (*httptest.Server, *node.LocalNode, string, string, string, string) {
	t.Helper()
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	srv := api.NewServer(n, "")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, n, aliceId, bobId, aliceSess, bobSess
}

// postOrder is a helper that POSTs an order and decodes the response.
func postOrder(t *testing.T, ts *httptest.Server, body api.SubmitOrderRequest) (api.OrderResponse, *http.Response) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/orders", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST /orders: %v", err)
	}
	var result api.OrderResponse
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return result, resp
}

// -------------------------------------------------------------------------
// Scenario 1: POST /orders — valid order submission
// -------------------------------------------------------------------------
func TestAPI_SubmitOrder_Valid(t *testing.T) {
	ts, _, _, bobId, _, bobSess := newTestServer(t)

	req := api.SubmitOrderRequest{
		AccountId:   bobId,
		SessionId:   bobSess,
		MarketId:    "BTC-USDC",
		Side:        "SELL",
		Price:       10_000,
		Quantity:    1,
		TimeInForce: "GTC",
	}
	result, resp := postOrder(t, ts, req)

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 Created, got %d", resp.StatusCode)
	}
	if result.OrderId == "" {
		t.Error("OrderId should not be empty")
	}
	if result.MarketId != "BTC-USDC" {
		t.Errorf("MarketId: want BTC-USDC got %s", result.MarketId)
	}
	if result.Side != "SELL" {
		t.Errorf("Side: want SELL got %s", result.Side)
	}
	// GTC SELL with no matching BUY → should be OPEN (resting in book)
	if result.Status != "OPEN" {
		t.Errorf("Status: want OPEN got %s", result.Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: POST /orders — validation errors
// -------------------------------------------------------------------------
func TestAPI_SubmitOrder_Validation(t *testing.T) {
	ts, _, _, _, _, _ := newTestServer(t)

	cases := []struct {
		name string
		body api.SubmitOrderRequest
	}{
		{"missing account_id", api.SubmitOrderRequest{SessionId: "s", MarketId: "BTC-USDC", Side: "BUY", Price: 1, Quantity: 1}},
		{"missing session_id", api.SubmitOrderRequest{AccountId: "a", MarketId: "BTC-USDC", Side: "BUY", Price: 1, Quantity: 1}},
		{"invalid side", api.SubmitOrderRequest{AccountId: "a", SessionId: "s", MarketId: "BTC-USDC", Side: "LONG", Price: 1, Quantity: 1}},
		{"zero price", api.SubmitOrderRequest{AccountId: "a", SessionId: "s", MarketId: "BTC-USDC", Side: "BUY", Price: 0, Quantity: 1}},
		{"zero quantity", api.SubmitOrderRequest{AccountId: "a", SessionId: "s", MarketId: "BTC-USDC", Side: "BUY", Price: 1, Quantity: 0}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.body)
			resp, err := http.Post(ts.URL+"/orders", "application/json", bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("POST /orders: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Scenario 3: GET /orderbook/{marketId} — snapshot
// -------------------------------------------------------------------------
func TestAPI_GetOrderBook(t *testing.T) {
	ts, _, _, bobId, _, bobSess := newTestServer(t)

	// Initially empty.
	resp, err := http.Get(ts.URL + "/orderbook/BTC-USDC")
	if err != nil {
		t.Fatalf("GET /orderbook: %v", err)
	}
	var ob api.OrderBookResponse
	_ = json.NewDecoder(resp.Body).Decode(&ob)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if len(ob.Bids) != 0 || len(ob.Asks) != 0 {
		t.Error("expected empty orderbook initially")
	}

	// Place a SELL order.
	postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: bobId, SessionId: bobSess,
		MarketId: "BTC-USDC", Side: "SELL", Price: 10_000, Quantity: 2, TimeInForce: "GTC",
	})

	// Orderbook should now have one ask level.
	resp, _ = http.Get(ts.URL + "/orderbook/BTC-USDC")
	_ = json.NewDecoder(resp.Body).Decode(&ob)
	resp.Body.Close()

	if len(ob.Asks) != 1 {
		t.Errorf("expected 1 ask level, got %d", len(ob.Asks))
	}
	if ob.Asks[0].Price != 10_000 {
		t.Errorf("ask price: want 10000 got %d", ob.Asks[0].Price)
	}
	if ob.Asks[0].Quantity != 2 {
		t.Errorf("ask quantity: want 2 got %d", ob.Asks[0].Quantity)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: GET /trades/{marketId} — trade history after fill
// -------------------------------------------------------------------------
func TestAPI_GetTrades(t *testing.T) {
	ts, _, aliceId, bobId, aliceSess, bobSess := newTestServer(t)

	// Bob sells, Alice buys → trade.
	postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: bobId, SessionId: bobSess,
		MarketId: "BTC-USDC", Side: "SELL", Price: 10_000, Quantity: 1, TimeInForce: "GTC",
	})
	result, resp := postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: aliceId, SessionId: aliceSess,
		MarketId: "BTC-USDC", Side: "BUY", Price: 10_000, Quantity: 1, TimeInForce: "GTC",
	})

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("BUY order failed: %d", resp.StatusCode)
	}
	if result.TradeCount != 1 {
		t.Errorf("expected TradeCount=1, got %d", result.TradeCount)
	}

	resp, err := http.Get(ts.URL + "/trades/BTC-USDC")
	if err != nil {
		t.Fatalf("GET /trades: %v", err)
	}
	var trades []api.TradeResponse
	_ = json.NewDecoder(resp.Body).Decode(&trades)
	resp.Body.Close()

	if len(trades) < 1 {
		t.Fatalf("expected >=1 trade in history, got %d", len(trades))
	}
	trade := trades[0]
	if trade.Price != 10_000 {
		t.Errorf("trade price: want 10000 got %d", trade.Price)
	}
	if trade.Quantity != 1 {
		t.Errorf("trade qty: want 1 got %d", trade.Quantity)
	}
	if trade.MakerFeeAmount != 2 {
		t.Errorf("maker fee: want 2 got %d", trade.MakerFeeAmount)
	}
	if trade.TakerFeeAmount != 5 {
		t.Errorf("taker fee: want 5 got %d", trade.TakerFeeAmount)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: GET /stream/orderbook — SSE receives update on order placement
// -------------------------------------------------------------------------
func TestAPI_StreamOrderBook_SSE(t *testing.T) {
	ts, _, _, bobId, _, bobSess := newTestServer(t)

	received := make(chan api.OrderBookResponse, 4)
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		resp, err := http.Get(ts.URL + "/stream/orderbook?market=BTC-USDC")
		if err != nil {
			return
		}
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var ob api.OrderBookResponse
				if err := json.Unmarshal([]byte(line[6:]), &ob); err == nil {
					received <- ob
					return // got first message, stop
				}
			}
		}
	}()

	// Give the SSE client time to connect.
	time.Sleep(50 * time.Millisecond)

	// Place an order to trigger an SSE broadcast.
	postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: bobId, SessionId: bobSess,
		MarketId: "BTC-USDC", Side: "SELL", Price: 10_000, Quantity: 3, TimeInForce: "GTC",
	})

	select {
	case ob := <-received:
		if len(ob.Asks) == 0 {
			t.Error("SSE orderbook snapshot has no asks after SELL order")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for SSE orderbook event")
	}
}

// -------------------------------------------------------------------------
// Scenario 6: GET /stream/trades — SSE receives trade event on fill
// -------------------------------------------------------------------------
func TestAPI_StreamTrades_SSE(t *testing.T) {
	ts, _, aliceId, bobId, aliceSess, bobSess := newTestServer(t)

	received := make(chan api.TradeResponse, 4)

	go func() {
		resp, err := http.Get(ts.URL + "/stream/trades")
		if err != nil {
			return
		}
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var tr api.TradeResponse
				if err := json.Unmarshal([]byte(line[6:]), &tr); err == nil {
					received <- tr
					return
				}
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Place SELL, then BUY → trade executes → SSE fires.
	postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: bobId, SessionId: bobSess,
		MarketId: "BTC-USDC", Side: "SELL", Price: 10_000, Quantity: 1, TimeInForce: "GTC",
	})
	postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: aliceId, SessionId: aliceSess,
		MarketId: "BTC-USDC", Side: "BUY", Price: 10_000, Quantity: 1, TimeInForce: "GTC",
	})

	select {
	case tr := <-received:
		if tr.Price != 10_000 {
			t.Errorf("SSE trade price: want 10000 got %d", tr.Price)
		}
		if tr.Quantity != 1 {
			t.Errorf("SSE trade qty: want 1 got %d", tr.Quantity)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for SSE trade event")
	}
}

// -------------------------------------------------------------------------
// Scenario 7: Partial fill through API — orderbook reflects remaining quantity
// -------------------------------------------------------------------------
func TestAPI_PartialFill_OrderBookResidual(t *testing.T) {
	ts, _, aliceId, bobId, aliceSess, bobSess := newTestServer(t)

	// Bob SELL 3 lots.
	postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: bobId, SessionId: bobSess,
		MarketId: "BTC-USDC", Side: "SELL", Price: 10_000, Quantity: 3, TimeInForce: "GTC",
	})
	// Alice BUY 1 lot → partial fill.
	result, _ := postOrder(t, ts, api.SubmitOrderRequest{
		AccountId: aliceId, SessionId: aliceSess,
		MarketId: "BTC-USDC", Side: "BUY", Price: 10_000, Quantity: 1, TimeInForce: "GTC",
	})
	if result.TradeCount != 1 {
		t.Errorf("expected 1 trade, got %d", result.TradeCount)
	}

	// Orderbook should still have 2 lots remaining ask.
	resp, _ := http.Get(ts.URL + "/orderbook/BTC-USDC")
	var ob api.OrderBookResponse
	_ = json.NewDecoder(resp.Body).Decode(&ob)
	resp.Body.Close()

	if len(ob.Asks) != 1 {
		t.Fatalf("expected 1 ask level after partial fill, got %d", len(ob.Asks))
	}
	if ob.Asks[0].Quantity != 2 {
		t.Errorf("remaining ask quantity: want 2 got %d", ob.Asks[0].Quantity)
	}
}

// -------------------------------------------------------------------------
// Scenario 8: Method not allowed
// -------------------------------------------------------------------------
func TestAPI_MethodNotAllowed(t *testing.T) {
	ts, _, _, _, _, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/orders")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}
