package lplex

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/sixfathoms/lplex/requestrules"
)

// TestBrokerRequestRuleOnOnline verifies the broker wires the request engine:
// when a matching device is discovered, the configured ISO request is sent.
func TestBrokerRequestRuleOnOnline(t *testing.T) {
	six := uint8(6)
	b := NewBroker(BrokerConfig{
		RingSize:          1024,
		MaxBufferDuration: time.Minute,
		Logger:            slog.Default(),
		RequestRules: []requestrules.Rule{{
			Name:        "route",
			Match:       requestrules.Match{Source: &six},
			Via:         requestrules.ViaISORequest,
			Wants:       []requestrules.Want{{PGN: 130065}},
			OnOnline:    true,
			MinInterval: time.Hour,
		}},
	})
	go b.Run(context.Background())
	defer b.CloseRx()

	// Address claim from src 6 -> device discovered -> rule fires.
	injectFrame(b, 60928, 6, []byte{1, 2, 3, 4, 5, 6, 7, 8})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-b.txFrames:
			if f.Header.PGN == 59904 && len(f.Data) >= 3 {
				reqPGN := uint32(f.Data[0]) | uint32(f.Data[1])<<8 | uint32(f.Data[2])<<16
				if reqPGN == 130065 {
					return // success
				}
			}
		case <-deadline:
			t.Fatal("did not observe ISO request for PGN 130065")
		}
	}
}
