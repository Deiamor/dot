package binance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/binance"
)

// newTestClient creates a Client wired to a test HTTP server.
// The server handles routes via the provided mux; if nil, a default is used.
func newTestClient(t *testing.T, handler http.Handler) (*binance.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := binance.NewWithBaseURL(srv.URL, binance.WithKey("test-api-key", "test-secret"))
	return c, srv
}

func jsonResp(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("jsonResp encode: %v", err)
	}
}

// ─── MarketSnapshot ───────────────────────────────────────────────────────────

func TestMarketSnapshot_ParsesBidAskAndFunding(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/ticker/bookTicker", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(t, w, map[string]string{
			"symbol":   "BTCUSDT",
			"bidPrice": "60000.50",
			"askPrice": "60001.50",
		})
	})
	mux.HandleFunc("/fapi/v1/premiumIndex", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(t, w, map[string]string{
			"symbol":          "BTCUSDT",
			"markPrice":       "60001.00",
			"indexPrice":      "59999.00",
			"lastFundingRate": "0.0001",
		})
	})

	c, _ := newTestClient(t, mux)
	snap, err := c.MarketSnapshot(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("MarketSnapshot: %v", err)
	}

	if snap.Bid != 60000.50 {
		t.Errorf("Bid: got %g want 60000.50", snap.Bid)
	}
	if snap.Ask != 60001.50 {
		t.Errorf("Ask: got %g want 60001.50", snap.Ask)
	}
	if snap.Mid != 60001.0 {
		t.Errorf("Mid: got %g want 60001.0", snap.Mid)
	}
	if snap.MarkPrice != 60001.0 {
		t.Errorf("MarkPrice: got %g want 60001.0", snap.MarkPrice)
	}
	if snap.IndexPrice != 59999.0 {
		t.Errorf("IndexPrice: got %g want 59999.0", snap.IndexPrice)
	}
	if snap.FundingRate != 0.0001 {
		t.Errorf("FundingRate: got %g want 0.0001", snap.FundingRate)
	}
	if snap.Symbol != "BTCUSDT" {
		t.Errorf("Symbol: got %s want BTCUSDT", snap.Symbol)
	}
}

// ─── OrderBook ────────────────────────────────────────────────────────────────

func TestOrderBook_ParsesBidsAsks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/depth", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(t, w, map[string]any{
			"bids": [][]string{{"60000", "1.5"}, {"59999", "2.0"}},
			"asks": [][]string{{"60001", "1.2"}, {"60002", "3.0"}},
		})
	})

	c, _ := newTestClient(t, mux)
	ob, err := c.OrderBook(context.Background(), "BTCUSDT", 5)
	if err != nil {
		t.Fatalf("OrderBook: %v", err)
	}
	if len(ob.Bids) != 2 {
		t.Errorf("Bids: got %d want 2", len(ob.Bids))
	}
	if ob.Bids[0].Price != 60000 {
		t.Errorf("Bids[0].Price: got %g want 60000", ob.Bids[0].Price)
	}
	if len(ob.Asks) != 2 {
		t.Errorf("Asks: got %d want 2", len(ob.Asks))
	}
}

// ─── Position ─────────────────────────────────────────────────────────────────

func TestPosition_ParsesLongPosition(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v2/positionRisk", func(w http.ResponseWriter, r *http.Request) {
		requireSignature(t, r)
		jsonResp(t, w, []map[string]string{{
			"symbol":           "BTCUSDT",
			"positionAmt":      "0.5",
			"entryPrice":       "59800.0",
			"unRealizedProfit": "100.0",
			"leverage":         "10",
		}})
	})

	c, _ := newTestClient(t, mux)
	pos, err := c.Position(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if pos.Qty != 0.5 {
		t.Errorf("Qty: got %g want 0.5", pos.Qty)
	}
	if pos.AvgEntryPrice != 59800.0 {
		t.Errorf("AvgEntryPrice: got %g want 59800.0", pos.AvgEntryPrice)
	}
	if pos.Leverage != 10 {
		t.Errorf("Leverage: got %g want 10", pos.Leverage)
	}
	if !pos.IsLong() {
		t.Error("expected IsLong() = true")
	}
}

