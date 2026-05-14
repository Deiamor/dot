package binance

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// WebSocket opcode constants (RFC 6455).
const (
	WSOpcodeText  byte = 0x1
	WSOpcodePing  byte = 0x9
	WSOpcodePong  byte = 0xA
	wsOpcodeClose byte = 0x8
)

const (
	mainnetWSHost = "fstream.binance.com"
	testnetWSHost = "stream.binancefutures.com"
)

// WsSnapshot caches the latest market data from the WebSocket feed.
type WsSnapshot struct {
	Bid         float64
	Ask         float64
	Mark        float64
	FundingRate float64
	UpdatedAt   time.Time
}

// WSFeed subscribes to the Binance combined bookTicker + markPrice@1s stream
// and maintains a thread-safe cache of WsSnapshot per symbol.
type WSFeed struct {
	mu    sync.RWMutex
	cache map[string]*WsSnapshot
	host  string
}

// NewWSFeed starts WebSocket subscriptions for the given symbols.
// Reconnects automatically on disconnect. ctx cancellation shuts down the feed.
func NewWSFeed(ctx context.Context, host string, symbols []string) *WSFeed {
	f := &WSFeed{
		cache: make(map[string]*WsSnapshot),
		host:  host,
	}
	go f.run(ctx, symbols)
	return f
}

// Snapshot returns the latest cached MarketSnapshot for symbol.
// ok=false if no data has been received yet.
func (f *WSFeed) Snapshot(symbol string) (exchange.MarketSnapshot, bool) {
	f.mu.RLock()
	s := f.cache[strings.ToUpper(symbol)]
	f.mu.RUnlock()
	if s == nil {
		return exchange.MarketSnapshot{}, false
	}
	mid := (s.Bid + s.Ask) / 2
	if mid == 0 && s.Mark > 0 {
		mid = s.Mark
	}
	if mid == 0 {
		return exchange.MarketSnapshot{}, false
	}
	return exchange.MarketSnapshot{
		Symbol:      strings.ToUpper(symbol),
		Bid:         s.Bid,
		Ask:         s.Ask,
		Mid:         mid,
		MarkPrice:   s.Mark,
		FundingRate: s.FundingRate,
		Timestamp:   s.UpdatedAt,
	}, true
}

// ─── connection loop ──────────────────────────────────────────────────────────

func (f *WSFeed) run(ctx context.Context, symbols []string) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = f.connect(ctx, symbols)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (f *WSFeed) connect(ctx context.Context, symbols []string) error {
	var parts []string
	for _, sym := range symbols {
		s := strings.ToLower(sym)
		parts = append(parts, s+"@bookTicker", s+"@markPrice@1s")
	}
	path := "/stream?streams=" + strings.Join(parts, "/")

	conn, r, err := wsDial(ctx, f.host, path)
	if err != nil {
		return err
	}
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		conn.SetReadDeadline(time.Now().Add(90 * time.Second)) //nolint:errcheck
		opcode, payload, err := wsReadFrame(r)
		if err != nil {
			return err
		}
		switch opcode {
		case WSOpcodeText:
			f.handleMessage(payload)
		case WSOpcodePing:
			if err := WSSendPong(conn, payload); err != nil {
				return err
			}
		case wsOpcodeClose:
			return fmt.Errorf("server closed")
		}
	}
}

