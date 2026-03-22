package lplex

import (
	"log/slog"
	"testing"
)

func TestNewMQTTBridgeDefaults(t *testing.T) {
	b := newConsumerTestBroker()
	bridge := NewMQTTBridge(MQTTBridgeConfig{
		BrokerURL: "tcp://localhost:1883",
	}, b)

	if bridge.cfg.TopicPrefix != "lplex" {
		t.Errorf("TopicPrefix = %q, want %q", bridge.cfg.TopicPrefix, "lplex")
	}
	if bridge.cfg.ClientID != "lplex-server" {
		t.Errorf("ClientID = %q, want %q", bridge.cfg.ClientID, "lplex-server")
	}
}

func TestNewMQTTBridgeCustomConfig(t *testing.T) {
	b := newConsumerTestBroker()
	bridge := NewMQTTBridge(MQTTBridgeConfig{
		BrokerURL:   "tcp://mqtt.example.com:1883",
		TopicPrefix: "boat/nmea",
		ClientID:    "my-boat",
		QoS:         1,
		Username:    "user",
		Password:    "pass",
		Logger:      slog.Default(),
	}, b)

	if bridge.cfg.TopicPrefix != "boat/nmea" {
		t.Errorf("TopicPrefix = %q, want %q", bridge.cfg.TopicPrefix, "boat/nmea")
	}
	if bridge.cfg.ClientID != "my-boat" {
		t.Errorf("ClientID = %q, want %q", bridge.cfg.ClientID, "my-boat")
	}
	if bridge.cfg.QoS != 1 {
		t.Errorf("QoS = %d, want 1", bridge.cfg.QoS)
	}
	if bridge.cfg.Username != "user" {
		t.Errorf("Username = %q, want %q", bridge.cfg.Username, "user")
	}
}
