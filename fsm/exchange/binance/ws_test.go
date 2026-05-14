package binance_test

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange/binance"
)

// ─── frame helpers ────────────────────────────────────────────────────────────

// makeTextFrame builds an unmasked server→client text frame (RFC 6455).
func makeTextFrame(payload []byte) []byte {
	return makeServerFrame(binance.WSOpcodeText, payload)
}

// makePingFrame builds an unmasked server→client ping frame.
func makePingFrame(payload []byte) []byte {
	return makeServerFrame(binance.WSOpcodePing, payload)
}

// makeServerFrame builds a minimal unmasked WebSocket frame (server→client).
func makeServerFrame(opcode byte, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(0x80 | opcode) // FIN=1, RSV=0
	n := len(payload)
	switch {
	case n < 126:
		buf.WriteByte(byte(n)) // MASK=0
	case n < 65536:
		buf.WriteByte(126)
		buf.WriteByte(byte(n >> 8))
		buf.WriteByte(byte(n))
	default:
		buf.WriteByte(127)
		for i := 7; i >= 0; i-- {
			buf.WriteByte(byte(n >> (8 * i)))
		}
	}
	buf.Write(payload)
	return buf.Bytes()
}

// ─── TestWS_ReadTextFrame ──────────────────────────────────────────────────────

func TestWS_ReadTextFrame(t *testing.T) {
	want := []byte(`{"stream":"btcusdt@bookTicker","data":{"s":"BTCUSDT","b":"50000","a":"50001"}}`)
	frame := makeTextFrame(want)

	opcode, payload, err := binance.WSReadFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("WSReadFrame error: %v", err)
	}
	if opcode != binance.WSOpcodeText {
		t.Errorf("opcode: got 0x%X want 0x%X", opcode, binance.WSOpcodeText)
	}
	if !bytes.Equal(payload, want) {
		t.Errorf("payload mismatch:\n  got  %s\n  want %s", payload, want)
	}
}

// TestWS_ReadTextFrame_ExtendedLen tests payload >125 bytes (2-byte extended length).
func TestWS_ReadTextFrame_ExtendedLen(t *testing.T) {
	want := bytes.Repeat([]byte("x"), 200)
	frame := makeTextFrame(want)

	opcode, payload, err := binance.WSReadFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("WSReadFrame extended-len error: %v", err)
	}
	if opcode != binance.WSOpcodeText {
		t.Errorf("opcode: got 0x%X want 0x%X", opcode, binance.WSOpcodeText)
	}
	if !bytes.Equal(payload, want) {
		t.Errorf("payload length: got %d want %d", len(payload), len(want))
	}
}

// ─── TestWS_ReadPingFrame ─────────────────────────────────────────────────────

func TestWS_ReadPingFrame(t *testing.T) {
	pingPayload := []byte("heartbeat")
	frame := makePingFrame(pingPayload)

	opcode, payload, err := binance.WSReadFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("WSReadFrame ping error: %v", err)
	}
	if opcode != binance.WSOpcodePing {
		t.Errorf("opcode: got 0x%X want 0x%X (Ping)", opcode, binance.WSOpcodePing)
	}
	if !bytes.Equal(payload, pingPayload) {
		t.Errorf("ping payload: got %q want %q", payload, pingPayload)
	}
}

// TestWS_ReadPingFrame_Empty tests a ping with no payload.
func TestWS_ReadPingFrame_Empty(t *testing.T) {
	frame := makePingFrame(nil)

	opcode, payload, err := binance.WSReadFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("WSReadFrame empty ping error: %v", err)
	}
	if opcode != binance.WSOpcodePing {
		t.Errorf("opcode: got 0x%X want 0x%X (Ping)", opcode, binance.WSOpcodePing)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

// ─── TestWS_PongRoundTrip ─────────────────────────────────────────────────────

// TestWS_PongRoundTrip writes a masked Pong over net.Pipe and reads back
// the frame header to confirm the MASK bit and correct opcode.
func TestWS_PongRoundTrip(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	pingData := []byte("pong-me")

	// Write pong from client side in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- binance.WSSendPong(clientConn, pingData)
		clientConn.Close()
	}()

	// Read the raw bytes on the server side.
	var buf [256]byte
	n, err := serverConn.Read(buf[:])
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WSSendPong: %v", err)
	}

	if n < 6 {
		t.Fatalf("too few bytes: %d", n)
	}
	// Byte 0: FIN=1, opcode=0xA (Pong)
	if buf[0] != (0x80 | binance.WSOpcodePong) {
		t.Errorf("byte0: got 0x%02X want 0x%02X", buf[0], 0x80|binance.WSOpcodePong)
	}
	// Byte 1: MASK bit must be set (0x80) for client→server frames.
	if buf[1]&0x80 == 0 {
		t.Error("MASK bit must be set for client→server frames")
	}
	payloadLen := int(buf[1] & 0x7F)
	if payloadLen != len(pingData) {
		t.Errorf("payload length: got %d want %d", payloadLen, len(pingData))
	}

	// Unmask payload (bytes 2–5 = mask key, bytes 6+ = masked payload).
	maskKey := buf[2:6]
	masked := buf[6 : 6+payloadLen]
	got := make([]byte, payloadLen)
	for i := range got {
		got[i] = masked[i] ^ maskKey[i%4]
	}
	if !bytes.Equal(got, pingData) {
		t.Errorf("unmasked payload: got %q want %q", got, pingData)
	}
}