func TestPosition_FlatWhenSymbolAbsent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v2/positionRisk", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(t, w, []map[string]string{})
	})

	c, _ := newTestClient(t, mux)
	pos, err := c.Position(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if !pos.IsFlat() {
		t.Errorf("expected flat position, got Qty=%g", pos.Qty)
	}
}

func TestPosition_ShortPosition(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v2/positionRisk", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(t, w, []map[string]string{{
			"symbol":      "BTCUSDT",
			"positionAmt": "-0.3",
			"entryPrice":  "61000.0",
			"leverage":    "5",
		}})
	})

	c, _ := newTestClient(t, mux)
	pos, err := c.Position(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if !pos.IsShort() {
		t.Errorf("expected IsShort()=true, got Qty=%g", pos.Qty)
	}
}

// ─── Balances ─────────────────────────────────────────────────────────────────

func TestBalances_ReturnsNonZeroAssets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v2/account", func(w http.ResponseWriter, r *http.Request) {
		requireSignature(t, r)
		jsonResp(t, w, map[string]any{
			"assets": []map[string]string{
				{"asset": "USDT", "balance": "1000.0", "availableBalance": "800.0"},
				{"asset": "BNB", "balance": "0", "availableBalance": "0"},
			},
		})
	})

	c, _ := newTestClient(t, mux)
	bals, err := c.Balances(context.Background())
	if err != nil {
		t.Fatalf("Balances: %v", err)
	}
	if len(bals) != 1 {
		t.Fatalf("expected 1 non-zero balance, got %d", len(bals))
	}
	if bals[0].Asset != "USDT" {
		t.Errorf("Asset: got %s want USDT", bals[0].Asset)
	}
	if bals[0].Available != 800.0 {
		t.Errorf("Available: got %g want 800.0", bals[0].Available)
	}
}

// ─── OpenOrders ───────────────────────────────────────────────────────────────

func TestOpenOrders_ParsedCorrectly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/openOrders", func(w http.ResponseWriter, r *http.Request) {
		requireSignature(t, r)
		jsonResp(t, w, []map[string]any{{
			"orderId":     int64(123456),
			"symbol":      "BTCUSDT",
			"side":        "BUY",
			"type":        "LIMIT",
			"price":       "59500.0",
			"origQty":     "0.01",
			"executedQty": "0.0",
			"status":      "NEW",
			"time":        int64(1700000000000),
		}})
	})

	c, _ := newTestClient(t, mux)
	orders, err := c.OpenOrders(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenOrders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	o := orders[0]
	if o.ID != "123456" {
		t.Errorf("ID: got %s want 123456", o.ID)
	}
	if o.Side != exchange.Buy {
		t.Errorf("Side: got %s want BUY", o.Side)
	}
	if o.Price != 59500.0 {
		t.Errorf("Price: got %g want 59500.0", o.Price)
	}
	if o.Status != exchange.StatusOpen {
		t.Errorf("Status: got %s want OPEN", o.Status)
	}
}

// ─── PlaceOrder ───────────────────────────────────────────────────────────────

func TestPlaceOrder_LimitBuy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/order", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		r.ParseForm() //nolint
		requireSignatureForm(t, r)
		if r.FormValue("timeInForce") != "GTC" {
			t.Errorf("limit order should include timeInForce=GTC, got: %q", r.FormValue("timeInForce"))
		}
		jsonResp(t, w, map[string]any{
			"orderId":      int64(789),
			"symbol":       "BTCUSDT",
			"side":         "BUY",
			"type":         "LIMIT",
			"price":        "59000.0",
			"origQty":      "0.01",
			"executedQty":  "0.0",
			"status":       "NEW",
			"transactTime": int64(1700000001000),
		})
	})

	c, _ := newTestClient(t, mux)
	order, err := c.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Limit,
		Price:  59000.0,
		Qty:    0.01,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if order.ID != "789" {
		t.Errorf("ID: got %s want 789", order.ID)
	}
}

func TestPlaceOrder_MarketSell(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/order", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm() //nolint
		if r.FormValue("timeInForce") != "" {
			t.Errorf("market order should NOT include timeInForce, got: %q", r.FormValue("timeInForce"))
		}
		jsonResp(t, w, map[string]any{
			"orderId": int64(1), "symbol": "BTCUSDT", "side": "SELL",
			"type": "MARKET", "price": "0", "origQty": "0.01",
			"executedQty": "0.01", "status": "FILLED", "transactTime": int64(0),
		})
	})

	c, _ := newTestClient(t, mux)
	_, err := c.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Sell, Type: exchange.Market, Qty: 0.01,
	})
	if err != nil {
		t.Fatalf("PlaceOrder market: %v", err)
	}
}

