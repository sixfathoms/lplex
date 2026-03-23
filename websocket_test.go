package lplex

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/sixfathoms/lplex/sendpolicy"
)

func TestWebSocketEphemeralStream(t *testing.T) {
	b := newConsumerTestBroker()
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, ts.URL+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// Inject a frame
	injectFrame(b, 129025, 1, []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22})
	time.Sleep(100 * time.Millisecond)

	// Read the frame from WebSocket
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "frame" {
		t.Errorf("type = %q, want %q", msg.Type, "frame")
	}

	// The data field should contain the frame JSON
	var frame frameJSON
	if err := json.Unmarshal(msg.Data, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.PGN != 129025 {
		t.Errorf("PGN = %d, want 129025", frame.PGN)
	}
}

func TestWebSocketFilter(t *testing.T) {
	b := newConsumerTestBroker()
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect with PGN filter
	conn, _, err := websocket.Dial(ctx, ts.URL+"/ws?pgn=129025", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// Inject frames with different PGNs
	injectFrame(b, 129026, 1, []byte{1, 0, 0, 0, 0, 0, 0, 0}) // should be filtered
	injectFrame(b, 129025, 1, []byte{2, 0, 0, 0, 0, 0, 0, 0}) // should pass
	time.Sleep(100 * time.Millisecond)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}

	var frame frameJSON
	if err := json.Unmarshal(msg.Data, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.PGN != 129025 {
		t.Errorf("expected filtered PGN 129025, got %d", frame.PGN)
	}
}

func TestWebSocketSendDisabled(t *testing.T) {
	b := newConsumerTestBroker()
	go b.Run(context.Background())
	defer b.CloseRx()
	drainTxFrame(b, time.Second)

	srv := NewServer(b, slog.Default(), sendpolicy.SendPolicy{Enabled: false})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, ts.URL+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// Try to send a frame
	sendMsg, _ := json.Marshal(wsMessage{
		Type: "send",
		Data: json.RawMessage(`{"pgn":59904,"dst":255,"prio":6,"data":"0102030405060708"}`),
	})
	if err := conn.Write(ctx, websocket.MessageText, sendMsg); err != nil {
		t.Fatal(err)
	}

	// Should receive an error message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "error" {
		t.Errorf("type = %q, want %q", msg.Type, "error")
	}
}
