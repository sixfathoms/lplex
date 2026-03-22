package lplex

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

// wsMessage is the envelope for messages sent and received over WebSocket.
// Incoming messages with type "send" are routed to the CAN bus; all other
// messages are ignored. Outgoing messages carry CAN frames and device events.
type wsMessage struct {
	Type string          `json:"type"`           // "frame", "device", "device_removed", "send"
	Data json.RawMessage `json:"data,omitempty"` // type-specific payload
}

// HandleWebSocket upgrades an HTTP connection to a WebSocket and provides
// bidirectional communication: CAN frames flow to the client (filtered like
// /events), and the client can send CAN frames (like /send) on the same
// connection.
//
// Query params for filtering are the same as /events: pgn, exclude_pgn,
// manufacturer, instance, name, exclude_name.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	filter, err := ParseFilterParams(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // allow any origin (CORS handled at Server level)
	})
	if err != nil {
		s.logger.Error("websocket accept failed", "error", err)
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	sub, cleanup := s.broker.Subscribe(filter)
	defer cleanup()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Read loop: handle incoming messages (send commands)
	go s.wsReadLoop(ctx, cancel, conn)

	// Write loop: fan out CAN frames to the client
	s.wsWriteLoop(ctx, conn, sub)
}

func (s *Server) wsWriteLoop(ctx context.Context, conn *websocket.Conn, sub *subscriber) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-sub.ch:
			if !ok {
				return
			}
			msg := wsMessage{Type: "frame", Data: data}
			b, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	}
}

func (s *Server) wsReadLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return // client disconnected or error
		}

		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.logger.Debug("websocket: invalid message", "error", err)
			continue
		}

		switch msg.Type {
		case "send":
			s.wsHandleSend(ctx, conn, msg.Data)
		default:
			// Ignore unknown message types for forward compatibility
		}
	}
}

func (s *Server) wsHandleSend(ctx context.Context, conn *websocket.Conn, data json.RawMessage) {
	var req struct {
		PGN  uint32 `json:"pgn"`
		Src  uint8  `json:"src"`
		Dst  uint8  `json:"dst"`
		Prio uint8  `json:"prio"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		s.wsError(ctx, conn, "invalid send payload")
		return
	}

	if !s.sendPolicy.Enabled {
		s.wsError(ctx, conn, "send is disabled")
		return
	}

	if len(s.sendPolicy.Rules) > 0 {
		var dstNAME uint64
		var nameKnown bool
		if req.Dst != 0xFF {
			if dev := s.broker.devices.Get(req.Dst); dev != nil {
				dstNAME = dev.NAME
				nameKnown = true
			}
		}
		allowed := false
		for _, rule := range s.sendPolicy.Rules {
			if rule.Matches(req.PGN, dstNAME, nameKnown) {
				allowed = rule.Allow
				break
			}
		}
		if !allowed {
			s.wsError(ctx, conn, "send denied by policy")
			return
		}
	}

	frameData, err := hex.DecodeString(req.Data)
	if err != nil {
		s.wsError(ctx, conn, "invalid hex data")
		return
	}

	src := req.Src
	if s.broker.virtualDevices != nil {
		if claimed, ok := s.broker.virtualDevices.ClaimedSource(); ok {
			src = claimed
		}
	}

	header := CANHeader{
		Priority:    req.Prio,
		PGN:         req.PGN,
		Source:      src,
		Destination: req.Dst,
	}

	if !s.broker.QueueTx(TxRequest{Header: header, Data: frameData}) {
		s.wsError(ctx, conn, "tx queue full")
		return
	}
}

func (s *Server) wsError(ctx context.Context, conn *websocket.Conn, msg string) {
	resp, _ := json.Marshal(wsMessage{
		Type: "error",
		Data: json.RawMessage(`"` + msg + `"`),
	})
	_ = conn.Write(ctx, websocket.MessageText, resp)
}
