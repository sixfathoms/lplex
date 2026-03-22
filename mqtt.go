package lplex

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTBridgeConfig configures the MQTT publisher bridge.
type MQTTBridgeConfig struct {
	// BrokerURL is the MQTT broker address (e.g. "tcp://localhost:1883").
	BrokerURL string

	// TopicPrefix is prepended to all published topics (default "lplex").
	// Frames are published to {prefix}/frames/{pgn} or {prefix}/frames/all.
	TopicPrefix string

	// ClientID identifies this client to the MQTT broker (default "lplex-server").
	ClientID string

	// QoS is the MQTT quality of service level (0, 1, or 2; default 0).
	QoS byte

	// Username and Password for MQTT broker authentication (optional).
	Username string
	Password string

	// Filter restricts which CAN frames are published.
	Filter *EventFilter

	// Logger for diagnostic output.
	Logger *slog.Logger
}

// MQTTBridge subscribes to the broker's frame stream and publishes each
// frame to an MQTT broker. Frames are published to per-PGN topics
// ({prefix}/frames/{pgn}) with the pre-serialized JSON payload.
type MQTTBridge struct {
	cfg    MQTTBridgeConfig
	broker *Broker
	client mqtt.Client
	logger *slog.Logger
}

// NewMQTTBridge creates a new MQTT bridge. Call Run to start publishing.
func NewMQTTBridge(cfg MQTTBridgeConfig, broker *Broker) *MQTTBridge {
	if cfg.TopicPrefix == "" {
		cfg.TopicPrefix = "lplex"
	}
	if cfg.ClientID == "" {
		cfg.ClientID = "lplex-server"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &MQTTBridge{
		cfg:    cfg,
		broker: broker,
		logger: cfg.Logger.With("component", "mqtt"),
	}
}

// Run connects to the MQTT broker, subscribes to the lplex broker's frame
// stream, and publishes frames until ctx is cancelled. Reconnection is
// handled automatically by the paho MQTT client.
func (m *MQTTBridge) Run(ctx context.Context) error {
	opts := mqtt.NewClientOptions().
		AddBroker(m.cfg.BrokerURL).
		SetClientID(m.cfg.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			m.logger.Warn("MQTT connection lost", "error", err)
		}).
		SetOnConnectHandler(func(_ mqtt.Client) {
			m.logger.Info("MQTT connected", "broker", m.cfg.BrokerURL)
		})

	if m.cfg.Username != "" {
		opts.SetUsername(m.cfg.Username)
		opts.SetPassword(m.cfg.Password)
	}

	m.client = mqtt.NewClient(opts)

	token := m.client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("MQTT connect: %w", err)
	}

	defer m.client.Disconnect(1000)

	m.logger.Info("MQTT bridge started",
		"broker", m.cfg.BrokerURL,
		"topic_prefix", m.cfg.TopicPrefix,
		"qos", m.cfg.QoS,
	)

	sub, cleanup := m.broker.Subscribe(m.cfg.Filter)
	defer cleanup()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data, ok := <-sub.ch:
			if !ok {
				return nil
			}
			m.publish(data)
		}
	}
}

func (m *MQTTBridge) publish(data []byte) {
	// Publish to the catch-all topic.
	topic := m.cfg.TopicPrefix + "/frames"
	token := m.client.Publish(topic, m.cfg.QoS, false, data)
	token.Wait()
	if err := token.Error(); err != nil {
		m.logger.Warn("MQTT publish failed", "topic", topic, "error", err)
	}
}
