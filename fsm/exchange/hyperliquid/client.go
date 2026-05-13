// Package hyperliquid implements the exchange.Exchange interface
// for HyperLiquid's perpetual futures API.
//
// API reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api
package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

const (
	mainnetBase = "https://api.hyperliquid.xyz"
	testnetBase = "https://api.hyperliquid-testnet.xyz"
)

// Client is a HyperLiquid exchange connector.
// Create one with New; it is safe for concurrent use.
type Client struct {
	base       string
	privateKey string // hex-encoded secp256k1 private key (empty = read-only)
	address    string // 0x... wallet address
	http       *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithTestnet routes requests to the HyperLiquid testnet.
func WithTestnet() Option {
	return func(c *Client) { c.base = testnetBase }
}

// WithKey sets the private key for order signing.
// key is a 64-char hex string; address is the 0x wallet address.
func WithKey(key, address string) Option {
	return func(c *Client) {
		c.privateKey = key
		c.address = address
	}
}

// New creates a HyperLiquid client.
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

// ─── Market data ─────────────────────────────────────────────────────────────

func (c *Client) MarketSnapshot(ctx context.Context, symbol string) (exchange.MarketSnapshot, error) {
	type metaResp struct {
		Universe []struct {
			Name string `json:"name"`
		} `json:"universe"`
	}
	type l2Book struct {
		Levels [][]struct {
			Px string `json:"px"`
			Sz string `json:"sz"`
			N  int    `json:"n"`
		} `json:"levels"`
	}
	type allMidsResp map[string]string
	type fundingResp []struct {
		Coin        string `json:"coin"`
		FundingRate string `json:"fundingRate"`
	}

	// Fetch mid prices for all assets.
	var mids allMidsResp
	if err := c.infoPost(ctx, map[string]any{"type": "allMids"}, &mids); err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("allMids: %w", err)
	}

	mid := parseFloat(mids[symbol])

	// Fetch order book for best bid/ask.
	var book l2Book
	if err := c.infoPost(ctx, map[string]any{
		"type": "l2Book",
		"coin": symbol,
	}, &book); err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("l2Book: %w", err)
	}

	var bid, ask float64
	if len(book.Levels) >= 2 {
		if len(book.Levels[0]) > 0 {
			bid = parseFloat(book.Levels[0][0].Px)
		}
		if len(book.Levels[1]) > 0 {
			ask = parseFloat(book.Levels[1][0].Px)
		}
	}

	// Fetch funding rate.
	var funding fundingResp
	if err := c.infoPost(ctx, map[string]any{"type": "fundingRates"}, &funding); err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("fundingRates: %w", err)
	}
	var fundingRate float64
	for _, f := range funding {
		if f.Coin == symbol {
			fundingRate = parseFloat(f.FundingRate)
			break
		}
	}

	return exchange.MarketSnapshot{
		Symbol:      symbol,
		Bid:         bid,
		Ask:         ask,
		Mid:         mid,
		MarkPrice:   mid,
		FundingRate: fundingRate,
		Timestamp:   time.Now(),
	}, nil
}

func (c *Client) OrderBook(ctx context.Context, symbol string, depth int) (exchange.OrderBook, error) {
	type level struct {
		Px string `json:"px"`
		Sz string `json:"sz"`
	}
	type resp struct {
		Levels [][]level `json:"levels"`
	}
	var r resp
	if err := c.infoPost(ctx, map[string]any{
		"type": "l2Book",
		"coin": symbol,
	}, &r); err != nil {
		return exchange.OrderBook{}, err
	}

	ob := exchange.OrderBook{Symbol: symbol}
	if len(r.Levels) >= 2 {
		for i, l := range r.Levels[0] {
			if i >= depth {
				break
			}
			ob.Bids = append(ob.Bids, exchange.PriceLevel{Price: parseFloat(l.Px), Size: parseFloat(l.Sz)})
		}
		for i, l := range r.Levels[1] {
			if i >= depth {
				break
			}
			ob.Asks = append(ob.Asks, exchange.PriceLevel{Price: parseFloat(l.Px), Size: parseFloat(l.Sz)})
		}
	}
	return ob, nil
}

// ─── Account state ────────────────────────────────────────────────────────────

