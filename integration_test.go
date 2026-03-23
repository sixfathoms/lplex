package lplex

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/sixfathoms/lplex/proto/replication/v1"
	"github.com/sixfathoms/lplex/sendpolicy"
	"google.golang.org/grpc"
)

// startCloudHTTP creates an HTTP server with the same routes as the cloud
// binary's registerCloudHTTP. Returns the test server (caller must Close it).
func startCloudHTTP(t *testing.T, im *InstanceManager, replServer *ReplicationServer) *httptest.Server {
	t.Helper()

	logger := slog.Default()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /instances", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := struct {
			Instances []InstanceSummary `json:"instances"`
		}{
			Instances: im.List(),
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("GET /instances/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		inst := replServer.GetInstanceState(id)
		if inst == nil {
			http.Error(w, "instance not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inst.Status())
	})

	mux.HandleFunc("GET /instances/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		broker := replServer.GetInstanceBroker(id)
		if broker == nil {
			http.Error(w, "instance not found or broker not running", http.StatusNotFound)
			return
		}
		srv := NewServer(broker, logger, sendpolicy.SendPolicy{})
		srv.HandleEphemeralSSE(w, r)
	})

	mux.HandleFunc("GET /instances/{id}/devices", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		broker := replServer.GetInstanceBroker(id)
		if broker == nil {
			http.Error(w, "instance not found or broker not running", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(broker.Devices().SnapshotJSON())
	})

	mux.HandleFunc("GET /instances/{id}/values", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		broker := replServer.GetInstanceBroker(id)
		if broker == nil {
			http.Error(w, "instance not found or broker not running", http.StatusNotFound)
			return
		}
		filter, err := ParseFilterParams(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(broker.Values().SnapshotJSON(broker.Devices(), filter))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestIntegrationBoatToCloudSSE is an end-to-end integration test that:
//  1. Starts a cloud stack (InstanceManager + ReplicationServer + gRPC server)
//  2. Starts a cloud HTTP server with the same routes as lplex-cloud
//  3. Starts a boat-side Broker and ReplicationClient
//  4. Feeds CAN frames into the boat broker
//  5. Verifies the frames arrive on the cloud side via the HTTP SSE endpoint
//  6. Verifies cloud REST endpoints (/instances, /instances/{id}/status, devices, values)
func TestIntegrationBoatToCloudSSE(t *testing.T) {
	logger := slog.Default()

	// --- Cloud side: gRPC ---
	cloudAddr, replServer, im, cleanup := startCloudStack(t)
	defer cleanup()

	// --- Cloud side: HTTP ---
	httpServer := startCloudHTTP(t, im, replServer)

	// --- Boat side ---
	boatBroker := NewBroker(BrokerConfig{
		RingSize: 4096,
		Logger:   logger,
	})
	go boatBroker.Run()
	defer boatBroker.CloseRx()

	// Feed initial frames before replication starts.
	feedFrames(boatBroker, 20)
	waitForSeq(t, boatBroker, 20)

	// Start replication client (boat -> cloud).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	replDone := make(chan error, 1)
	go func() {
		replDone <- NewReplicationClient(ReplicationClientConfig{
			Target:     cloudAddr,
			InstanceID: "integration-boat",
			Logger:     logger,
		}, boatBroker).Run(ctx)
	}()

	// Wait for cloud broker to appear and receive the initial frames.
	var cloudBroker *Broker
	deadline := time.After(5 * time.Second)
	for cloudBroker == nil {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for cloud broker to start")
		default:
			cloudBroker = replServer.GetInstanceBroker("integration-boat")
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitForCloudSeq(t, cloudBroker, 20, 5*time.Second)
	t.Logf("cloud broker received initial 20 frames (head=%d)", cloudBroker.CurrentSeq())

	// --- Verify SSE endpoint: connect and read replicated frames ---
	t.Run("SSE_stream", func(t *testing.T) {
		resp, err := http.Get(httpServer.URL + "/instances/integration-boat/events")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("SSE status: got %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("content-type: got %q, want text/event-stream", ct)
		}

		// Feed more frames while SSE is connected.
		feedFrames(boatBroker, 10)
		waitForSeq(t, boatBroker, 30)
		waitForCloudSeq(t, cloudBroker, 30, 5*time.Second)

		// Read SSE events.
		scanner := bufio.NewScanner(resp.Body)
		received := make(chan frameJSON, 20)
		go func() {
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					var msg frameJSON
					if json.Unmarshal([]byte(line[6:]), &msg) == nil && msg.Seq > 0 {
						received <- msg
					}
				}
			}
		}()

		// We should receive at least one frame (the live ones we just fed).
		select {
		case msg := <-received:
			if msg.PGN != 129025 {
				t.Errorf("PGN: got %d, want 129025", msg.PGN)
			}
			if msg.Seq == 0 {
				t.Error("seq should not be 0")
			}
			t.Logf("received SSE frame: seq=%d pgn=%d src=%d", msg.Seq, msg.PGN, msg.Src)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for SSE event from cloud")
		}
	})

	// --- Verify SSE endpoint returns 404 for unknown instance ---
	t.Run("SSE_unknown_instance", func(t *testing.T) {
		resp, err := http.Get(httpServer.URL + "/instances/nonexistent/events")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status: got %d, want 404", resp.StatusCode)
		}
	})

	// --- Verify /instances endpoint lists the boat ---
	t.Run("instances_list", func(t *testing.T) {
		resp, err := http.Get(httpServer.URL + "/instances")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		var body struct {
			Instances []InstanceSummary `json:"instances"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}

		if len(body.Instances) == 0 {
			t.Fatal("expected at least 1 instance")
		}

		found := false
		for _, inst := range body.Instances {
			if inst.ID == "integration-boat" {
				found = true
				t.Logf("instance: id=%s cursor=%d", inst.ID, inst.Cursor)
			}
		}
		if !found {
			t.Error("integration-boat not found in instances list")
		}
	})

	// --- Verify /instances/{id}/status ---
	t.Run("instance_status", func(t *testing.T) {
		resp, err := http.Get(httpServer.URL + "/instances/integration-boat/status")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		var status InstanceStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			t.Fatal(err)
		}

		if status.Cursor == 0 {
			t.Error("cursor should not be 0")
		}
		t.Logf("instance status: cursor=%d holes=%d boat_head=%d",
			status.Cursor, len(status.Holes), status.BoatHeadSeq)
	})

	// --- Verify /instances/{id}/devices ---
	t.Run("instance_devices", func(t *testing.T) {
		resp, err := http.Get(httpServer.URL + "/instances/integration-boat/devices")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		// Devices may be empty since we didn't send address claim PGNs,
		// but the endpoint should return valid JSON.
		var devices []Device
		if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
			t.Fatal(err)
		}
		t.Logf("devices: %d", len(devices))
	})

	// --- Verify /instances/{id}/values ---
	t.Run("instance_values", func(t *testing.T) {
		resp, err := http.Get(httpServer.URL + "/instances/integration-boat/values")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}
		t.Log("values endpoint returned 200")
	})

	// --- Verify SSE with PGN filter ---
	t.Run("SSE_with_filter", func(t *testing.T) {
		resp, err := http.Get(httpServer.URL + "/instances/integration-boat/events?pgn=129025")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("SSE status: got %d, want 200", resp.StatusCode)
		}

		// Feed a matching and non-matching frame.
		boatBroker.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22},
		}
		boatBroker.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129026, Source: 2, Destination: 0xFF},
			Data:      []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88},
		}

		waitForSeq(t, boatBroker, 32)
		waitForCloudSeq(t, cloudBroker, 32, 5*time.Second)

		scanner := bufio.NewScanner(resp.Body)
		received := make(chan frameJSON, 10)
		go func() {
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					var msg frameJSON
					if json.Unmarshal([]byte(line[6:]), &msg) == nil && msg.Seq > 0 {
						received <- msg
					}
				}
			}
		}()

		select {
		case msg := <-received:
			if msg.PGN != 129025 {
				t.Errorf("filtered PGN: got %d, want 129025", msg.PGN)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for filtered SSE event")
		}

		// The 129026 frame should not arrive.
		select {
		case msg := <-received:
			if msg.PGN == 129026 {
				t.Error("should not receive PGN 129026 through filter")
			}
		case <-time.After(500 * time.Millisecond):
			// good - filtered out
		}
	})

	cancel()
	<-replDone
	t.Log("integration test: boat-to-cloud SSE round-trip verified")
}

// TestIntegrationMultiBoatCloudSSE verifies that two boats replicating to the
// same cloud can each be read independently via their SSE endpoints.
func TestIntegrationMultiBoatCloudSSE(t *testing.T) {
	logger := slog.Default()

	cloudAddr, replServer, im, cleanup := startCloudStack(t)
	defer cleanup()

	httpServer := startCloudHTTP(t, im, replServer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two boat brokers.
	boat1 := NewBroker(BrokerConfig{RingSize: 1024, Logger: logger})
	go boat1.Run()
	defer boat1.CloseRx()

	boat2 := NewBroker(BrokerConfig{RingSize: 1024, Logger: logger})
	go boat2.Run()
	defer boat2.CloseRx()

	// Feed different PGNs to each boat for identification.
	for range 10 {
		boat1.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		}
	}
	for range 10 {
		boat2.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129026, Source: 2, Destination: 0xFF},
			Data:      []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88},
		}
	}
	waitForSeq(t, boat1, 10)
	waitForSeq(t, boat2, 10)

	// Start replication for both.
	done1 := make(chan error, 1)
	done2 := make(chan error, 1)
	go func() {
		done1 <- NewReplicationClient(ReplicationClientConfig{
			Target: cloudAddr, InstanceID: "multi-sse-1", Logger: logger,
		}, boat1).Run(ctx)
	}()
	go func() {
		done2 <- NewReplicationClient(ReplicationClientConfig{
			Target: cloudAddr, InstanceID: "multi-sse-2", Logger: logger,
		}, boat2).Run(ctx)
	}()

	// Wait for both cloud brokers.
	var cloud1, cloud2 *Broker
	deadline := time.After(5 * time.Second)
	for cloud1 == nil || cloud2 == nil {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for cloud brokers")
		default:
			if cloud1 == nil {
				cloud1 = replServer.GetInstanceBroker("multi-sse-1")
			}
			if cloud2 == nil {
				cloud2 = replServer.GetInstanceBroker("multi-sse-2")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitForCloudSeq(t, cloud1, 10, 5*time.Second)
	waitForCloudSeq(t, cloud2, 10, 5*time.Second)

	// Verify /instances lists both.
	resp, err := http.Get(httpServer.URL + "/instances")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Instances []InstanceSummary `json:"instances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Instances) < 2 {
		t.Fatalf("expected at least 2 instances, got %d", len(body.Instances))
	}

	// Feed more frames to trigger SSE delivery.
	for range 5 {
		boat1.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7, 0xA8},
		}
		boat2.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129026, Source: 2, Destination: 0xFF},
			Data:      []byte{0xB1, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8},
		}
	}
	waitForSeq(t, boat1, 15)
	waitForSeq(t, boat2, 15)
	waitForCloudSeq(t, cloud1, 15, 5*time.Second)
	waitForCloudSeq(t, cloud2, 15, 5*time.Second)

	// Read SSE from each instance and verify PGN isolation.
	readSSEFrame := func(t *testing.T, url string) frameJSON {
		t.Helper()
		sseResp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sseResp.Body.Close() }()

		scanner := bufio.NewScanner(sseResp.Body)
		ch := make(chan frameJSON, 1)
		go func() {
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					var msg frameJSON
					if json.Unmarshal([]byte(line[6:]), &msg) == nil && msg.Seq > 0 {
						ch <- msg
						return
					}
				}
			}
		}()

		// Feed one more frame to each to ensure live delivery.
		boat1.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC},
		}
		boat2.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129026, Source: 2, Destination: 0xFF},
			Data:      []byte{0xDD, 0xDD, 0xDD, 0xDD, 0xDD, 0xDD, 0xDD, 0xDD},
		}

		select {
		case msg := <-ch:
			return msg
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for SSE frame")
			return frameJSON{}
		}
	}

	msg1 := readSSEFrame(t, httpServer.URL+"/instances/multi-sse-1/events")
	if msg1.PGN != 129025 {
		t.Errorf("boat-1 PGN: got %d, want 129025", msg1.PGN)
	}

	msg2 := readSSEFrame(t, httpServer.URL+"/instances/multi-sse-2/events")
	if msg2.PGN != 129026 {
		t.Errorf("boat-2 PGN: got %d, want 129026", msg2.PGN)
	}

	cancel()
	<-done1
	<-done2
	t.Log("multi-boat cloud SSE verified: each instance serves its own frames")
}

