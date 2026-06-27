package collector

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

func newTestEasyTier(t *testing.T) (*EasyTier, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	cfg := config.EasyTierConfig{
		Enabled:    true,
		RPCAddress: "127.0.0.1:15888",
		CLIPath:    "easytier-cli",
	}
	et, err := NewEasyTier(cfg, reg, slog.Default())
	if err != nil {
		t.Fatalf("NewEasyTier: %v", err)
	}
	return et, reg
}

// --- isPeerP2P ---

func TestIsPeerP2P(t *testing.T) {
	tests := []struct {
		name string
		peer peerEntry
		want bool
	}{
		{
			name: "cost p2p",
			peer: peerEntry{Cost: "p2p"},
			want: true,
		},
		{
			name: "cost P2P uppercase",
			peer: peerEntry{Cost: "P2P"},
			want: true,
		},
		{
			name: "relay cost",
			peer: peerEntry{Cost: "relay(2)"},
			want: false,
		},
		{
			name: "empty cost",
			peer: peerEntry{Cost: ""},
			want: false,
		},
		{
			name: "Local cost",
			peer: peerEntry{Cost: "Local"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPeerP2P(tt.peer)
			if got != tt.want {
				t.Errorf("isPeerP2P(%+v) = %v, want %v", tt.peer, got, tt.want)
			}
		})
	}
}

// --- parseExpositionText ---

func TestParseExpositionText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int // number of metrics
	}{
		{
			name: "simple gauges and counters",
			input: `# HELP easytier_peer_count Number of peers
# TYPE easytier_peer_count gauge
easytier_peer_count 5
easytier_rx_bytes_total 123456
easytier_tx_bytes_total 654321
`,
			want: 3,
		},
		{
			name: "metrics with labels",
			input: `easytier_peer_latency{peer="10.0.0.1",hostname="node-a"} 12.5
easytier_peer_latency{peer="10.0.0.2",hostname="node-b"} 8.3
`,
			want: 2,
		},
		{
			name:  "empty input",
			input: "",
			want:  0,
		},
		{
			name:  "only comments",
			input: "# HELP foo\n# TYPE foo gauge\n",
			want:  0,
		},
		{
			name:  "blank lines and whitespace",
			input: "\n  \n\n",
			want:  0,
		},
		{
			name:  "malformed line with one field",
			input: "just_a_name\n",
			want:  0,
		},
		{
			name:  "non-numeric value",
			input: "metric_name not_a_number\n",
			want:  0,
		},
		{
			name:  "scientific notation",
			input: "metric_a 1.5e3\nmetric_b 0\n",
			want:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseExpositionText(tt.input)
			if len(got) != tt.want {
				t.Errorf("len(result) = %d, want %d; got %+v", len(got), tt.want, got)
			}
		})
	}
}

// --- NewEasyTier metric registration ---

func TestNewEasyTier_MetricRegistration(t *testing.T) {
	// First registration should succeed
	_, _ = newTestEasyTier(t)

	// Verify no panic on construction — metrics registered cleanly
}

func TestNewEasyTier_DoubleRegisterPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	cfg := config.EasyTierConfig{Enabled: true}
	logger := slog.Default()

	_, err := NewEasyTier(cfg, reg, logger)
	if err != nil {
		t.Fatalf("first NewEasyTier: %v", err)
	}

	// Second registration to same registry should panic (prometheus MustRegister)
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on double-register, got nil")
		}
	}()
	_, _ = NewEasyTier(cfg, reg, logger)
}

// --- Peer/route JSON parsing ---

