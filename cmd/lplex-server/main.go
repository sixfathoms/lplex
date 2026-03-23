package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/journal"
	"github.com/sixfathoms/lplex/keeper"
	"github.com/sixfathoms/lplex/sendpolicy"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	iface := flag.String("interface", "can0", "SocketCAN interface name (for single-bus; use -interfaces for multi-bus)")
	ifacesStr := flag.String("interfaces", "", "Comma-separated SocketCAN interface names for multi-bus (e.g. can0,can1)")
	port := flag.Int("port", 8089, "HTTP listen port")
	maxBufDur := flag.String("max-buffer-duration", "PT5M", "Max client buffer duration (ISO 8601, e.g. PT5M)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	journalDir := flag.String("journal-dir", "", "Directory for journal files (empty = disabled)")
	journalPrefix := flag.String("journal-prefix", "nmea2k", "Journal file name prefix")
	journalBlockSize := flag.Int("journal-block-size", 262144, "Journal block size (power of 2, min 4096)")
	journalRotateDur := flag.String("journal-rotate-duration", "PT1H", "Rotate journal after duration (ISO 8601, e.g. PT1H)")
	journalRotateSize := flag.Int64("journal-rotate-size", 0, "Rotate journal after bytes (0 = disabled)")
	journalCompression := flag.String("journal-compression", "zstd", "Journal compression: none, zstd, zstd-dict")
	retentionMaxAge := flag.String("journal-retention-max-age", "", "Delete journal files older than this (ISO 8601, e.g. P30D)")
	retentionMinKeep := flag.String("journal-retention-min-keep", "", "Never delete files younger than this (ISO 8601, e.g. PT24H), unless max-size exceeded")
	retentionMaxSize := flag.Int64("journal-retention-max-size", 0, "Hard size cap in bytes; delete oldest files when exceeded")
	retentionSoftPct := flag.Int("journal-retention-soft-pct", 80, "Proactive archive threshold as % of max-size (1-99)")
	retentionOverflowPolicy := flag.String("journal-retention-overflow-policy", "delete-unarchived", "Overflow policy: delete-unarchived or pause-recording")
	archiveCommand := flag.String("journal-archive-command", "", "Path to archive script")
	archiveTriggerStr := flag.String("journal-archive-trigger", "", "Archive trigger: on-rotate or before-expire")
	busSilenceThreshold := flag.String("bus-silence-threshold", "", "Alert on CAN bus silence after this duration (ISO 8601, e.g. PT30S)")
	replTarget := flag.String("replication-target", "", "Cloud replication gRPC address (host:port)")
	replInstanceID := flag.String("replication-instance-id", "", "Instance ID for cloud replication")
	replTLSCert := flag.String("replication-tls-cert", "", "Client certificate for replication mTLS")
	replTLSKey := flag.String("replication-tls-key", "", "Client private key for replication mTLS")
	replTLSCA := flag.String("replication-tls-ca", "", "CA certificate for replication server verification")
	replMaxLiveLag := flag.Int("replication-max-live-lag", int(lplex.DefaultMaxLiveLag), "Max frames live stream can lag before switching to backfill")
	replLagCheckInterval := flag.Int("replication-lag-check-interval", lplex.DefaultLagCheckInterval, "Check live lag every N frames sent")
	replMinLagReconnect := flag.String("replication-min-lag-reconnect-interval", "30s", "Min interval between lag-triggered reconnects (e.g. 30s)")
	deviceIdleTimeout := flag.String("device-idle-timeout", "5m", "Remove devices not seen for this duration (0 = disabled)")
	sendEnabled := flag.Bool("send-enabled", false, "Enable the /send and /query HTTP endpoints (default: disabled)")
	sendRulesStr := flag.String("send-rules", "", "Semicolon-separated send rules (e.g. 'pgn:59904; !pgn:65280-65535')")
	virtualDeviceEnabled := flag.Bool("virtual-device", false, "Enable a virtual NMEA 2000 device for address claiming")
	virtualDeviceName := flag.String("virtual-device-name", "", "64-bit hex ISO NAME for the virtual device (required when -virtual-device is set)")
	virtualDeviceModelID := flag.String("virtual-device-model-id", "lplex-server", "Product info model ID for the virtual device")
	claimHeartbeatStr := flag.String("virtual-device-claim-heartbeat", "60s", "Interval for re-broadcasting address claims (PGN 60928)")
	productInfoHeartbeatStr := flag.String("virtual-device-product-info-heartbeat", "5m", "Interval for re-broadcasting product info (PGN 126996)")
	ringSize := flag.Int("ring-size", 65536, "Ring buffer size in entries (must be power of 2)")
	loopback := flag.Bool("loopback", false, "Use in-memory loopback buses instead of SocketCAN (for development on macOS)")
	busSilenceTimeout := flag.String("bus-silence-timeout", "", "Alert when no CAN frames received for this duration (ISO 8601, e.g. PT30S)")
	configFile := flag.String("config", "", "Path to HOCON config file (default: ./lplex-server.conf, /etc/lplex/lplex-server.conf)")
	validateConfig := flag.Bool("validate-config", false, "Parse and validate config, then exit")
	otelEndpoint := flag.String("otel-endpoint", "", "OTLP gRPC collector endpoint for distributed tracing (e.g. localhost:4317)")
	otelSampleRatio := flag.Float64("otel-sample-ratio", 1.0, "Trace sampling ratio (0.0-1.0, default: 1.0 = sample all)")
	alertWebhookURL := flag.String("alert-webhook-url", "", "HTTP POST endpoint for alert notifications (empty = disabled)")
	alertDedupWindow := flag.String("alert-dedup-window", "5m", "Suppress duplicate alerts within this window")
	mqttBrokerURL := flag.String("mqtt-broker", "", "MQTT broker URL for bridge publishing (e.g. tcp://localhost:1883)")
	mqttTopicPrefix := flag.String("mqtt-topic-prefix", "lplex", "MQTT topic prefix for published frames")
	mqttClientID := flag.String("mqtt-client-id", "lplex-server", "MQTT client ID")
	mqttQoS := flag.Int("mqtt-qos", 0, "MQTT quality of service (0, 1, or 2)")
	mqttUsername := flag.String("mqtt-username", "", "MQTT broker username")
	mqttPassword := flag.String("mqtt-password", "", "MQTT broker password")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lplex-server %s (commit %s, built %s)\n", version, commit, date)
		os.Exit(0)
	}

	// Load HOCON config file (CLI flags take precedence).
	cfgPath, err := findConfigFile(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if cfgPath != "" {
		if err := applyConfig(cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// Validate-config mode: parse everything, report errors, then exit.
	if *validateConfig {
		exitCode := runValidateConfig(cfgPath,
			*maxBufDur, *deviceIdleTimeout, *journalBlockSize, *ringSize,
			*journalRotateDur, *journalCompression,
			*retentionMaxAge, *retentionMinKeep, *retentionMaxSize,
			*retentionSoftPct, *retentionOverflowPolicy,
			*archiveCommand, *archiveTriggerStr,
			*busSilenceTimeout, *busSilenceThreshold,
			*alertDedupWindow,
			*claimHeartbeatStr, *productInfoHeartbeatStr,
			*sendEnabled, *sendRulesStr,
			*virtualDeviceEnabled, *virtualDeviceName,
			*replMinLagReconnect,
		)
		os.Exit(exitCode)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if cfgPath != "" {
		logger.Info("loaded config", "path", cfgPath)
	}

	// Initialize distributed tracing.
	tracingShutdown, err := lplex.InitTracing(context.Background(), lplex.TracingConfig{
		Enabled:        *otelEndpoint != "",
		Endpoint:       *otelEndpoint,
		ServiceName:    "lplex-server",
		ServiceVersion: version,
		SampleRatio:    *otelSampleRatio,
	})
	if err != nil {
		logger.Error("failed to initialize tracing", "error", err)
		os.Exit(1)
	}
	defer func() { _ = tracingShutdown(context.Background()) }()
	if *otelEndpoint != "" {
		logger.Info("tracing enabled", "endpoint", *otelEndpoint, "sample_ratio", *otelSampleRatio)
	}

	bufDuration, err := lplex.ParseISO8601Duration(*maxBufDur)
	if err != nil {
		logger.Error("invalid max-buffer-duration", "value", *maxBufDur, "error", err)
		os.Exit(1)
	}

	devIdleTimeout, err := time.ParseDuration(*deviceIdleTimeout)
	if err != nil {
		logger.Error("invalid device-idle-timeout", "value", *deviceIdleTimeout, "error", err)
		os.Exit(1)
	}
	// Map 0 → -1 (disabled sentinel for BrokerConfig).
	if devIdleTimeout == 0 {
		devIdleTimeout = -1
	}

	var virtualDevices []lplex.VirtualDeviceConfig
	if *virtualDeviceEnabled {
		if *virtualDeviceName == "" {
			logger.Error("-virtual-device-name is required when -virtual-device is set")
			os.Exit(1)
		}
		name, err := strconv.ParseUint(*virtualDeviceName, 16, 64)
		if err != nil {
			logger.Error("invalid virtual-device-name: must be 64-bit hex", "value", *virtualDeviceName, "error", err)
			os.Exit(1)
		}
		hostname, _ := os.Hostname()
		virtualDevices = append(virtualDevices, lplex.VirtualDeviceConfig{
			NAME: name,
			ProductInfo: lplex.VirtualProductInfo{
				ModelID:         *virtualDeviceModelID,
				SoftwareVersion: version,
				ModelSerial:     hostname,
			},
		})
		logger.Info("virtual device configured", "name", *virtualDeviceName, "model_id", *virtualDeviceModelID)
	}

	claimHeartbeat, err := time.ParseDuration(*claimHeartbeatStr)
	if err != nil {
		logger.Error("invalid virtual-device-claim-heartbeat", "value", *claimHeartbeatStr, "error", err)
		os.Exit(1)
	}
	productInfoHeartbeat, err := time.ParseDuration(*productInfoHeartbeatStr)
	if err != nil {
		logger.Error("invalid virtual-device-product-info-heartbeat", "value", *productInfoHeartbeatStr, "error", err)
		os.Exit(1)
	}

	broker := lplex.NewBroker(lplex.BrokerConfig{
		RingSize:             *ringSize,
		MaxBufferDuration:    bufDuration,
		JournalDir:           *journalDir,
		Logger:               logger,
		DeviceIdleTimeout:    devIdleTimeout,
		VirtualDevices:       virtualDevices,
		ClaimHeartbeat:       claimHeartbeat,
		ProductInfoHeartbeat: productInfoHeartbeat,
	})

	sendPolicy, err := parseSendPolicy(*sendEnabled, *sendRulesStr)
	if err != nil {
		logger.Error("invalid send policy", "error", err)
		os.Exit(1)
	}
	srv := lplex.NewServer(broker, logger, sendPolicy)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	// Set up journal writer if configured
	var journalCh chan lplex.RxFrame
	var jw *lplex.JournalWriter
	if *journalDir != "" {
		var rotateDur time.Duration
		if *journalRotateDur != "" {
			rotateDur, err = lplex.ParseISO8601Duration(*journalRotateDur)
			if err != nil {
				logger.Error("invalid journal-rotate-duration", "value", *journalRotateDur, "error", err)
				os.Exit(1)
			}
		}

		var compression journal.CompressionType
		switch *journalCompression {
		case "none":
			compression = journal.CompressionNone
		case "zstd":
			compression = journal.CompressionZstd
		case "zstd-dict":
			compression = journal.CompressionZstdDict
		default:
			logger.Error("invalid journal-compression", "value", *journalCompression)
			os.Exit(1)
		}

		// Set up journal keeper (retention + archive) if configured.
		var jk *keeper.JournalKeeper
		keeperCfg, err := buildKeeperConfig(
			*journalDir, *replInstanceID,
			*retentionMaxAge, *retentionMinKeep, *retentionMaxSize,
			*retentionSoftPct, *retentionOverflowPolicy,
			*archiveCommand, *archiveTriggerStr, logger,
		)
		if err != nil {
			logger.Error("invalid retention/archive config", "error", err)
			os.Exit(1)
		}
		if keeperCfg != nil {
			jk = keeper.NewJournalKeeper(*keeperCfg)
		}

		journalCh = make(chan lplex.RxFrame, 16384)
		broker.SetJournal(journalCh)

		jwCfg := lplex.JournalConfig{
			Dir:            *journalDir,
			Prefix:         *journalPrefix,
			BlockSize:      *journalBlockSize,
			Compression:    compression,
			RotateDuration: rotateDur,
			RotateSize:     *journalRotateSize,
			Logger:         logger,
		}
		if jk != nil {
			jwCfg.OnRotate = func(rf keeper.RotatedFile) {
				rf.InstanceID = *replInstanceID
				jk.Send(rf)
			}
		}

		jw, err = lplex.NewJournalWriter(jwCfg, broker.Devices(), journalCh)
		if err != nil {
			logger.Error("journal writer init failed", "error", err)
			os.Exit(1)
		}

		if jk != nil {
			jk.SetOnPauseChange(func(_ keeper.KeeperDir, paused bool) {
				jw.SetPaused(paused)
			})
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := jw.Run(ctx); err != nil {
				if ctx.Err() == nil {
					logger.Error("journal writer failed", "error", err)
				}
			}
		}()

		if jk != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				jk.Run(ctx)
			}()
			logger.Info("journal keeper enabled",
				"max_age", *retentionMaxAge,
				"min_keep", *retentionMinKeep,
				"max_size", *retentionMaxSize,
				"soft_pct", *retentionSoftPct,
				"overflow_policy", *retentionOverflowPolicy,
				"archive_command", *archiveCommand,
				"archive_trigger", *archiveTriggerStr,
			)
		}

		logger.Info("journal enabled", "dir", *journalDir, "block_size", *journalBlockSize, "compression", *journalCompression)
	}

	go broker.Run(ctx)

	// Resolve interface list: -interfaces takes priority over -interface.
	var ifaceNames []string
	if *ifacesStr != "" {
		for _, s := range strings.Split(*ifacesStr, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				ifaceNames = append(ifaceNames, s)
			}
		}
	} else {
		ifaceNames = []string{*iface}
	}

	// Build CANBus instances: SocketCAN (default) or loopback (for macOS dev).
	buses := make([]lplex.CANBus, len(ifaceNames))
	for i, name := range ifaceNames {
		if *loopback {
			buses[i] = lplex.NewLoopbackBus(name, 256, logger)
		} else {
			buses[i] = lplex.NewSocketCANBus(name, logger)
		}
	}
	if *loopback {
		logger.Info("using loopback buses (no SocketCAN)", "interfaces", ifaceNames)
	}

	// Start CAN readers (one per bus, all feed into the same broker).
	for _, bus := range buses {
		bus := bus // capture for goroutine
		go func() {
			if err := bus.ReadFrames(ctx, broker.RxFrames()); err != nil {
				if ctx.Err() == nil {
					logger.Error("CAN reader failed", "error", err, "interface", bus.Name())
					cancel()
				}
			}
		}()
	}

	// Start TX dispatcher: routes TxRequests from the broker to the correct
	// CAN bus based on the Bus field. Empty Bus = first bus.
	if len(buses) == 1 {
		// Single bus: direct writer, no routing needed.
		go func() {
			if err := buses[0].WriteFrames(ctx, broker.TxFrames()); err != nil {
				if ctx.Err() == nil {
					logger.Error("CAN writer failed", "error", err, "interface", buses[0].Name())
					cancel()
				}
			}
		}()
	} else {
		// Multi-bus: per-bus TX channels with a dispatcher goroutine.
		txChans := make(map[string]chan lplex.TxRequest, len(buses))
		for _, bus := range buses {
			ch := make(chan lplex.TxRequest, 64)
			txChans[bus.Name()] = ch
			bus := bus
			go func() {
				if err := bus.WriteFrames(ctx, ch); err != nil {
					if ctx.Err() == nil {
						logger.Error("CAN writer failed", "error", err, "interface", bus.Name())
					}
				}
			}()
		}
		defaultBus := buses[0].Name()
		go func() {
			for req := range broker.TxFrames() {
				bus := req.Bus
				if bus == "" {
					bus = defaultBus
				}
				if ch, ok := txChans[bus]; ok {
					select {
					case ch <- req:
					default:
					}
				} else {
					logger.Warn("TX for unknown bus, routing to default", "bus", bus, "default", defaultBus)
					if ch, ok := txChans[defaultBus]; ok {
						select {
						case ch <- req:
						default:
						}
					}
				}
			}
			// Close all per-bus channels when the broker's TX channel closes.
			for _, ch := range txChans {
				close(ch)
			}
		}()
	}

	// Set up alert manager if webhook is configured.
	dedupWindow, err := time.ParseDuration(*alertDedupWindow)
	if err != nil {
		logger.Error("invalid alert-dedup-window", "value", *alertDedupWindow, "error", err)
		os.Exit(1)
	}
	alertMgr := lplex.NewAlertManager(lplex.AlertManagerConfig{
		WebhookURL:  *alertWebhookURL,
		DedupWindow: dedupWindow,
		InstanceID:  *replInstanceID,
		Logger:      logger,
	})
	if alertMgr != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alertMgr.Run(ctx)
		}()
		logger.Info("alerting enabled", "webhook_url", *alertWebhookURL, "dedup_window", dedupWindow)
	}
	broker.SetAlerts(alertMgr)

	// Start bus silence monitor if configured
	if *busSilenceTimeout != "" {
		silenceTimeout, err := lplex.ParseISO8601Duration(*busSilenceTimeout)
		if err != nil {
			logger.Error("invalid bus-silence-timeout", "value", *busSilenceTimeout, "error", err)
			os.Exit(1)
		}
		monitor := lplex.NewBusSilenceMonitor(silenceTimeout, broker, logger, alertMgr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			monitor.Run(ctx)
		}()
		logger.Info("bus silence monitor enabled", "timeout", silenceTimeout)
	}

	// Start MQTT bridge if configured
	if *mqttBrokerURL != "" {
		bridge := lplex.NewMQTTBridge(lplex.MQTTBridgeConfig{
			BrokerURL:   *mqttBrokerURL,
			TopicPrefix: *mqttTopicPrefix,
			ClientID:    *mqttClientID,
			QoS:         byte(*mqttQoS),
			Username:    *mqttUsername,
			Password:    *mqttPassword,
			Logger:      logger,
		}, broker)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := bridge.Run(ctx); err != nil && ctx.Err() == nil {
				logger.Error("MQTT bridge failed", "error", err)
			}
		}()
	}

	// Start replication client if configured
	var replClient *lplex.ReplicationClient
	if *replTarget != "" && *replInstanceID != "" {
		minLagReconnect, err := time.ParseDuration(*replMinLagReconnect)
		if err != nil {
			logger.Error("invalid replication-min-lag-reconnect-interval", "value", *replMinLagReconnect, "error", err)
			os.Exit(1)
		}

		replClient = lplex.NewReplicationClient(lplex.ReplicationClientConfig{
			Target:                  *replTarget,
			InstanceID:              *replInstanceID,
			CertFile:                *replTLSCert,
			KeyFile:                 *replTLSKey,
			CAFile:                  *replTLSCA,
			Logger:                  logger,
			MaxLiveLag:              uint64(*replMaxLiveLag),
			LagCheckInterval:        *replLagCheckInterval,
			MinLagReconnectInterval: minLagReconnect,
		}, broker)
		replClient.SetAlerts(alertMgr)

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := replClient.Run(ctx); err != nil {
				if ctx.Err() == nil {
					logger.Error("replication client failed", "error", err)
				}
			}
		}()
		logger.Info("replication enabled", "target", *replTarget, "instance_id", *replInstanceID)

		// Add replication status endpoint
		srv.HandleFunc("GET /replication/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			status := replClient.Status()
			if err := json.NewEncoder(w).Encode(status); err != nil {
				logger.Error("failed to encode replication status", "error", err)
			}
		})
	}

	// Register metrics endpoint.
	var replStatusFn func() *lplex.ReplicationStatus
	if replClient != nil {
		replStatusFn = func() *lplex.ReplicationStatus {
			s := replClient.Status()
			return &s
		}
	}
	var journalStatsFn func() *lplex.JournalWriterStats
	if jw != nil {
		journalStatsFn = func() *lplex.JournalWriterStats {
			s := jw.Stats()
			return &s
		}
	}
	srv.HandleFunc("GET /metrics", lplex.MetricsHandler(broker, replStatusFn, journalStatsFn))

	// Register health check endpoint.
	healthCfg := lplex.HealthConfig{
		Broker:     broker,
		ReplStatus: replStatusFn,
	}
	if *busSilenceThreshold != "" {
		silenceDur, err := lplex.ParseISO8601Duration(*busSilenceThreshold)
		if err != nil {
			logger.Error("invalid bus-silence-threshold", "value", *busSilenceThreshold, "error", err)
			os.Exit(1)
		}
		healthCfg.BusSilenceThreshold = silenceDur
	}
	srv.HandleFunc("GET /healthz", lplex.HealthHandler(healthCfg))
	srv.HandleFunc("GET /livez", lplex.LivenessHandler())
	srv.HandleFunc("GET /readyz", lplex.ReadinessHandler(healthCfg))

	addr := fmt.Sprintf(":%d", *port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv,
	}

	go func() {
		logger.Info("HTTP server starting", "addr", addr, "interfaces", ifaceNames, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", "error", err)
			cancel()
		}
	}()

	hostname, _ := os.Hostname()
	mdns, err := zeroconf.Register(hostname, "_lplex._tcp", "local.", *port, nil, nil)
	if err != nil {
		logger.Error("mDNS registration failed", "error", err)
	} else {
		defer mdns.Shutdown()
		logger.Info("mDNS registered", "service", "_lplex._tcp", "port", *port)
	}

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown error", "error", err)
	}

	broker.CloseRx()
	if journalCh != nil {
		close(journalCh)
	}
	wg.Wait()

	logger.Info("lplex-server stopped")
}