// TestIntegrationCloudSSELiveDelivery verifies that frames fed into the boat
// while an SSE client is connected to the cloud are delivered in real-time.
func TestIntegrationCloudSSELiveDelivery(t *testing.T) {
	logger := slog.Default()

	cloudAddr, replServer, im, cleanup := startCloudStack(t)
	defer cleanup()

	httpServer := startCloudHTTP(t, im, replServer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	boatBroker := NewBroker(BrokerConfig{RingSize: 1024, Logger: logger})
	go boatBroker.Run()
	defer boatBroker.CloseRx()

	// Start replication.
	replDone := make(chan error, 1)
	go func() {
		replDone <- NewReplicationClient(ReplicationClientConfig{
			Target: cloudAddr, InstanceID: "live-sse", Logger: logger,
		}, boatBroker).Run(ctx)
	}()

	// Wait for cloud broker.
	var cloudBroker *Broker
	deadline := time.After(5 * time.Second)
	for cloudBroker == nil {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for cloud broker")
		default:
			cloudBroker = replServer.GetInstanceBroker("live-sse")
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Connect SSE before feeding any frames.
	sseResp, err := http.Get(httpServer.URL + "/instances/live-sse/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sseResp.Body.Close() }()

	scanner := bufio.NewScanner(sseResp.Body)
	received := make(chan frameJSON, 50)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var msg frameJSON
				if json.Unmarshal([]byte(line[6:]), &msg) == nil && msg.Seq > 0 {
					received <- msg
				}
			}
		}
	}()

	// Now feed frames into the boat. They should flow:
	// boat broker -> ReplicationClient -> gRPC -> cloud broker -> SSE
	const frameCount = 10
	for i := range frameCount {
		boatBroker.RxFrames() <- RxFrame{
			Timestamp: time.Now(),
			Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
			Data:      []byte{byte(i), 1, 2, 3, 4, 5, 6, 7},
		}
	}
	waitForSeq(t, boatBroker, uint64(frameCount))
	waitForCloudSeq(t, cloudBroker, uint64(frameCount), 5*time.Second)

	// Read all frames from SSE.
	var frames []frameJSON
	timeout := time.After(5 * time.Second)
	for len(frames) < frameCount {
		select {
		case msg := <-received:
			frames = append(frames, msg)
		case <-timeout:
			t.Fatalf("timeout: received %d/%d frames via SSE", len(frames), frameCount)
		}
	}

	// Verify sequential delivery.
	for i, f := range frames {
		if f.PGN != 129025 {
			t.Errorf("frame %d: PGN got %d, want 129025", i, f.PGN)
		}
		if i > 0 && f.Seq <= frames[i-1].Seq {
			t.Errorf("frame %d: seq %d not greater than previous %d", i, f.Seq, frames[i-1].Seq)
		}
	}

	t.Logf("received %d frames via cloud SSE in real-time (seqs %d-%d)",
		len(frames), frames[0].Seq, frames[len(frames)-1].Seq)

	cancel()
	<-replDone
}

