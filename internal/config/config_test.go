package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func TestLoad_FullConfig(t *testing.T) {
	yaml := `
listen: ":8080"
collectors:
  easytier:
    enabled: true
    rpc_address: "10.0.0.1:15888"
    cli_path: "/usr/bin/easytier-cli"
    collect_interval: 30s
  singbox:
    enabled: true
    api_type: "clash"
    clash_address: "127.0.0.1:9091"
    clash_secret: "hunter2"
    collect_interval: 20s
push:
  enabled: true
  remote_write_url: "http://vm:8428/api/v1/import/prometheus"
  push_interval: 60s
  instance_label: "router-1"
topology:
  local_peers:
    - "10.144.144.1"
    - "10.144.144.2"
`
	cfg, err := Load(writeTestConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Listen != ":8080" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, ":8080")
	}

	et := cfg.Collectors.EasyTier
	if !et.Enabled {
		t.Error("EasyTier.Enabled = false, want true")
	}
	if et.RPCAddress != "10.0.0.1:15888" {
		t.Errorf("RPCAddress = %q, want %q", et.RPCAddress, "10.0.0.1:15888")
	}
	if et.CLIPath != "/usr/bin/easytier-cli" {
		t.Errorf("CLIPath = %q, want %q", et.CLIPath, "/usr/bin/easytier-cli")
	}
	if et.CollectInterval != 30*time.Second {
		t.Errorf("CollectInterval = %v, want %v", et.CollectInterval, 30*time.Second)
	}

	sb := cfg.Collectors.SingBox
	if !sb.Enabled {
		t.Error("SingBox.Enabled = false, want true")
	}
	if sb.APIType != "clash" {
		t.Errorf("APIType = %q, want %q", sb.APIType, "clash")
	}
	if sb.ClashAddress != "127.0.0.1:9091" {
		t.Errorf("ClashAddress = %q, want %q", sb.ClashAddress, "127.0.0.1:9091")
	}
	if sb.ClashSecret != "hunter2" {
		t.Errorf("ClashSecret = %q, want %q", sb.ClashSecret, "hunter2")
	}
	if sb.CollectInterval != 20*time.Second {
		t.Errorf("CollectInterval = %v, want %v", sb.CollectInterval, 20*time.Second)
	}

	if !cfg.Push.Enabled {
		t.Error("Push.Enabled = false, want true")
	}
	if cfg.Push.RemoteWriteURL != "http://vm:8428/api/v1/import/prometheus" {
		t.Errorf("RemoteWriteURL = %q", cfg.Push.RemoteWriteURL)
	}
	if cfg.Push.PushInterval != 60*time.Second {
		t.Errorf("PushInterval = %v, want %v", cfg.Push.PushInterval, 60*time.Second)
	}
	if cfg.Push.InstanceLabel != "router-1" {
		t.Errorf("InstanceLabel = %q, want %q", cfg.Push.InstanceLabel, "router-1")
	}

	if len(cfg.Topology.LocalPeers) != 2 {
		t.Fatalf("LocalPeers len = %d, want 2", len(cfg.Topology.LocalPeers))
	}
	if cfg.Topology.LocalPeers[0] != "10.144.144.1" {
		t.Errorf("LocalPeers[0] = %q", cfg.Topology.LocalPeers[0])
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	yaml := `
collectors:
  easytier:
    enabled: true
`
	cfg, err := Load(writeTestConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Listen != ":9550" {
		t.Errorf("default Listen = %q, want %q", cfg.Listen, ":9550")
	}
	if cfg.Collectors.EasyTier.RPCAddress != "127.0.0.1:15888" {
		t.Errorf("default RPCAddress = %q, want %q", cfg.Collectors.EasyTier.RPCAddress, "127.0.0.1:15888")
	}
	if cfg.Collectors.EasyTier.CLIPath != "easytier-cli" {
		t.Errorf("default CLIPath = %q, want %q", cfg.Collectors.EasyTier.CLIPath, "easytier-cli")
	}
	if cfg.Collectors.EasyTier.CollectInterval != 15*time.Second {
		t.Errorf("default EasyTier CollectInterval = %v, want %v", cfg.Collectors.EasyTier.CollectInterval, 15*time.Second)
	}
	if cfg.Collectors.SingBox.APIType != "grpc" {
		t.Errorf("default APIType = %q, want %q", cfg.Collectors.SingBox.APIType, "grpc")
	}
	if cfg.Collectors.SingBox.GRPCAddress != "127.0.0.1:9191" {
		t.Errorf("default GRPCAddress = %q, want %q", cfg.Collectors.SingBox.GRPCAddress, "127.0.0.1:9191")
	}
	if cfg.Collectors.SingBox.ClashAddress != "127.0.0.1:9090" {
		t.Errorf("default ClashAddress = %q, want %q", cfg.Collectors.SingBox.ClashAddress, "127.0.0.1:9090")
	}
	if cfg.Push.PushInterval != 30*time.Second {
		t.Errorf("default PushInterval = %v, want %v", cfg.Push.PushInterval, 30*time.Second)
	}
}

func TestLoad_NoCollectorEnabled(t *testing.T) {
	yaml := `
collectors:
  easytier:
    enabled: false
  singbox:
    enabled: false
`
	_, err := Load(writeTestConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error when no collector enabled, got nil")
	}
	want := "at least one collector must be enabled"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	yaml := `
listen: ":8080"
collectors:
  easytier:
    enabled: [not, valid, yaml:
`
	_, err := Load(writeTestConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestLoad_OnlySingBoxEnabled(t *testing.T) {
	yaml := `
collectors:
  singbox:
    enabled: true
    api_type: clash
`
	cfg, err := Load(writeTestConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Collectors.EasyTier.Enabled {
		t.Error("EasyTier should be disabled")
	}
	if !cfg.Collectors.SingBox.Enabled {
		t.Error("SingBox should be enabled")
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	// Empty YAML results in defaults, but no collector is enabled → validation error
	_, err := Load(writeTestConfig(t, ""))
	if err == nil {
		t.Fatal("expected error for empty config (no collector enabled), got nil")
	}
}

func TestLoad_ZeroIntervalClamped(t *testing.T) {
	yaml := `
collectors:
  easytier:
    enabled: true
    collect_interval: 0s
push:
  enabled: true
  remote_write_url: "http://vm:8428/api/v1/import/prometheus"
  push_interval: 0s
`
	cfg, err := Load(writeTestConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Collectors.EasyTier.CollectInterval <= 0 {
		t.Errorf("EasyTier CollectInterval should be clamped to positive, got %v", cfg.Collectors.EasyTier.CollectInterval)
	}
	if cfg.Push.PushInterval <= 0 {
		t.Errorf("PushInterval should be clamped to positive, got %v", cfg.Push.PushInterval)
	}
}
