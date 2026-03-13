package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sixfathoms/lplex/lplexc"
)

// resolveServerURL resolves a server URL from flags and config.
// Priority: explicit --server > boat mDNS/cloud > generic mDNS discovery.
func resolveServerURL(serverFlag string, boat *BoatConfig, mdnsTimeout time.Duration) string {
	if serverFlag != "" {
		return serverFlag
	}

	if boat != nil {
		return resolveBoatServer(boat, mdnsTimeout)
	}

	// No boat config, try generic mDNS discovery.
	discovered, err := discoverAny(mdnsTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mDNS discovery failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "specify --server explicitly, e.g. --server http://inuc1.local:8089\n")
		os.Exit(1)
	}
	log.Printf("discovered lplex at %s", discovered)
	return discovered
}

// resolveBoatServer resolves a server URL for a specific boat config.
func resolveBoatServer(boat *BoatConfig, mdnsTimeout time.Duration) string {
	if boat.MDNS != "" {
		url, err := discoverNamed(boat.MDNS, mdnsTimeout)
		if err == nil {
			log.Printf("discovered %s via mDNS at %s", boat.Name, url)
			return url
		}
		log.Printf("mDNS discovery for %s failed: %v", boat.Name, err)
	}
	if boat.Cloud != "" {
		log.Printf("using cloud endpoint for %s: %s", boat.Name, boat.Cloud)
		return boat.Cloud
	}
	fmt.Fprintf(os.Stderr, "boat %s: mDNS failed and no cloud URL configured\n", boat.Name)
	os.Exit(1)
	return "" // unreachable
}

func discoverNamed(name string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return lplexc.DiscoverNamed(ctx, name)
	}
	return lplexc.DiscoverNamed(context.Background(), name)
}

func discoverAny(timeout time.Duration) (string, error) {
	if timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return lplexc.Discover(ctx)
	}
	return lplexc.Discover(context.Background())
}

// loadBoatConfig loads config and resolves the boat if --boat is set.
// Returns the resolved boat config (or nil), the mdns timeout, and merged exclusion lists.
func loadBoatConfig(boatName, configPath string, boatSet bool) (boat *BoatConfig, mdnsTimeout time.Duration, excludePGNs []uint32, excludeNames []string, err error) {
	cfgPath := configPath
	if cfgPath == "" {
		cfgPath = defaultConfigPath()
	}
	dc, err := loadConfig(cfgPath)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	mdnsTimeout = dc.MDNSTimeout

	// Merge global config exclusions.
	excludePGNs = append(excludePGNs, dc.ExcludePGNs...)
	excludeNames = append(excludeNames, dc.ExcludeNames...)

	if boatSet {
		bc, err := resolveBoat(boatName, dc.Boats)
		if err != nil {
			return nil, 0, nil, nil, err
		}
		boat = &bc

		// Merge per-boat config exclusions.
		excludePGNs = append(excludePGNs, bc.ExcludePGNs...)
		excludeNames = append(excludeNames, bc.ExcludeNames...)
	}

	return boat, mdnsTimeout, excludePGNs, excludeNames, nil
}
