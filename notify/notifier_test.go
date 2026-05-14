package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// ── mock Notifier ─────────────────────────────────────────────────────────────

type mockNotifier struct {
	calls []string
	err   error
}

func (m *mockNotifier) Send(_ context.Context, msg string) error {
	m.calls = append(m.calls, msg)
	return m.err
}

// ── Webhook ───────────────────────────────────────────────────────────────────

func TestWebhook_Send_PostsJSON(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := NewWebhook(srv.URL)
	if err := wh.Send(context.Background(), "hello world"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.Contains(gotContentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["text"] != "hello world" {
		t.Errorf("body.text = %q, want %q", gotBody["text"], "hello world")
	}
	if gotBody["timestamp"] == "" {
		t.Error("body.timestamp is empty")
	}
	// Verify timestamp parses as RFC3339.
	if _, err := time.Parse(time.RFC3339, gotBody["timestamp"]); err != nil {
		t.Errorf("timestamp parse error: %v", err)
	}
}

// ── Telegram ──────────────────────────────────────────────────────────────────

func TestTelegram_Send_CallsAPI(t *testing.T) {
	const (
		wantToken  = "test-token-123"
		wantChatID = "999888"
		wantText   = "alert message"
	)

	var gotPath string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tg := NewTelegram(wantToken, wantChatID)
	// Override the HTTP client to point at the test server.
	tg.http = &http.Client{
		Transport: &urlRewriteTransport{base: srv.URL},
	}

	if err := tg.Send(context.Background(), wantText); err != nil {
		t.Fatalf("Send: %v", err)
	}

	wantPath := "/bot" + wantToken + "/sendMessage"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if gotBody["chat_id"] != wantChatID {
		t.Errorf("body.chat_id = %q, want %q", gotBody["chat_id"], wantChatID)
	}
	if gotBody["text"] != wantText {
		t.Errorf("body.text = %q, want %q", gotBody["text"], wantText)
	}
	if gotBody["parse_mode"] != "HTML" {
		t.Errorf("body.parse_mode = %q, want HTML", gotBody["parse_mode"])
	}
}

// urlRewriteTransport redirects all requests to a test server base URL,
// preserving path + query but replacing scheme+host.
type urlRewriteTransport struct {
	base string // e.g. "http://127.0.0.1:PORT"
}

func (u *urlRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	// parse host from base
	base := strings.TrimPrefix(u.base, "http://")
	clone.URL.Host = base
	return http.DefaultTransport.RoundTrip(clone)
}

// ── Multi ─────────────────────────────────────────────────────────────────────

func TestMulti_Send_AllReceive(t *testing.T) {
	a := &mockNotifier{}
	b := &mockNotifier{}
	m := NewMulti(a, b)

	if err := m.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(a.calls) != 1 || a.calls[0] != "ping" {
		t.Errorf("notifier a: calls = %v, want [ping]", a.calls)
	}
	if len(b.calls) != 1 || b.calls[0] != "ping" {
		t.Errorf("notifier b: calls = %v, want [ping]", b.calls)
	}
}

func TestMulti_Send_ContinuesOnError(t *testing.T) {
	errNotifier := &mockNotifier{err: context.DeadlineExceeded}
	second := &mockNotifier{}
	m := NewMulti(errNotifier, second)

	err := m.Send(context.Background(), "important")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Second notifier should still have been called.
	if len(second.calls) != 1 {
		t.Errorf("second notifier calls = %d, want 1", len(second.calls))
	}
}

// ── Watcher ───────────────────────────────────────────────────────────────────

func TestWatcher_FillAlert_Sent(t *testing.T) {
	n := &mockNotifier{}
	w := NewWatcher(n, "BTCUSDT")

	ch := make(chan exchange.TradingEvent, 1)
	ch <- exchange.TradingEvent{
		Kind:        exchange.EventKindFill,
		Symbol:      "BTCUSDT",
		Side:        exchange.Buy,
		Price:       50000.0,
		Qty:         0.001,
		Fee:         0.0002,
		RealizedPnL: 12.5,
	}
	close(ch)

	w.Watch(context.Background(), ch)

	if len(n.calls) != 1 {
		t.Fatalf("expected 1 alert, got %d: %v", len(n.calls), n.calls)
	}
	if !strings.Contains(n.calls[0], "Fill") {
		t.Errorf("alert does not mention Fill: %q", n.calls[0])
	}
	if !strings.Contains(n.calls[0], "BTCUSDT") {
		t.Errorf("alert does not mention symbol: %q", n.calls[0])
	}
}

// ── Reconciler ────────────────────────────────────────────────────────────────

func TestReconciler_NoDiscrepancy(t *testing.T) {
	n := &mockNotifier{}
	r := NewReconciler(n, 0.001)

	pos := exchange.Position{Symbol: "BTCUSDT", Qty: 0.01}
	discrepancy, err := r.Check(context.Background(), pos, pos)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if discrepancy != 0 {
		t.Errorf("discrepancy = %v, want 0", discrepancy)
	}
	if len(n.calls) != 0 {
		t.Errorf("expected no alerts, got %d: %v", len(n.calls), n.calls)
	}
}

func TestReconciler_Discrepancy_Alert(t *testing.T) {
	n := &mockNotifier{}
	r := NewReconciler(n, 0.001)

	local := exchange.Position{Symbol: "BTCUSDT", Qty: 0.01}
	remote := exchange.Position{Symbol: "BTCUSDT", Qty: 0.05}

	discrepancy, err := r.Check(context.Background(), local, remote)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	const wantDisc = 0.04
	if abs(discrepancy-wantDisc) > 1e-9 {
		t.Errorf("discrepancy = %v, want %v", discrepancy, wantDisc)
	}
	if len(n.calls) != 1 {
		t.Fatalf("expected 1 alert, got %d: %v", len(n.calls), n.calls)
	}
	if !strings.Contains(n.calls[0], "discrepancy") {
		t.Errorf("alert does not mention discrepancy: %q", n.calls[0])
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