func TestPeerEntryUnmarshal(t *testing.T) {
	// Use actual easytier-cli JSON format (all strings).
	raw := `[{
		"cidr": "10.144.144.1/24",
		"ipv4": "10.144.144.1",
		"hostname": "node-alpha",
		"cost": "p2p",
		"lat_ms": "5.20",
		"loss_rate": "1.0%",
		"rx_bytes": "1.02 kB",
		"tx_bytes": "2.05 kB",
		"tunnel_proto": "udp_p2p",
		"nat_type": "FullCone",
		"id": "abc123",
		"version": "1.2.3"
	}]`

	var peers []peerEntry
	if err := json.Unmarshal([]byte(raw), &peers); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}

	p := peers[0]
	if p.Cost != "p2p" {
		t.Errorf("Cost = %q, want %q", p.Cost, "p2p")
	}
	if p.Hostname != "node-alpha" {
		t.Errorf("Hostname = %q", p.Hostname)
	}
	if p.IPv4 != "10.144.144.1" {
		t.Errorf("IPv4 = %q", p.IPv4)
	}
	if p.LatMs != "5.20" {
		t.Errorf("LatMs = %q, want %q", p.LatMs, "5.20")
	}
	if p.LossRate != "1.0%" {
		t.Errorf("LossRate = %q", p.LossRate)
	}
	if p.RxBytes != "1.02 kB" {
		t.Errorf("RxBytes = %q", p.RxBytes)
	}
	if p.TxBytes != "2.05 kB" {
		t.Errorf("TxBytes = %q", p.TxBytes)
	}
	if p.TunnelProto != "udp_p2p" {
		t.Errorf("TunnelProto = %q", p.TunnelProto)
	}
	if p.ID != "abc123" {
		t.Errorf("ID = %q", p.ID)
	}
	if p.CIDR != "10.144.144.1/24" {
		t.Errorf("CIDR = %q", p.CIDR)
	}
}

func TestRouteEntryUnmarshal(t *testing.T) {
	// Use actual easytier-cli JSON format.
	raw := `[{
		"ipv4": "10.144.144.2/24",
		"hostname": "node-beta",
		"proxy_cidrs": "192.168.1.0/24",
		"next_hop_ipv4": "10.144.144.1/24",
		"next_hop_hostname": "node-alpha",
		"next_hop_lat": 3.5,
		"path_len": 2,
		"path_latency": 12,
		"next_hop_ipv4_lat_first": "10.144.144.1/24",
		"next_hop_hostname_lat_first": "node-alpha",
		"path_len_lat_first": 2,
		"path_latency_lat_first": 12,
		"version": "1.2.3"
	}]`

	var routes []routeEntry
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("len = %d, want 1", len(routes))
	}

	r := routes[0]
	if r.IPv4 != "10.144.144.2/24" {
		t.Errorf("IPv4 = %q", r.IPv4)
	}
	if r.Hostname != "node-beta" {
		t.Errorf("Hostname = %q", r.Hostname)
	}
	if r.NextHopIPv4 != "10.144.144.1/24" {
		t.Errorf("NextHopIPv4 = %q", r.NextHopIPv4)
	}
	if r.PathLen != 2 {
		t.Errorf("PathLen = %d, want 2", r.PathLen)
	}
	if r.PathLatency != 12 {
		t.Errorf("PathLatency = %d, want 12", r.PathLatency)
	}
	if r.ProxyCIDRs != "192.168.1.0/24" {
		t.Errorf("ProxyCIDRs = %q", r.ProxyCIDRs)
	}
	if r.NextHopHostname != "node-alpha" {
		t.Errorf("NextHopHostname = %q", r.NextHopHostname)
	}
}

func TestPeerEntryUnmarshal_EmptyArray(t *testing.T) {
	var peers []peerEntry
	if err := json.Unmarshal([]byte(`[]`), &peers); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(peers) != 0 {
		t.Errorf("len = %d, want 0", len(peers))
	}
}

func TestRouteEntryUnmarshal_EmptyArray(t *testing.T) {
	var routes []routeEntry
	if err := json.Unmarshal([]byte(`[]`), &routes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("len = %d, want 0", len(routes))
	}
}

// --- Parsing helpers ---

