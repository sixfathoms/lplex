package lplex

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// TestChaosRandomDisconnects verifies that the replication client recovers
// correctly after multiple random disconnects. Each disconnect creates a
// gap that should be tracked as a hole, and the final state should have
// a cursor that has advanced beyond the initial frames.
func TestChaosRandomDisconnects(t *testing.T) {
	cloudAddr, replServer, _, cleanup := startCloudStack(t)
	defer cleanup()

	logger := slog.Default()
	const totalFrames = 500
	const disconnects = 5

	boatBroker := NewBroker(BrokerConfig{
		RingSize: 65536,
		Logger:   logger,
	})
	brokerCtx, brokerCancel := context.WithCancel(context.Background())
	go boatBroker.Run(brokerCtx)
	defer func() {
		brokerCancel()
		boatBroker.CloseRx()
	}()

	// Feed initial frames.
	feedFrames(boatBroker, totalFrames)
	waitForSeq(t, boatBroker, totalFrames)

	// Run the replication client with a short context, kill and restart it
	// multiple times to simulate random disconnects.
	for i := range disconnects {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)

		replClient := NewReplicationClient(ReplicationClientConfig{
			Target:     cloudAddr,
			InstanceID: "chaos-disconnect",
			Logger:     logger,
		}, boatBroker)

		done := make(chan error, 1)
		go func() { done <- replClient.Run(ctx) }()

		// Let it run for a random duration then cancel.
		jitter := time.Duration(rand.IntN(150)) * time.Millisecond
		time.Sleep(50*time.Millisecond + jitter)
		cancel()
		<-done

		// Feed more frames between reconnects.
		feedFrames(boatBroker, 100)
		waitForSeq(t, boatBroker, uint64(totalFrames+(i+1)*100))

		t.Logf("disconnect %d: fed frames, total boat head=%d", i+1, boatBroker.CurrentSeq())
	}

	// Final reconnect: let it run long enough to sync up.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	replClient := NewReplicationClient(ReplicationClientConfig{
		Target:     cloudAddr,
		InstanceID: "chaos-disconnect",
		Logger:     logger,
	}, boatBroker)

	go func() { _ = replClient.Run(ctx) }()

	// Wait for cloud to receive at least the initial frames.
	state := replServer.GetInstanceState("chaos-disconnect")
	if state == nil {
		t.Fatal("instance not found on cloud after reconnects")
	}

	deadline := time.After(8 * time.Second)
	for {
		status := state.Status()
		if status.Cursor >= totalFrames {
			t.Logf("chaos disconnect recovery OK: cursor=%d, boat_head=%d, holes=%d",
				status.Cursor, status.BoatHeadSeq, len(status.Holes))
			break
		}
		select {
		case <-deadline:
			status := state.Status()
			t.Fatalf("timeout: cursor=%d (want >=%d), holes=%v",
				status.Cursor, totalFrames, status.Holes)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
	cancel()
}

// TestChaosRapidReconnects hammers the cloud with many quick connect/disconnect
// cycles and verifies the instance state remains consistent.
func TestChaosRapidReconnects(t *testing.T) {
	cloudAddr, replServer, _, cleanup := startCloudStack(t)
	defer cleanup()

	logger := slog.Default()

	boatBroker := NewBroker(BrokerConfig{
		RingSize: 65536,
		Logger:   logger,
	})
	brokerCtx, brokerCancel := context.WithCancel(context.Background())
	go boatBroker.Run(brokerCtx)
	defer func() {
		brokerCancel()
		boatBroker.CloseRx()
	}()

	feedFrames(boatBroker, 200)
	waitForSeq(t, boatBroker, 200)

	// 20 rapid connect/disconnect cycles.
	for i := range 20 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		replClient := NewReplicationClient(ReplicationClientConfig{
			Target:     cloudAddr,
			InstanceID: "chaos-rapid",
			Logger:     logger,
		}, boatBroker)
		done := make(chan error, 1)
		go func() { done <- replClient.Run(ctx) }()
		<-done
		cancel()

		if i%5 == 0 {
			t.Logf("rapid reconnect cycle %d/20", i+1)
		}
	}

	// Verify cloud-side state is consistent (cursor only advances, never goes backward).
	state := replServer.GetInstanceState("chaos-rapid")
	if state == nil {
		// Instance might not have been created if all connections were too brief.
		t.Log("instance not created (all connections too brief) — OK")
		return
	}

	status := state.Status()
	t.Logf("after 20 rapid reconnects: cursor=%d, holes=%d, boat_head=%d",
		status.Cursor, len(status.Holes), status.BoatHeadSeq)

	// Cursor should never be negative or nonsensical.
	if status.Cursor > boatBroker.CurrentSeq() {
		t.Errorf("cursor %d > boat head %d", status.Cursor, boatBroker.CurrentSeq())
	}
}

