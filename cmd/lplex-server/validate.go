package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sixfathoms/lplex"
	"github.com/sixfathoms/lplex/keeper"
	"github.com/sixfathoms/lplex/sendpolicy"
)

// runValidateConfig validates all configuration values and prints a report.
// Returns 0 if all checks pass, 1 if any fail.
func runValidateConfig(
	cfgPath string,
	maxBufDur, deviceIdleTimeout string,
	journalBlockSize, ringSize int,
	journalRotateDur, journalCompression string,
	retentionMaxAge, retentionMinKeep string,
	retentionMaxSize int64,
	retentionSoftPct int,
	retentionOverflowPolicy string,
	archiveCommand, archiveTriggerStr string,
	busSilenceTimeout, busSilenceThreshold string,
	alertDedupWindow string,
	claimHeartbeat, productInfoHeartbeat string,
	sendEnabled bool, sendRulesStr string,
	virtualDeviceEnabled bool, virtualDeviceName string,
	replMinLagReconnect string,
) int {
	if cfgPath != "" {
		fmt.Printf("Config file: %s\n", cfgPath)
	} else {
		fmt.Println("Config file: (none)")
	}
	fmt.Println()

	var errors, warnings int

	check := func(name, value string, validate func() error) {
		if value == "" {
			fmt.Printf("  [OK]   %-35s (not set)\n", name)
			return
		}
		if err := validate(); err != nil {
			fmt.Printf("  [FAIL] %-35s %s\n", name, err)
			errors++
		} else {
			fmt.Printf("  [OK]   %-35s %s\n", name, value)
		}
	}

	// Durations
	check("max-buffer-duration", maxBufDur, func() error {
		_, err := lplex.ParseISO8601Duration(maxBufDur)
		return err
	})

	check("device-idle-timeout", deviceIdleTimeout, func() error {
		_, err := time.ParseDuration(deviceIdleTimeout)
		return err
	})

	check("journal-rotate-duration", journalRotateDur, func() error {
		if journalRotateDur == "" {
			return nil
		}
		_, err := lplex.ParseISO8601Duration(journalRotateDur)
		return err
	})

	check("journal-retention-max-age", retentionMaxAge, func() error {
		_, err := lplex.ParseISO8601Duration(retentionMaxAge)
		return err
	})

	check("journal-retention-min-keep", retentionMinKeep, func() error {
		_, err := lplex.ParseISO8601Duration(retentionMinKeep)
		return err
	})

	check("bus-silence-timeout", busSilenceTimeout, func() error {
		_, err := lplex.ParseISO8601Duration(busSilenceTimeout)
		return err
	})

	check("bus-silence-threshold", busSilenceThreshold, func() error {
		_, err := lplex.ParseISO8601Duration(busSilenceThreshold)
		return err
	})

	check("alert-dedup-window", alertDedupWindow, func() error {
		_, err := time.ParseDuration(alertDedupWindow)
		return err
	})

	check("virtual-device-claim-heartbeat", claimHeartbeat, func() error {
		_, err := time.ParseDuration(claimHeartbeat)
		return err
	})

	check("virtual-device-product-info-heartbeat", productInfoHeartbeat, func() error {
		_, err := time.ParseDuration(productInfoHeartbeat)
		return err
	})

	check("replication-min-lag-reconnect-interval", replMinLagReconnect, func() error {
		_, err := time.ParseDuration(replMinLagReconnect)
		return err
	})

	// Numeric validations
	check("ring-size", fmt.Sprintf("%d", ringSize), func() error {
		if ringSize <= 0 {
			return fmt.Errorf("must be positive")
		}
		if ringSize&(ringSize-1) != 0 {
			return fmt.Errorf("must be a power of 2")
		}
		return nil
	})

	check("journal-block-size", fmt.Sprintf("%d", journalBlockSize), func() error {
		if journalBlockSize < 4096 {
			return fmt.Errorf("must be >= 4096")
		}
		if journalBlockSize&(journalBlockSize-1) != 0 {
			return fmt.Errorf("must be a power of 2")
		}
		return nil
	})

	check("journal-retention-soft-pct", fmt.Sprintf("%d", retentionSoftPct), func() error {
		if retentionSoftPct < 1 || retentionSoftPct > 99 {
			return fmt.Errorf("must be 1-99")
		}
		return nil
	})

	// Enum validations
	check("journal-compression", journalCompression, func() error {
		switch journalCompression {
		case "none", "zstd", "zstd-dict":
			return nil
		default:
			return fmt.Errorf("must be none, zstd, or zstd-dict")
		}
	})

	check("journal-retention-overflow-policy", retentionOverflowPolicy, func() error {
		_, err := keeper.ParseOverflowPolicy(retentionOverflowPolicy)
		return err
	})

	check("journal-archive-trigger", archiveTriggerStr, func() error {
		_, err := keeper.ParseArchiveTrigger(archiveTriggerStr)
		return err
	})

	// Send rules
	if sendRulesStr != "" {
		check("send-rules", sendRulesStr, func() error {
			var ruleStrs []string
			for _, s := range strings.Split(sendRulesStr, ";") {
				s = strings.TrimSpace(s)
				if s != "" {
					ruleStrs = append(ruleStrs, s)
				}
			}
			_, err := sendpolicy.ParseSendRules(ruleStrs)
			return err
		})
	}

	// Virtual device
	if virtualDeviceEnabled {
		if virtualDeviceName == "" {
			fmt.Printf("  [FAIL] %-35s required when -virtual-device is set\n", "virtual-device-name")
			errors++
		} else {
			check("virtual-device-name", virtualDeviceName, func() error {
				_, err := strconv.ParseUint(virtualDeviceName, 16, 64)
				if err != nil {
					return fmt.Errorf("must be 64-bit hex: %w", err)
				}
				return nil
			})
		}
	}

	// Warnings
	if retentionMaxSize > 0 && retentionMaxAge == "" {
		fmt.Printf("  [WARN] %-35s max-size set without max-age\n", "retention policy")
		warnings++
	}
	if archiveCommand != "" && archiveTriggerStr == "" {
		fmt.Printf("  [WARN] %-35s archive command set but no trigger\n", "archive config")
		warnings++
	}

	fmt.Println()
	if errors > 0 {
		fmt.Printf("%d error(s), %d warning(s)\n", errors, warnings)
		return 1
	}
	if warnings > 0 {
		fmt.Printf("Config valid with %d warning(s)\n", warnings)
	} else {
		fmt.Println("Config valid")
	}
	return 0
}