// TestIntegrationCloudSSEDisconnectReconnect verifies that data continues
// flowing through SSE after the boat disconnects and reconnects.
func TestIntegrationCloudSSEDisconnectReconnect(t *testing.T) {
	logger := slog.Default()

	cloudDataDir := t.TempDir()
	imObj, err := NewInstanceManager(cloudDataDir, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer imObj.Shutdown()

	replServer := NewReplicationServer(imObj, logger)

	grpcServer, cloudAddr := startGRPC(t, replServer)
	defer grpcServer.Stop()

	httpServer := startCloudHTTP(t, imObj, replServer)

	boatBroker := NewBroker(BrokerConfig{RingSize: 4096, Logger: logger})
	go boatBroker.Run()
	defer boatBroker.CloseRx()

	// Phase 1: First connection, feed frames 1-20.
	feedFrames(boatBroker, 20)
	waitForSeq(t, boatBroker, 20)

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() {
		done1 <- NewReplicationClient(ReplicationClientConfig{
			Target: cloudAddr, InstanceID: "reconnect-sse", Logger: logger,
		}, boatBroker).Run(ctx1)
	}()

	var cloudBroker *Broker
	deadline := time.After(5 * time.Second)
	for cloudBroker == nil {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for cloud broker")
		default:
			cloudBroker = replServer.GetInstanceBroker("reconnect-sse")
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitForCloudSeq(t, cloudBroker, 20, 5*time.Second)
	t.Logf("phase 1: cloud received 20 frames")

	// Disconnect.
	cancel1()
	<-done1

	// Phase 2: Feed frames while disconnected.
	feedFrames(boatBroker, 20) // frames 21-40
	waitForSeq(t, boatBroker, 40)

	// Phase 3: Reconnect.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	done2 := make(chan error, 1)
	go func() {
		done2 <- NewReplicationClient(ReplicationClientConfig{
			Target: cloudAddr, InstanceID: "reconnect-sse", Logger: logger,
		}, boatBroker).Run(ctx2)
	}()

	// Feed live frames after reconnect.
	feedFrames(boatBroker, 10) // frames 41-50
	waitForSeq(t, boatBroker, 50)
	waitForCloudSeq(t, cloudBroker, 50, 5*time.Second)
	t.Logf("phase 3: cloud caught up to 50 frames")

	// Connect SSE and verify we can read live frames.
	sseResp, err := http.Get(httpServer.URL + "/instances/reconnect-sse/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sseResp.Body.Close() }()

	scanner := bufio.NewScanner(sseResp.Body)
	received := make(chan frameJSON, 10)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var msg frameJSON
				if json.Unmarshal([]byte(line[6:]), &msg) == nil && msg.Seq > 0 {
					received <- msg
				}
			}
		}
	}()

	// Feed one more frame for live delivery.
	boatBroker.RxFrames() <- RxFrame{
		Timestamp: time.Now(),
		Header:    CANHeader{Priority: 2, PGN: 129025, Source: 1, Destination: 0xFF},
		Data:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
	}
	waitForSeq(t, boatBroker, 51)
	waitForCloudSeq(t, cloudBroker, 51, 5*time.Second)

	select {
	case msg := <-received:
		if msg.PGN != 129025 {
			t.Errorf("PGN: got %d, want 129025", msg.PGN)
		}
		t.Logf("SSE frame after reconnect: seq=%d", msg.Seq)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SSE frame after reconnect")
	}

	// Verify instance status reflects the reconnect.
	statusResp, err := http.Get(httpServer.URL + "/instances/reconnect-sse/status")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = statusResp.Body.Close() }()

	var status InstanceStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	t.Logf("instance status after reconnect: cursor=%d holes=%d boat_head=%d",
		status.Cursor, len(status.Holes), status.BoatHeadSeq)

	cancel2()
	<-done2
	t.Log("disconnect/reconnect SSE verified")
}

// startGRPC starts a gRPC server for the given ReplicationServer and returns
// the server and its address.
func startGRPC(t *testing.T, replServer *ReplicationServer) (*grpc.Server, string) {
	t.Helper()

	grpcSrv := grpc.NewServer()
	pb.RegisterReplicationServer(grpcSrv, replServer)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = grpcSrv.Serve(lis) }()

	return grpcSrv, lis.Addr().String()
}