func TestPlaceOrder_ReduceOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/order", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm() //nolint
		if r.FormValue("reduceOnly") != "true" {
			t.Errorf("reduce-only order should include reduceOnly=true, got: %q", r.FormValue("reduceOnly"))
		}
		jsonResp(t, w, map[string]any{
			"orderId": int64(2), "symbol": "BTCUSDT", "side": "SELL",
			"type": "MARKET", "price": "0", "origQty": "0.01",
			"executedQty": "0.0", "status": "NEW", "transactTime": int64(0),
		})
	})

	c, _ := newTestClient(t, mux)
	_, err := c.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Sell, Type: exchange.Market,
		Qty: 0.01, ReduceOnly: true,
	})
	if err != nil {
		t.Fatalf("PlaceOrder reduce-only: %v", err)
	}
}

func TestPlaceOrder_NoAPIKey_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := binance.NewWithBaseURL(srv.URL) // no key
	_, err := c.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Buy, Type: exchange.Market, Qty: 0.01,
	})
	if err == nil {
		t.Fatal("expected error without API key")
	}
}

// ─── CancelOrder ─────────────────────────────────────────────────────────────

func TestCancelOrder_SendsDelete(t *testing.T) {
	var method string
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/order", func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requireSignature(t, r)
		jsonResp(t, w, map[string]string{"status": "CANCELED"})
	})

	c, _ := newTestClient(t, mux)
	if err := c.CancelOrder(context.Background(), "BTCUSDT", "99999"); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", method)
	}
}

// ─── CancelAllOrders ─────────────────────────────────────────────────────────

func TestCancelAllOrders_SendsDelete(t *testing.T) {
	var method string
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/allOpenOrders", func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requireSignature(t, r)
		jsonResp(t, w, map[string]string{"code": "200"})
	})

	c, _ := newTestClient(t, mux)
	if err := c.CancelAllOrders(context.Background(), "BTCUSDT"); err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", method)
	}
}

// ─── Signing ──────────────────────────────────────────────────────────────────

func TestSigning_SignaturePresent(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v2/positionRisk", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		jsonResp(t, w, []map[string]string{})
	})

	c, _ := newTestClient(t, mux)
	c.Position(context.Background(), "BTCUSDT") //nolint

	if !strings.Contains(gotQuery, "signature=") {
		t.Errorf("signed request should include signature param, got: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "timestamp=") {
		t.Errorf("signed request should include timestamp param, got: %s", gotQuery)
	}
}

func TestSigning_APIKeyHeader(t *testing.T) {
	var gotKey string
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v2/positionRisk", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-MBX-APIKEY")
		jsonResp(t, w, []map[string]string{})
	})

	c, _ := newTestClient(t, mux)
	c.Position(context.Background(), "BTCUSDT") //nolint

	if gotKey != "test-api-key" {
		t.Errorf("expected X-MBX-APIKEY=test-api-key, got %q", gotKey)
	}
}

// ─── HTTP error handling ──────────────────────────────────────────────────────

func TestHTTPError_NonOK_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/ticker/bookTicker", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code":-1003,"msg":"Too many requests"}`)) //nolint
	})
	mux.HandleFunc("/fapi/v1/premiumIndex", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(t, w, map[string]string{})
	})

	c, _ := newTestClient(t, mux)
	_, err := c.MarketSnapshot(context.Background(), "BTCUSDT")
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// requireSignature checks the URL query string for signature (GET/DELETE).
func requireSignature(t *testing.T, r *http.Request) {
	t.Helper()
	if r.URL.Query().Get("signature") == "" {
		t.Errorf("expected signature param in URL query for %s %s", r.Method, r.URL.Path)
	}
}

// requireSignatureForm checks POST form body for signature.
func requireSignatureForm(t *testing.T, r *http.Request) {
	t.Helper()
	if r.FormValue("signature") == "" {
		t.Errorf("expected signature param in form body for %s %s", r.Method, r.URL.Path)
	}
}
