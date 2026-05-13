// Package binance implements the exchange.Exchange interface
// for Binance USDT-M Perpetual Futures (fapi).
//
// API reference: https://binance-docs.github.io/apidocs/futures/en/
//
// Authentication: HMAC-SHA256 over the canonical query string.
// All signed endpoints require X-MBX-APIKEY header + "signature" parameter.
package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

const (
	mainnetBase = "https://fapi.binance.com"
	testnetBase = "https://testnet.binancefutures.com"
)

// Client is a Binance USDT-M futures connector.
// Create one with New; it is safe for concurrent use.
type Client struct {
	base      string
	apiKey    string
	secretKey string
	http      *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithTestnet routes requests to the Binance Futures testnet.
func WithTestnet() Option {
	return func(c *Client) { c.base = testnetBase }
}

// WithKey sets the API key and secret for signed requests.
func WithKey(apiKey, secretKey string) Option {
	return func(c *Client) {
		c.apiKey = apiKey
		c.secretKey = secretKey
	}
}

// New creates a Binance futures client.
func New(opts ...Option) *Client {
	c := &Client{
		base: mainnetBase,
		http: &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewWithBaseURL creates a client with a custom base URL.
// Intended for testing against a mock HTTP server.
func NewWithBaseURL(base string, opts ...Option) *Client {
	c := New(opts...)
	c.base = base
	return c
}

// ─── Market data ──────────────────────────────────────────────────────────────

func (c *Client) MarketSnapshot(ctx context.Context, symbol string) (exchange.MarketSnapshot, error) {
	// Best bid/ask from bookTicker.
	type bookTicker struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		AskPrice string `json:"askPrice"`
	}
	var bt bookTicker
	if err := c.publicGet(ctx, "/fapi/v1/ticker/bookTicker", url.Values{"symbol": {symbol}}, &bt); err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("bookTicker: %w", err)
	}

	// Mark price, index price, funding rate from premiumIndex.
	type premiumIndex struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
	}
	var pi premiumIndex
	if err := c.publicGet(ctx, "/fapi/v1/premiumIndex", url.Values{"symbol": {symbol}}, &pi); err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("premiumIndex: %w", err)
	}

	bid := parseFloat(bt.BidPrice)
	ask := parseFloat(bt.AskPrice)
	mark := parseFloat(pi.MarkPrice)
	index := parseFloat(pi.IndexPrice)
	funding := parseFloat(pi.LastFundingRate)

	return exchange.MarketSnapshot{
		Symbol:      symbol,
		Bid:         bid,
		Ask:         ask,
		Mid:         (bid + ask) / 2,
		MarkPrice:   mark,
		IndexPrice:  index,
		FundingRate: funding,
		Timestamp:   time.Now(),
	}, nil
}

func (c *Client) OrderBook(ctx context.Context, symbol string, depth int) (exchange.OrderBook, error) {
	type entry [2]string // [price, qty]
	type resp struct {
		Bids []entry `json:"bids"`
		Asks []entry `json:"asks"`
	}
	var r resp
	params := url.Values{
		"symbol": {symbol},
		"limit":  {strconv.Itoa(clampDepth(depth))},
	}
	if err := c.publicGet(ctx, "/fapi/v1/depth", params, &r); err != nil {
		return exchange.OrderBook{}, err
	}

	ob := exchange.OrderBook{Symbol: symbol}
	for _, e := range r.Bids {
		ob.Bids = append(ob.Bids, exchange.PriceLevel{Price: parseFloat(e[0]), Size: parseFloat(e[1])})
	}
	for _, e := range r.Asks {
		ob.Asks = append(ob.Asks, exchange.PriceLevel{Price: parseFloat(e[0]), Size: parseFloat(e[1])})
	}
	return ob, nil
}

// ─── Account state ────────────────────────────────────────────────────────────