func (c *Client) Position(ctx context.Context, symbol string) (exchange.Position, error) {
	if c.address == "" {
		return exchange.Position{Symbol: symbol}, nil
	}
	type assetPos struct {
		Position struct {
			Coin          string `json:"coin"`
			Szi           string `json:"szi"`
			EntryPx       string `json:"entryPx"`
			UnrealizedPnl string `json:"unrealizedPnl"`
			Leverage      struct {
				Value int `json:"value"`
			} `json:"leverage"`
		} `json:"position"`
	}
	type resp struct {
		AssetPositions []assetPos `json:"assetPositions"`
	}
	var r resp
	if err := c.infoPost(ctx, map[string]any{
		"type": "clearinghouseState",
		"user": c.address,
	}, &r); err != nil {
		return exchange.Position{}, err
	}
	for _, ap := range r.AssetPositions {
		if ap.Position.Coin == symbol {
			return exchange.Position{
				Symbol:        symbol,
				Qty:           parseFloat(ap.Position.Szi),
				AvgEntryPrice: parseFloat(ap.Position.EntryPx),
				UnrealizedPnL: parseFloat(ap.Position.UnrealizedPnl),
				Leverage:      float64(ap.Position.Leverage.Value),
			}, nil
		}
	}
	return exchange.Position{Symbol: symbol}, nil
}

func (c *Client) Balances(ctx context.Context) ([]exchange.Balance, error) {
	if c.address == "" {
		return nil, nil
	}
	type resp struct {
		CrossMarginSummary struct {
			AccountValue string `json:"accountValue"`
		} `json:"crossMarginSummary"`
	}
	var r resp
	if err := c.infoPost(ctx, map[string]any{
		"type": "clearinghouseState",
		"user": c.address,
	}, &r); err != nil {
		return nil, err
	}
	return []exchange.Balance{{
		Asset:     "USDC",
		Available: parseFloat(r.CrossMarginSummary.AccountValue),
		Total:     parseFloat(r.CrossMarginSummary.AccountValue),
	}}, nil
}

func (c *Client) OpenOrders(ctx context.Context, symbol string) ([]exchange.Order, error) {
	if c.address == "" {
		return nil, nil
	}
	type raw struct {
		Coin      string `json:"coin"`
		Oid       int64  `json:"oid"`
		Side      string `json:"side"`
		LimitPx   string `json:"limitPx"`
		Sz        string `json:"sz"`
		OrigSz    string `json:"origSz"`
		Timestamp int64  `json:"timestamp"`
	}
	var orders []raw
	if err := c.infoPost(ctx, map[string]any{
		"type": "openOrders",
		"user": c.address,
	}, &orders); err != nil {
		return nil, err
	}

	var result []exchange.Order
	for _, o := range orders {
		if o.Coin != symbol {
			continue
		}
		side := exchange.Buy
		if o.Side == "A" {
			side = exchange.Sell
		}
		orig := parseFloat(o.OrigSz)
		rem := parseFloat(o.Sz)
		result = append(result, exchange.Order{
			ID:           fmt.Sprintf("%d", o.Oid),
			Symbol:       o.Coin,
			Side:         side,
			Type:         exchange.Limit,
			Price:        parseFloat(o.LimitPx),
			Qty:          orig,
			FilledQty:    orig - rem,
			RemainingQty: rem,
			Status:       exchange.StatusOpen,
			CreatedAt:    time.UnixMilli(o.Timestamp),
		})
	}
	return result, nil
}

// ─── Execution ────────────────────────────────────────────────────────────────

func (c *Client) PlaceOrder(ctx context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	if c.privateKey == "" {
		return exchange.Order{}, fmt.Errorf("no private key configured; read-only client")
	}
	// HyperLiquid order placement uses EIP-712 signing.
	// Full signing implementation omitted for brevity;
	// see docs/hyperliquid-signing.md for the reference implementation.
	return exchange.Order{}, fmt.Errorf("PlaceOrder: signing not yet implemented (see docs/hyperliquid-signing.md)")
}

func (c *Client) CancelOrder(ctx context.Context, symbol, orderID string) error {
	if c.privateKey == "" {
		return fmt.Errorf("no private key configured; read-only client")
	}
	return fmt.Errorf("CancelOrder: signing not yet implemented")
}

func (c *Client) CancelAllOrders(ctx context.Context, symbol string) error {
	if c.privateKey == "" {
		return fmt.Errorf("no private key configured; read-only client")
	}
	return fmt.Errorf("CancelAllOrders: signing not yet implemented")
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (c *Client) infoPost(ctx context.Context, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/info", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}