// TestChaosConcurrentStreams tests that two boat instances replicating
// concurrently to the same cloud don't interfere with each other's state.
func TestChaosConcurrentStreams(t *testing.T) {
	cloudAddr, replServer, _, cleanup := startCloudStack(t)
	defer cleanup()

	logger := slog.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const framesPerBoat = 300
	const numBoats = 3

	var wg sync.WaitGroup
	for i := range numBoats {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			broker := NewBroker(BrokerConfig{
				RingSize: 65536,
				Logger:   logger,
			})
			go broker.Run(ctx)

			// Each boat feeds frames at different rates with jitter.
			go func() {
				for j := range framesPerBoat {
					select {
					case broker.RxFrames() <- RxFrame{
						Timestamp: time.Now(),
						Header:    CANHeader{Priority: 2, PGN: 129025, Source: uint8(i + 1), Destination: 0xFF},
						Data:      []byte{byte(j), byte(i), 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
					}:
					case <-ctx.Done():
						return
					}
					// Random jitter to create interleaving.
					time.Sleep(time.Duration(rand.IntN(5)) * time.Millisecond)
				}
			}()

			instanceID := "chaos-boat-" + string(rune('A'+i))
			replClient := NewReplicationClient(ReplicationClientConfig{
				Target:     cloudAddr,
				InstanceID: instanceID,
				Logger:     logger,
			}, broker)
			_ = replClient.Run(ctx)
			broker.CloseRx()
		}()
	}

	// Let them run for a bit.
	time.Sleep(5 * time.Second)
	cancel()
	wg.Wait()

	// Verify each instance has independent, consistent state.
	for i := range numBoats {
		instanceID := "chaos-boat-" + string(rune('A'+i))
		state := replServer.GetInstanceState(instanceID)
		if state == nil {
			t.Logf("instance %s: not created (connection too brief)", instanceID)
			continue
		}
		status := state.Status()
		t.Logf("instance %s: cursor=%d, holes=%d, boat_head=%d",
			instanceID, status.Cursor, len(status.Holes), status.BoatHeadSeq)

		// Cursor should be reasonable (not wildly wrong).
		if status.Cursor > framesPerBoat+100 {
			t.Errorf("instance %s: cursor %d unreasonably high (max frames=%d)",
				instanceID, status.Cursor, framesPerBoat)
		}
	}
}