func (c *Client) Position(ctx context.Context, symbol string) (exchange.Position, error) {
	if c.apiKey == "" {
		return exchange.Position{Symbol: symbol}, nil
	}
	type posRisk struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		UnRealizedProfit string `json:"unRealizedProfit"`
		Leverage         string `json:"leverage"`
	}
	var positions []posRisk
	if err := c.signedGet(ctx, "/fapi/v2/positionRisk", url.Values{"symbol": {symbol}}, &positions); err != nil {
		return exchange.Position{}, err
	}
	for _, p := range positions {
		if p.Symbol == symbol {
			return exchange.Position{
				Symbol:        symbol,
				Qty:           parseFloat(p.PositionAmt),
				AvgEntryPrice: parseFloat(p.EntryPrice),
				UnrealizedPnL: parseFloat(p.UnRealizedProfit),
				Leverage:      parseFloat(p.Leverage),
			}, nil
		}
	}
	return exchange.Position{Symbol: symbol}, nil
}

func (c *Client) Balances(ctx context.Context) ([]exchange.Balance, error) {
	if c.apiKey == "" {
		return nil, nil
	}
	type asset struct {
		Asset            string `json:"asset"`
		AvailableBalance string `json:"availableBalance"`
		Balance          string `json:"balance"`
		CrossWalletBalance string `json:"crossWalletBalance"`
	}
	type accountResp struct {
		Assets []asset `json:"assets"`
	}
	var r accountResp
	if err := c.signedGet(ctx, "/fapi/v2/account", url.Values{}, &r); err != nil {
		return nil, err
	}
	var out []exchange.Balance
	for _, a := range r.Assets {
		total := parseFloat(a.Balance)
		avail := parseFloat(a.AvailableBalance)
		if total == 0 && avail == 0 {
			continue
		}
		out = append(out, exchange.Balance{
			Asset:     a.Asset,
			Available: avail,
			Reserved:  total - avail,
			Total:     total,
		})
	}
	return out, nil
}

