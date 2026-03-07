package lplexc

import (
	"context"
	"fmt"
	"time"

	"github.com/grandcat/zeroconf"
)

// Discover browses for a _lplex._tcp mDNS service on the local network
// and returns the URL of the first instance found. It blocks until a
// service is discovered or the context is cancelled.
//
// A default 3-second timeout is applied if the context has no deadline.
func Discover(ctx context.Context) (string, error) {
	return discover(ctx, "")
}

// DiscoverNamed browses for a _lplex._tcp mDNS service whose instance name
// matches the given name (e.g. "inuc1"). This lets you target a specific lplex
// server when multiple are on the network. Non-matching services are ignored.
//
// A default 3-second timeout is applied if the context has no deadline.
func DiscoverNamed(ctx context.Context, name string) (string, error) {
	return discover(ctx, name)
}

func discover(ctx context.Context, name string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
	}

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return "", fmt.Errorf("creating mDNS resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry)

	if err := resolver.Browse(ctx, "_lplex._tcp", "local.", entries); err != nil {
		return "", fmt.Errorf("browsing mDNS: %w", err)
	}

	for {
		select {
		case entry := <-entries:
			if name != "" && entry.Instance != name {
				continue
			}
			return entryURL(entry)
		case <-ctx.Done():
			if name != "" {
				return "", fmt.Errorf("no _lplex._tcp service named %q found on the network", name)
			}
			return "", fmt.Errorf("no _lplex._tcp service found on the network")
		}
	}
}

func entryURL(entry *zeroconf.ServiceEntry) (string, error) {
	if len(entry.AddrIPv4) > 0 {
		return fmt.Sprintf("http://%s:%d", entry.AddrIPv4[0], entry.Port), nil
	}
	if len(entry.AddrIPv6) > 0 {
		return fmt.Sprintf("http://[%s]:%d", entry.AddrIPv6[0], entry.Port), nil
	}
	return "", fmt.Errorf("service found but no addresses in mDNS entry")
}