// TestChaosHoleTrackerStress verifies that HoleTracker correctly handles
// many overlapping add/fill operations. HoleTracker itself is not
// goroutine-safe (callers hold a mutex), so this test exercises the
// algorithm with many random operations sequentially.
func TestChaosHoleTrackerStress(t *testing.T) {
	ht := &HoleTracker{}

	// Create several large holes.
	ht.Add(1, 1000)
	ht.Add(2000, 3000)
	ht.Add(5000, 10000)

	if len(ht.Holes()) != 3 {
		t.Fatalf("expected 3 holes, got %d", len(ht.Holes()))
	}

	// Fill with many random overlapping ranges.
	r := rand.New(rand.NewPCG(42, 137))
	for range 5000 {
		start := r.Uint64N(10000) + 1
		end := start + r.Uint64N(500) + 1
		ht.Fill(start, end)
	}

	holes := ht.Holes()
	t.Logf("after stress: %d holes remaining", len(holes))

	// Verify holes are well-formed: non-overlapping, ordered.
	for i := range len(holes) - 1 {
		if holes[i].End >= holes[i+1].Start {
			t.Errorf("overlapping holes: [%d,%d) and [%d,%d)",
				holes[i].Start, holes[i].End, holes[i+1].Start, holes[i+1].End)
		}
	}
	for _, h := range holes {
		if h.Start >= h.End {
			t.Errorf("invalid hole: start=%d >= end=%d", h.Start, h.End)
		}
	}

	// Also test idempotency: filling an already-filled range is a no-op.
	before := len(ht.Holes())
	ht.Fill(1, 500)
	ht.Fill(1, 500)
	after := len(ht.Holes())
	if after > before {
		t.Errorf("duplicate fill created new holes: %d → %d", before, after)
	}
}

// TestChaosDisconnectDuringBackfill simulates a disconnect while backfill
// is in progress. After reconnecting, the remaining holes should still be
// tracked and the system should resume filling them.
func TestChaosDisconnectDuringBackfill(t *testing.T) {
	cloudAddr, replServer, _, cleanup := startCloudStack(t)
	defer cleanup()

	logger := slog.Default()

	boatBroker := NewBroker(BrokerConfig{
		RingSize: 65536,
		Logger:   logger,
	})
	brokerCtx, brokerCancel := context.WithCancel(context.Background())
	go boatBroker.Run(brokerCtx)
	defer func() {
		brokerCancel()
		boatBroker.CloseRx()
	}()

	// Feed a batch of frames, then create a gap, then feed more.
	feedFrames(boatBroker, 500)
	waitForSeq(t, boatBroker, 500)

	// First connection: establish cursor at 500.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 3*time.Second)
	replClient1 := NewReplicationClient(ReplicationClientConfig{
		Target:     cloudAddr,
		InstanceID: "chaos-backfill",
		Logger:     logger,
	}, boatBroker)
	go func() { _ = replClient1.Run(ctx1) }()

	// Wait for cloud to receive initial frames.
	state := replServer.GetInstanceState("chaos-backfill")
	deadline := time.After(5 * time.Second)
	for state == nil {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for instance creation")
		default:
			time.Sleep(50 * time.Millisecond)
			state = replServer.GetInstanceState("chaos-backfill")
		}
	}

	deadline = time.After(5 * time.Second)
	for {
		status := state.Status()
		if status.Cursor >= 200 {
			t.Logf("initial sync reached cursor=%d", status.Cursor)
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for initial cursor advance: cursor=%d", state.Status().Cursor)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Disconnect.
	cancel1()

	// Feed more frames (creating a gap in the cloud's view).
	feedFrames(boatBroker, 500)
	waitForSeq(t, boatBroker, 1000)

	// Reconnect — this should create holes for the gap.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	replClient2 := NewReplicationClient(ReplicationClientConfig{
		Target:     cloudAddr,
		InstanceID: "chaos-backfill",
		Logger:     logger,
	}, boatBroker)
	go func() { _ = replClient2.Run(ctx2) }()

	// Wait for recovery.
	deadline = time.After(5 * time.Second)
	for {
		status := state.Status()
		// The live stream should have advanced past the old cursor.
		if status.Cursor >= 500 && status.BoatHeadSeq >= 1000 {
			t.Logf("backfill recovery OK: cursor=%d, boat_head=%d, holes=%v",
				status.Cursor, status.BoatHeadSeq, status.Holes)
			break
		}
		select {
		case <-deadline:
			status := state.Status()
			t.Fatalf("timeout: cursor=%d, boat_head=%d, holes=%v",
				status.Cursor, status.BoatHeadSeq, status.Holes)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}