func (c *Client) OpenOrders(ctx context.Context, symbol string) ([]exchange.Order, error) {
	if c.apiKey == "" {
		return nil, nil
	}
	type raw struct {
		OrderID       int64  `json:"orderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Type          string `json:"type"`
		Price         string `json:"price"`
		OrigQty       string `json:"origQty"`
		ExecutedQty   string `json:"executedQty"`
		Status        string `json:"status"`
		Time          int64  `json:"time"`
	}
	var orders []raw
	if err := c.signedGet(ctx, "/fapi/v1/openOrders", url.Values{"symbol": {symbol}}, &orders); err != nil {
		return nil, err
	}

	out := make([]exchange.Order, 0, len(orders))
	for _, o := range orders {
		orig := parseFloat(o.OrigQty)
		filled := parseFloat(o.ExecutedQty)
		out = append(out, exchange.Order{
			ID:           strconv.FormatInt(o.OrderID, 10),
			Symbol:       o.Symbol,
			Side:         parseSide(o.Side),
			Type:         parseOrderType(o.Type),
			Price:        parseFloat(o.Price),
			Qty:          orig,
			FilledQty:    filled,
			RemainingQty: orig - filled,
			Status:       parseStatus(o.Status),
			CreatedAt:    time.UnixMilli(o.Time),
		})
	}
	return out, nil
}

// ─── Execution ────────────────────────────────────────────────────────────────

func (c *Client) PlaceOrder(ctx context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	if c.apiKey == "" {
		return exchange.Order{}, fmt.Errorf("no API key configured; read-only client")
	}

	params := url.Values{
		"symbol":   {req.Symbol},
		"side":     {string(req.Side)},
		"type":     {binanceOrderType(req.Type)},
		"quantity": {formatFloat(req.Qty)},
	}
	if req.Type == exchange.Limit {
		params.Set("price", formatFloat(req.Price))
		params.Set("timeInForce", "GTC")
	}
	if req.ReduceOnly {
		params.Set("reduceOnly", "true")
	}
	if req.ClientID != "" {
		params.Set("newClientOrderId", req.ClientID)
	}

	type resp struct {
		OrderID     int64  `json:"orderId"`
		Symbol      string `json:"symbol"`
		Side        string `json:"side"`
		Type        string `json:"type"`
		Price       string `json:"price"`
		OrigQty     string `json:"origQty"`
		ExecutedQty string `json:"executedQty"`
		Status      string `json:"status"`
		Time        int64  `json:"transactTime"`
	}
	var r resp
	if err := c.signedPost(ctx, "/fapi/v1/order", params, &r); err != nil {
		return exchange.Order{}, err
	}

	orig := parseFloat(r.OrigQty)
	filled := parseFloat(r.ExecutedQty)
	return exchange.Order{
		ID:           strconv.FormatInt(r.OrderID, 10),
		Symbol:       r.Symbol,
		Side:         parseSide(r.Side),
		Type:         parseOrderType(r.Type),
		Price:        parseFloat(r.Price),
		Qty:          orig,
		FilledQty:    filled,
		RemainingQty: orig - filled,
		Status:       parseStatus(r.Status),
		CreatedAt:    time.UnixMilli(r.Time),
	}, nil
}

func (c *Client) CancelOrder(ctx context.Context, symbol, orderID string) error {
	if c.apiKey == "" {
		return fmt.Errorf("no API key configured; read-only client")
	}
	var result json.RawMessage
	return c.signedDelete(ctx, "/fapi/v1/order", url.Values{
		"symbol":  {symbol},
		"orderId": {orderID},
	}, &result)
}

func (c *Client) CancelAllOrders(ctx context.Context, symbol string) error {
	if c.apiKey == "" {
		return fmt.Errorf("no API key configured; read-only client")
	}
	var result json.RawMessage
	return c.signedDelete(ctx, "/fapi/v1/allOpenOrders", url.Values{
		"symbol": {symbol},
	}, &result)
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (c *Client) publicGet(ctx context.Context, path string, params url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, params, false, out)
}

func (c *Client) signedGet(ctx context.Context, path string, params url.Values, out any) error {
	c.addTimestamp(params)
	c.sign(params)
	return c.do(ctx, http.MethodGet, path, params, true, out)
}

func (c *Client) signedPost(ctx context.Context, path string, params url.Values, out any) error {
	c.addTimestamp(params)
	c.sign(params)
	return c.do(ctx, http.MethodPost, path, params, true, out)
}

func (c *Client) signedDelete(ctx context.Context, path string, params url.Values, out any) error {
	c.addTimestamp(params)
	c.sign(params)
	return c.do(ctx, http.MethodDelete, path, params, true, out)
}

func (c *Client) do(ctx context.Context, method, path string, params url.Values, signed bool, out any) error {
	u := c.base + path

	var body io.Reader
	var queryString string

	switch method {
	case http.MethodGet, http.MethodDelete:
		if len(params) > 0 {
			queryString = params.Encode()
		}
	case http.MethodPost, http.MethodPut:
		if len(params) > 0 {
			body = strings.NewReader(params.Encode())
		}
	}

	if queryString != "" {
		u += "?" + queryString
	}

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if method == http.MethodPost || method == http.MethodPut {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if signed && c.apiKey != "" {
		req.Header.Set("X-MBX-APIKEY", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) addTimestamp(params url.Values) {
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
}

func (c *Client) sign(params url.Values) {
	if c.secretKey == "" {
		return
	}
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(params.Encode()))
	params.Set("signature", hex.EncodeToString(mac.Sum(nil)))
}

// ─── type helpers ─────────────────────────────────────────────────────────────

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func parseSide(s string) exchange.Side {
	if strings.ToUpper(s) == "BUY" {
		return exchange.Buy
	}
	return exchange.Sell
}

func parseOrderType(s string) exchange.OrderType {
	if strings.ToUpper(s) == "LIMIT" {
		return exchange.Limit
	}
	return exchange.Market
}

func parseStatus(s string) exchange.OrderStatus {
	switch strings.ToUpper(s) {
	case "NEW", "PARTIALLY_FILLED":
		return exchange.StatusOpen
	case "FILLED":
		return exchange.StatusFilled
	case "CANCELED", "EXPIRED":
		return exchange.StatusCancelled
	case "REJECTED":
		return exchange.StatusRejected
	default:
		return exchange.StatusOpen
	}
}

func binanceOrderType(t exchange.OrderType) string {
	if t == exchange.Limit {
		return "LIMIT"
	}
	return "MARKET"
}

func clampDepth(d int) int {
	// Binance supports: 5, 10, 20, 50, 100, 500, 1000
	switch {
	case d <= 5:
		return 5
	case d <= 10:
		return 10
	case d <= 20:
		return 20
	case d <= 50:
		return 50
	case d <= 100:
		return 100
	case d <= 500:
		return 500
	default:
		return 1000
	}
}