// ─── TestWS_ParseBookTicker ───────────────────────────────────────────────────

func TestWS_ParseBookTicker(t *testing.T) {
	data := []byte(`{"e":"bookTicker","s":"BTCUSDT","b":"50000.50","B":"1.234","a":"50001.75","A":"0.5"}`)

	var snap binance.WsSnapshot
	if err := binance.WSParseBookTicker(data, &snap); err != nil {
		t.Fatalf("WSParseBookTicker: %v", err)
	}

	if snap.Bid != 50000.50 {
		t.Errorf("Bid: got %g want 50000.50", snap.Bid)
	}
	if snap.Ask != 50001.75 {
		t.Errorf("Ask: got %g want 50001.75", snap.Ask)
	}
	// Mark/FundingRate not set by bookTicker.
	if snap.Mark != 0 {
		t.Errorf("Mark should be zero, got %g", snap.Mark)
	}
	if snap.FundingRate != 0 {
		t.Errorf("FundingRate should be zero, got %g", snap.FundingRate)
	}
	// UpdatedAt should be recent.
	if time.Since(snap.UpdatedAt) > 5*time.Second {
		t.Errorf("UpdatedAt too old: %v", snap.UpdatedAt)
	}
}

func TestWS_ParseBookTicker_InvalidJSON(t *testing.T) {
	var snap binance.WsSnapshot
	if err := binance.WSParseBookTicker([]byte(`not-json`), &snap); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ─── TestWS_ParseMarkPrice ────────────────────────────────────────────────────

func TestWS_ParseMarkPrice(t *testing.T) {
	data := []byte(`{"e":"markPriceUpdate","s":"BTCUSDT","p":"50000.12","r":"0.0001","T":1000000}`)

	var snap binance.WsSnapshot
	if err := binance.WSParseMarkPrice(data, &snap); err != nil {
		t.Fatalf("WSParseMarkPrice: %v", err)
	}

	if snap.Mark != 50000.12 {
		t.Errorf("Mark: got %g want 50000.12", snap.Mark)
	}
	if snap.FundingRate != 0.0001 {
		t.Errorf("FundingRate: got %g want 0.0001", snap.FundingRate)
	}
	// Bid/Ask not set by markPrice.
	if snap.Bid != 0 {
		t.Errorf("Bid should be zero, got %g", snap.Bid)
	}
	if snap.Ask != 0 {
		t.Errorf("Ask should be zero, got %g", snap.Ask)
	}
	if time.Since(snap.UpdatedAt) > 5*time.Second {
		t.Errorf("UpdatedAt too old: %v", snap.UpdatedAt)
	}
}

func TestWS_ParseMarkPrice_InvalidJSON(t *testing.T) {
	var snap binance.WsSnapshot
	if err := binance.WSParseMarkPrice([]byte(`{bad}`), &snap); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// TestWS_ParseMarkPrice_NegativeFunding verifies negative funding rates parse correctly.
func TestWS_ParseMarkPrice_NegativeFunding(t *testing.T) {
	data := []byte(`{"e":"markPriceUpdate","s":"ETHUSDT","p":"3000.00","r":"-0.0003","T":1000000}`)

	var snap binance.WsSnapshot
	if err := binance.WSParseMarkPrice(data, &snap); err != nil {
		t.Fatalf("WSParseMarkPrice: %v", err)
	}
	if snap.FundingRate != -0.0003 {
		t.Errorf("FundingRate: got %g want -0.0003", snap.FundingRate)
	}
}

// ─── TestWS_MultipleFrames ────────────────────────────────────────────────────

// TestWS_MultipleFrames verifies sequential frame reads from a single reader.
func TestWS_MultipleFrames(t *testing.T) {
	msg1 := []byte(`{"stream":"btcusdt@bookTicker","data":{}}`)
	msg2 := []byte(`{"stream":"btcusdt@markPrice@1s","data":{}}`)

	var buf bytes.Buffer
	buf.Write(makeTextFrame(msg1))
	buf.Write(makeTextFrame(msg2))

	opcode1, payload1, err := binance.WSReadFrame(&buf)
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if opcode1 != binance.WSOpcodeText {
		t.Errorf("frame1 opcode: 0x%X", opcode1)
	}
	if !bytes.Equal(payload1, msg1) {
		t.Errorf("frame1 payload mismatch")
	}

	opcode2, payload2, err := binance.WSReadFrame(&buf)
	if err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if opcode2 != binance.WSOpcodeText {
		t.Errorf("frame2 opcode: 0x%X", opcode2)
	}
	if !bytes.Equal(payload2, msg2) {
		t.Errorf("frame2 payload mismatch")
	}
}