func TestParseFloat(t *testing.T) {
	tests := []struct {
		input string
		want  float64
		err   bool
	}{
		{"5.20", 5.2, false},
		{"0", 0, false},
		{"-", 0, true},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := parseFloat(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("parseFloat(%q) error=%v, wantErr=%v", tt.input, err, tt.err)
		}
		if err == nil && got != tt.want {
			t.Errorf("parseFloat(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParsePercentage(t *testing.T) {
	tests := []struct {
		input string
		want  float64
		err   bool
	}{
		{"0.0%", 0, false},
		{"50.0%", 0.5, false},
		{"100.0%", 1.0, false},
		{"-", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := parsePercentage(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("parsePercentage(%q) error=%v, wantErr=%v", tt.input, err, tt.err)
		}
		if err == nil && got != tt.want {
			t.Errorf("parsePercentage(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseHumanBytes(t *testing.T) {
	tests := []struct {
		input string
		want  uint64
		err   bool
	}{
		{"0 B", 0, false},
		{"1.5 kB", 1500, false},
		{"2.5 MB", 2500000, false},
		{"1 GB", 1000000000, false},
		{"-", 0, true},
		{"", 0, true},
		{"abc", 0, true},
		{"100", 100, false},
	}
	for _, tt := range tests {
		got, err := parseHumanBytes(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("parseHumanBytes(%q) error=%v, wantErr=%v", tt.input, err, tt.err)
		}
		if err == nil && got != tt.want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestStripCIDR(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"10.1.1.1/24", "10.1.1.1"},
		{"10.1.1.1", "10.1.1.1"},
		{"", ""},
	}
	for _, tt := range tests {
		got := stripCIDR(tt.input)
		if got != tt.want {
			t.Errorf("stripCIDR(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Multi-instance config ---

func TestResolvedInstances_Explicit(t *testing.T) {
	cfg := config.EasyTierConfig{
		CLIPath: "easytier-cli",
		Instances: []config.EasyTierInstance{
			{RPCAddress: "127.0.0.1:15888", NetworkName: "locus-mesh"},
			{RPCAddress: "127.0.0.1:15889", NetworkName: "coscene-mesh"},
		},
	}
	instances := cfg.ResolvedInstances()
	if len(instances) != 2 {
		t.Fatalf("len = %d, want 2", len(instances))
	}
	if instances[0].CLIPath != "easytier-cli" {
		t.Errorf("inherited CLIPath = %q", instances[0].CLIPath)
	}
	if instances[1].NetworkName != "coscene-mesh" {
		t.Errorf("NetworkName = %q", instances[1].NetworkName)
	}
}

func TestResolvedInstances_BackwardCompat(t *testing.T) {
	cfg := config.EasyTierConfig{
		RPCAddress: "127.0.0.1:15888",
		CLIPath:    "easytier-cli",
	}
	instances := cfg.ResolvedInstances()
	if len(instances) != 1 {
		t.Fatalf("len = %d, want 1", len(instances))
	}
	if instances[0].NetworkName != "default" {
		t.Errorf("NetworkName = %q, want %q", instances[0].NetworkName, "default")
	}
}

// --- parseMetricLine ---

func TestParseMetricLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantName  string
		wantValue float64
		wantOk    bool
	}{
		{
			name:      "simple metric",
			line:      "foo_total 42",
			wantName:  "foo_total",
			wantValue: 42,
			wantOk:    true,
		},
		{
			name:      "metric with labels",
			line:      `bar_gauge{host="a",port="80"} 3.14`,
			wantName:  "bar_gauge",
			wantValue: 3.14,
			wantOk:    true,
		},
		{
			name:   "empty line",
			line:   "",
			wantOk: false,
		},
		{
			name:   "single word",
			line:   "orphan",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, _, value, ok := parseMetricLine(tt.line)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if value != tt.wantValue {
				t.Errorf("value = %v, want %v", value, tt.wantValue)
			}
		})
	}
}
