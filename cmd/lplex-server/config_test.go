package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sixfathoms/lplex/requestrules"
)

func TestFindConfigFilePrefersLocal(t *testing.T) {
	tmp := t.TempDir()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := os.WriteFile("lplex.conf", []byte("port = 9000\n"), 0644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	got, err := findConfigFile("")
	if err != nil {
		t.Fatalf("findConfigFile: %v", err)
	}
	if got != "./lplex.conf" {
		t.Fatalf("expected ./lplex.conf, got %q", got)
	}
}

func TestFindConfigFileNewNameTakesPriority(t *testing.T) {
	tmp := t.TempDir()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Both exist; lplex-server.conf should win.
	if err := os.WriteFile("lplex.conf", []byte("port = 9000\n"), 0644); err != nil {
		t.Fatalf("write old config: %v", err)
	}
	if err := os.WriteFile("lplex-server.conf", []byte("port = 9001\n"), 0644); err != nil {
		t.Fatalf("write new config: %v", err)
	}

	got, err := findConfigFile("")
	if err != nil {
		t.Fatalf("findConfigFile: %v", err)
	}
	if got != "./lplex-server.conf" {
		t.Fatalf("expected ./lplex-server.conf, got %q", got)
	}
}

func TestFindConfigFileExplicitMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.conf")

	_, err := findConfigFile(path)
	if err == nil {
		t.Fatal("expected error for missing explicit config path")
	}
	if !strings.Contains(err.Error(), "config file not found: "+path) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyConfigCLIFlagsWin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := "interface = can9\nport = 9000\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := flag.CommandLine
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 8089, "")
	iface := fs.String("interface", "can0", "")
	if err := fs.Parse([]string{"-port", "7777"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flag.CommandLine = fs
	t.Cleanup(func() {
		flag.CommandLine = old
	})

	if _, err := applyConfig(path); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	if *port != 7777 {
		t.Fatalf("expected CLI port to win (7777), got %d", *port)
	}
	if *iface != "can9" {
		t.Fatalf("expected interface from config (can9), got %q", *iface)
	}
}

func TestApplyConfigSendRulesStringArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := `send { rules = ["pgn:59904", "!pgn:65280-65535"] }`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := flag.CommandLine
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sendRules := fs.String("send-rules", "", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flag.CommandLine = fs
	t.Cleanup(func() { flag.CommandLine = old })

	if _, err := applyConfig(path); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	if *sendRules != "pgn:59904;!pgn:65280-65535" {
		t.Fatalf("expected send-rules from string array, got %q", *sendRules)
	}
}

func TestApplyConfigSendRulesObjectFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := `send { rules = [
		{ pgn = "59904" }
		{ deny = true, pgn = "65280-65535" }
		{ pgn = "126208", name = "001c6e4000200000" }
		{ deny = true }
	] }`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := flag.CommandLine
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sendRules := fs.String("send-rules", "", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flag.CommandLine = fs
	t.Cleanup(func() { flag.CommandLine = old })

	if _, err := applyConfig(path); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	want := "pgn:59904;! pgn:65280-65535;pgn:126208 name:001c6e4000200000;!"
	if *sendRules != want {
		t.Fatalf("expected %q, got %q", want, *sendRules)
	}
}

func TestApplyConfigSendRulesObjectNameArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := `send { rules = [
		{ pgn = "129025-129029", name = ["001c6e4000200000", "001c6e4000200001"] }
	] }`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := flag.CommandLine
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sendRules := fs.String("send-rules", "", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flag.CommandLine = fs
	t.Cleanup(func() { flag.CommandLine = old })

	if _, err := applyConfig(path); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	want := "pgn:129025-129029 name:001c6e4000200000,001c6e4000200001"
	if *sendRules != want {
		t.Fatalf("expected %q, got %q", want, *sendRules)
	}
}

func TestApplyConfigSendRulesMixed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := `send { rules = [
		"pgn:59904"
		{ deny = true, pgn = "65280-65535" }
	] }`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := flag.CommandLine
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sendRules := fs.String("send-rules", "", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flag.CommandLine = fs
	t.Cleanup(func() { flag.CommandLine = old })

	if _, err := applyConfig(path); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	want := "pgn:59904;! pgn:65280-65535"
	if *sendRules != want {
		t.Fatalf("expected %q, got %q", want, *sendRules)
	}
}

func TestApplyConfigSetsUnsetFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := "interface = can9\nport = 9000\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := flag.CommandLine
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 8089, "")
	iface := fs.String("interface", "can0", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flag.CommandLine = fs
	t.Cleanup(func() {
		flag.CommandLine = old
	})

	if _, err := applyConfig(path); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	if *port != 9000 {
		t.Fatalf("expected port from config (9000), got %d", *port)
	}
	if *iface != "can9" {
		t.Fatalf("expected interface from config (can9), got %q", *iface)
	}
}

func TestApplyConfigRequestRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := `
requests-global-min-interval = "1s"
requests = [
  {
    name = "gpsmap-route"
    match { model-id = "GPSMAP*" }
    via = "iso"
    want = [130065, 130067]
    to-device = true
    on-online = true
    max-age = "30s"
    min-interval = "5s"
    invalidate-on = [129284]
  }
  {
    name = "victron-alarms"
    match { manufacturer-code = 358 }
    via = "frame"
    frame-pgn = 61184
    frame-template = "6699000000000000"
    subkey-write-offset = 2
    subkey-write-len = 2
    subkey-read-offset = 2
    subkey-read-len = 2
    want = [798, 796]
    to-device = true
    min-interval = "30s"
  }
]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := applyConfig(path)
	if err != nil {
		t.Fatalf("applyConfig: %v", err)
	}
	if res.RequestGlobalMinInterval != time.Second {
		t.Errorf("global min = %v, want 1s", res.RequestGlobalMinInterval)
	}
	if len(res.RequestRules) != 2 {
		t.Fatalf("got %d rules, want 2", len(res.RequestRules))
	}
	r0 := res.RequestRules[0]
	if r0.Name != "gpsmap-route" || r0.Match.ModelID != "GPSMAP*" || r0.Via != requestrules.ViaISORequest {
		t.Errorf("rule0 unexpected: %+v", r0)
	}
	if len(r0.Wants) != 2 || r0.Wants[0].PGN != 130065 || !r0.ToDevice || r0.MaxAge != 30*time.Second || r0.MinInterval != 5*time.Second {
		t.Errorf("rule0 fields unexpected: %+v", r0)
	}
	r1 := res.RequestRules[1]
	if r1.Via != requestrules.ViaFrame || r1.FramePGN != 61184 || len(r1.FrameTemplate) != 8 {
		t.Errorf("rule1 frame fields unexpected: %+v", r1)
	}
	if len(r1.Wants) != 2 || !r1.Wants[0].HasSubKey || r1.Wants[0].SubKey != 798 || r1.Wants[0].PGN != 61184 {
		t.Errorf("rule1 wants unexpected: %+v", r1.Wants)
	}
	if r1.SubKeyWriteOff != 2 || r1.SubKeyReadLen != 2 {
		t.Errorf("rule1 subkey offsets unexpected: %+v", r1)
	}
}

func TestApplyConfigRequestRuleMissingMinInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lplex.conf")
	content := `
requests = [ { name = "x", via = "iso", want = [1] } ]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := applyConfig(path); err == nil {
		t.Fatal("expected error for missing min-interval")
	}
}