func (f *WSFeed) handleMessage(data []byte) {
	var env struct {
		Stream string          `json:"stream"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil || env.Stream == "" {
		return
	}
	sym := streamSymbol(env.Stream)
	if sym == "" {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.cache[sym]
	if s == nil {
		s = &WsSnapshot{}
		f.cache[sym] = s
	}
	if strings.Contains(env.Stream, "@bookTicker") {
		WSParseBookTicker(env.Data, s) //nolint:errcheck
	} else if strings.Contains(env.Stream, "@markPrice") {
		WSParseMarkPrice(env.Data, s) //nolint:errcheck
	}
}

func streamSymbol(stream string) string {
	idx := strings.IndexByte(stream, '@')
	if idx <= 0 {
		return ""
	}
	return strings.ToUpper(stream[:idx])
}

// ─── TLS WebSocket dialer ─────────────────────────────────────────────────────

func wsDial(ctx context.Context, host, path string) (net.Conn, *bufio.Reader, error) {
	d := tls.Dialer{Config: &tls.Config{ServerName: host}}
	conn, err := d.DialContext(ctx, "tcp", host+":443")
	if err != nil {
		return nil, nil, fmt.Errorf("ws dial %s: %w", host, err)
	}

	key := wsKey()
	hs := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(hs)); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("ws handshake write: %w", err)
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("ws handshake read: %w", err)
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, nil, fmt.Errorf("ws unexpected response: %s", strings.TrimSpace(statusLine))
	}
	// Drain response headers until blank line.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, nil, fmt.Errorf("ws header drain: %w", err)
		}
		if line == "\r\n" {
			break
		}
	}
	return conn, br, nil
}

func wsKey() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	return base64.StdEncoding.EncodeToString(b)
}

// ─── frame I/O ────────────────────────────────────────────────────────────────

// WSReadFrame reads one WebSocket frame from r (server→client, unmasked).
// Exported for testing.
func WSReadFrame(r io.Reader) (opcode byte, payload []byte, err error) {
	return wsReadFrame(r)
}

func wsReadFrame(r io.Reader) (opcode byte, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	opcode = hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	plen := int64(hdr[1] & 0x7F)

	switch plen {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		plen = int64(ext[0])<<8 | int64(ext[1])
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		plen = 0
		for _, b := range ext {
			plen = plen<<8 | int64(b)
		}
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(r, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}
	payload = make([]byte, plen)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i, b := range payload {
			payload[i] = b ^ maskKey[i%4]
		}
	}
	return opcode, payload, nil
}

// WSSendPong sends a masked Pong frame over conn.
// Client→server frames must be masked per RFC 6455.
func WSSendPong(conn net.Conn, payload []byte) error {
	return wsSendMasked(conn, WSOpcodePong, payload)
}

func wsSendMasked(conn net.Conn, opcode byte, payload []byte) error {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	n := len(payload)
	buf := make([]byte, 0, 6+n)
	buf = append(buf, 0x80|opcode) // FIN=1
	switch {
	case n < 126:
		buf = append(buf, 0x80|byte(n))
	case n < 65536:
		buf = append(buf, 0x80|126, byte(n>>8), byte(n))
	default:
		buf = append(buf, 0x80|127)
		for i := 7; i >= 0; i-- {
			buf = append(buf, byte(n>>(8*i)))
		}
	}
	buf = append(buf, mask[:]...)
	for i, b := range payload {
		buf = append(buf, b^mask[i%4])
	}
	_, err := conn.Write(buf)
	return err
}

// ─── JSON parsers ─────────────────────────────────────────────────────────────

// WSParseBookTicker updates snap.Bid, snap.Ask, snap.UpdatedAt from a bookTicker payload.
// All four fields (b/B/a/A) are declared explicitly to prevent Go's case-insensitive
// JSON key matching from overwriting the price fields with the quantity fields.
func WSParseBookTicker(data []byte, snap *WsSnapshot) error {
	var r struct {
		BidPrice string `json:"b"`
		BidQty   string `json:"B"`
		AskPrice string `json:"a"`
		AskQty   string `json:"A"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	snap.Bid = parseFloat(r.BidPrice)
	snap.Ask = parseFloat(r.AskPrice)
	snap.UpdatedAt = time.Now()
	return nil
}

// WSParseMarkPrice updates snap.Mark, snap.FundingRate, snap.UpdatedAt from a markPriceUpdate payload.
func WSParseMarkPrice(data []byte, snap *WsSnapshot) error {
	var r struct {
		MarkPrice   string `json:"p"`
		FundingRate string `json:"r"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	snap.Mark = parseFloat(r.MarkPrice)
	snap.FundingRate = parseFloat(r.FundingRate)
	snap.UpdatedAt = time.Now()
	return nil
}