// buildKeeperConfig parses retention/archive flags and returns a KeeperConfig,
// or nil if no retention or archive is configured.
func buildKeeperConfig(
	journalDir, instanceID string,
	maxAgeStr, minKeepStr string,
	maxSize int64,
	softPct int, overflowPolicyStr string,
	archiveCmd, archiveTriggerStr string,
	logger *slog.Logger,
) (*keeper.KeeperConfig, error) {
	var maxAge, minKeep time.Duration
	var err error

	if maxAgeStr != "" {
		maxAge, err = lplex.ParseISO8601Duration(maxAgeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid journal-retention-max-age %q: %w", maxAgeStr, err)
		}
	}
	if minKeepStr != "" {
		minKeep, err = lplex.ParseISO8601Duration(minKeepStr)
		if err != nil {
			return nil, fmt.Errorf("invalid journal-retention-min-keep %q: %w", minKeepStr, err)
		}
	}

	archiveTrigger, err := keeper.ParseArchiveTrigger(archiveTriggerStr)
	if err != nil {
		return nil, err
	}

	overflowPolicy, err := keeper.ParseOverflowPolicy(overflowPolicyStr)
	if err != nil {
		return nil, err
	}

	// Nothing to do if no retention and no archive.
	if maxAge == 0 && maxSize == 0 && archiveCmd == "" {
		return nil, nil
	}

	return &keeper.KeeperConfig{
		Dirs:           []keeper.KeeperDir{{Dir: journalDir, InstanceID: instanceID}},
		MaxAge:         maxAge,
		MinKeep:        minKeep,
		MaxSize:        maxSize,
		SoftPct:        softPct,
		OverflowPolicy: overflowPolicy,
		ArchiveCommand: archiveCmd,
		ArchiveTrigger: archiveTrigger,
		Logger:         logger,
	}, nil
}

// parseSendPolicy builds a SendPolicy from the CLI flag values.
func parseSendPolicy(enabled bool, rulesStr string) (sendpolicy.SendPolicy, error) {
	p := sendpolicy.SendPolicy{Enabled: enabled}
	if rulesStr != "" {
		var ruleStrs []string
		for _, s := range strings.Split(rulesStr, ";") {
			s = strings.TrimSpace(s)
			if s != "" {
				ruleStrs = append(ruleStrs, s)
			}
		}
		rules, err := sendpolicy.ParseSendRules(ruleStrs)
		if err != nil {
			return p, err
		}
		p.Rules = rules
	}
	return p, nil
}
