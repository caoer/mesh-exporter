package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// EasyTier collects mesh peer/route metrics via easytier-cli.
// Supports multiple EasyTier instances, each identified by a network label.
type EasyTier struct {
	cfg       config.EasyTierConfig
	instances []config.EasyTierInstance
	logger    *slog.Logger
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	reg       *prometheus.Registry

	// Gauges — all include "network" label for multi-instance support.
	peerLatency  *prometheus.GaugeVec
	peerLossRate *prometheus.GaugeVec
	peerConnType *prometheus.GaugeVec
	peerRxBytes  *prometheus.GaugeVec
	peerTxBytes  *prometheus.GaugeVec
	routePathLen *prometheus.GaugeVec
	routePathLat *prometheus.GaugeVec
	peersTotal   *prometheus.GaugeVec
	peersP2P     *prometheus.GaugeVec
	peersRelay   *prometheus.GaugeVec

	// Info
	nodeInfo *prometheus.GaugeVec

	// Native stats collector (lazily created)
	nativeCollector *nativeStatsCollector
}

// peerEntry matches easytier-cli -o json peer list structure.
// All fields are strings — the CLI formats numbers as human-readable text.
type peerEntry struct {
	CIDR       string `json:"cidr"`         // CIDR notation, e.g. "10.26.1.1/24"
	IPv4       string `json:"ipv4"`         // bare IP, e.g. "10.26.1.1"
	Hostname   string `json:"hostname"`
	Cost       string `json:"cost"`         // "p2p", "relay(2)", "Local"
	LatMs      string `json:"lat_ms"`       // e.g. "1.23", "-" for local
	LossRate   string `json:"loss_rate"`    // e.g. "0.0%", "-" for local
	RxBytes    string `json:"rx_bytes"`     // human-readable, e.g. "1.5 MB"
	TxBytes    string `json:"tx_bytes"`     // human-readable, e.g. "2.3 MB"
	TunnelProto string `json:"tunnel_proto"` // e.g. "tcp,udp"
	NatType    string `json:"nat_type"`     // e.g. "FullCone", "Symmetric"
	ID         string `json:"id"`           // peer ID (numeric string)
	Version    string `json:"version"`
}

// routeEntry matches easytier-cli -o json route list structure.
type routeEntry struct {
	IPv4                    string `json:"ipv4"`         // CIDR notation
	Hostname                string `json:"hostname"`
	ProxyCIDRs              string `json:"proxy_cidrs"`  // comma-separated string
	NextHopIPv4             string `json:"next_hop_ipv4"`
	NextHopHostname         string `json:"next_hop_hostname"`
	NextHopLat              float64 `json:"next_hop_lat"`
	PathLen                 int    `json:"path_len"`     // hop count
	PathLatency             int    `json:"path_latency"` // ms
	NextHopIPv4LatFirst     string `json:"next_hop_ipv4_lat_first"`
	NextHopHostnameLatFirst string `json:"next_hop_hostname_lat_first"`
	PathLenLatFirst         int    `json:"path_len_lat_first"`
	PathLatencyLatFirst     int    `json:"path_latency_lat_first"`
	Version                 string `json:"version"`
}

func NewEasyTier(cfg config.EasyTierConfig, reg *prometheus.Registry, logger *slog.Logger) (*EasyTier, error) {
	instances := cfg.ResolvedInstances()

	// Verify CLI is available at startup — warn but don't crash.
	for _, inst := range instances {
		cliPath := inst.CLIPath
		if _, err := exec.LookPath(cliPath); err != nil {
			logger.Warn("easytier-cli not found in PATH, collection will be skipped",
				"cli_path", cliPath, "network", inst.NetworkName, "error", err)
		}
	}

	et := &EasyTier{
		cfg:       cfg,
		instances: instances,
		logger:    logger.With("collector", "easytier"),
		reg:       reg,

		peerLatency: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_latency_ms",
			Help: "RTT latency to peer in milliseconds",
		}, []string{"network", "peer", "hostname"}),

		peerLossRate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_loss_rate",
			Help: "Packet loss rate to peer (0-1)",
		}, []string{"network", "peer", "hostname"}),

		peerConnType: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_p2p",
			Help: "1 if peer connection is P2P, 0 if relay",
		}, []string{"network", "peer", "hostname"}),

		peerRxBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_rx_bytes_total",
			Help: "Total bytes received from peer",
		}, []string{"network", "peer", "hostname"}),

		peerTxBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_tx_bytes_total",
			Help: "Total bytes sent to peer",
		}, []string{"network", "peer", "hostname"}),

		routePathLen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_route_path_length",
			Help: "Hop count to destination",
		}, []string{"network", "destination", "hostname"}),

		routePathLat: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_route_path_latency_ms",
			Help: "Path latency to destination in milliseconds",
		}, []string{"network", "destination", "hostname"}),

		peersTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peers_total",
			Help: "Total number of peers",
		}, []string{"network"}),

		peersP2P: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peers_p2p",
			Help: "Number of peers connected via P2P",
		}, []string{"network"}),

		peersRelay: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peers_relay",
			Help: "Number of peers connected via relay",
		}, []string{"network"}),

		nodeInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_node_info",
			Help: "Node information (always 1)",
		}, []string{"network", "hostname", "ipv4", "peer_id"}),
	}

	reg.MustRegister(
		et.peerLatency, et.peerLossRate, et.peerConnType,
		et.peerRxBytes, et.peerTxBytes,
		et.routePathLen, et.routePathLat,
		et.peersTotal, et.peersP2P, et.peersRelay,
		et.nodeInfo,
	)

	// Set up native stats passthrough if enabled.
	if cfg.NativeStats {
		nc := newNativeStatsCollector(et)
		reg.MustRegister(nc)
		et.nativeCollector = nc
	}

	return et, nil
}

func (et *EasyTier) Name() string { return "easytier" }

func (et *EasyTier) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	et.cancel = cancel

	// Initial collect
	et.collect(ctx)

	et.wg.Add(1)
	go func() {
		defer et.wg.Done()
		ticker := time.NewTicker(et.cfg.CollectInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				et.collect(ctx)
			}
		}
	}()

	return nil
}

func (et *EasyTier) Stop() {
	if et.cancel != nil {
		et.cancel()
	}
	et.wg.Wait()
}

func (et *EasyTier) collect(ctx context.Context) {
	for _, inst := range et.instances {
		et.collectInstance(ctx, inst)
	}
}

func (et *EasyTier) collectInstance(ctx context.Context, inst config.EasyTierInstance) {
	et.collectPeers(ctx, inst)
	et.collectRoutes(ctx, inst)
	if et.nativeCollector != nil {
		et.collectNativeStatsForInstance(ctx, inst)
	}
}

func (et *EasyTier) collectPeers(ctx context.Context, inst config.EasyTierInstance) {
	// Global flags (-p, -o) must precede the subcommand in easytier-cli v2.x.
	var args []string
	if inst.RPCAddress != "" {
		args = append(args, "-p", inst.RPCAddress)
	}
	args = append(args, "-o", "json", "peer", "list")

	out, err := et.runCLI(ctx, inst.CLIPath, args...)
	if err != nil {
		et.logger.Warn("peer collect failed", "network", inst.NetworkName, "error", err)
		return
	}

	var peers []peerEntry
	if err := json.Unmarshal(out, &peers); err != nil {
		et.logger.Warn("peer parse failed", "network", inst.NetworkName, "error", err,
			"output", truncate(string(out), 200))
		return
	}

	// Reset metrics for this network before setting new values.
	// We delete only labels for this network to avoid clearing other instances.
	et.resetPeerMetricsForNetwork(inst.NetworkName)

	var p2pCount, relayCount int

	for _, p := range peers {
		// Skip the local node entry (cost="Local").
		if p.Cost == "Local" || p.Cost == "-" {
			// Register local node info.
			et.nodeInfo.With(prometheus.Labels{
				"network":  inst.NetworkName,
				"hostname": p.Hostname,
				"ipv4":     p.IPv4,
				"peer_id":  p.ID,
			}).Set(1)
			continue
		}

		labels := prometheus.Labels{
			"network":  inst.NetworkName,
			"peer":     p.IPv4,
			"hostname": p.Hostname,
		}

		if latency, err := parseFloat(p.LatMs); err == nil {
			et.peerLatency.With(labels).Set(latency)
		}

		if lossRate, err := parsePercentage(p.LossRate); err == nil {
			et.peerLossRate.With(labels).Set(lossRate)
		}

		if rxBytes, err := parseHumanBytes(p.RxBytes); err == nil {
			et.peerRxBytes.With(labels).Set(float64(rxBytes))
		}

		if txBytes, err := parseHumanBytes(p.TxBytes); err == nil {
			et.peerTxBytes.With(labels).Set(float64(txBytes))
		}

		isP2P := isPeerP2P(p)
		if isP2P {
			et.peerConnType.With(labels).Set(1)
			p2pCount++
		} else {
			et.peerConnType.With(labels).Set(0)
			relayCount++
		}
	}

	netLabels := prometheus.Labels{"network": inst.NetworkName}
	et.peersTotal.With(netLabels).Set(float64(p2pCount + relayCount))
	et.peersP2P.With(netLabels).Set(float64(p2pCount))
	et.peersRelay.With(netLabels).Set(float64(relayCount))
}

func (et *EasyTier) resetPeerMetricsForNetwork(network string) {
	// DeletePartialMatch removes all metrics where the "network" label matches.
	et.peerLatency.DeletePartialMatch(prometheus.Labels{"network": network})
	et.peerLossRate.DeletePartialMatch(prometheus.Labels{"network": network})
	et.peerConnType.DeletePartialMatch(prometheus.Labels{"network": network})
	et.peerRxBytes.DeletePartialMatch(prometheus.Labels{"network": network})
	et.peerTxBytes.DeletePartialMatch(prometheus.Labels{"network": network})
	et.nodeInfo.DeletePartialMatch(prometheus.Labels{"network": network})
}

func isPeerP2P(p peerEntry) bool {
	cost := strings.ToLower(p.Cost)
	return cost == "p2p"
}

func (et *EasyTier) collectRoutes(ctx context.Context, inst config.EasyTierInstance) {
	var args []string
	if inst.RPCAddress != "" {
		args = append(args, "-p", inst.RPCAddress)
	}
	args = append(args, "-o", "json", "route", "list")

	out, err := et.runCLI(ctx, inst.CLIPath, args...)
	if err != nil {
		et.logger.Warn("route collect failed", "network", inst.NetworkName, "error", err)
		return
	}

	var routes []routeEntry
	if err := json.Unmarshal(out, &routes); err != nil {
		et.logger.Warn("route parse failed", "network", inst.NetworkName, "error", err,
			"output", truncate(string(out), 200))
		return
	}

	// Reset route metrics for this network.
	et.routePathLen.DeletePartialMatch(prometheus.Labels{"network": inst.NetworkName})
	et.routePathLat.DeletePartialMatch(prometheus.Labels{"network": inst.NetworkName})

	for _, r := range routes {
		// Skip local node (next_hop_hostname="Local").
		if r.NextHopHostname == "Local" {
			continue
		}

		labels := prometheus.Labels{
			"network":     inst.NetworkName,
			"destination": stripCIDR(r.IPv4),
			"hostname":    r.Hostname,
		}
		et.routePathLen.With(labels).Set(float64(r.PathLen))
		if r.PathLatency > 0 {
			et.routePathLat.With(labels).Set(float64(r.PathLatency))
		}
	}
}

func (et *EasyTier) runCLI(ctx context.Context, cliPath string, args ...string) ([]byte, error) {
	// Check CLI exists before trying to run it.
	if _, err := exec.LookPath(cliPath); err != nil {
		return nil, fmt.Errorf("cli not found: %s", cliPath)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}

// collectNativeStatsForInstance fetches and caches native prometheus stats.
func (et *EasyTier) collectNativeStatsForInstance(ctx context.Context, inst config.EasyTierInstance) {
	var args []string
	if inst.RPCAddress != "" {
		args = append(args, "-p", inst.RPCAddress)
	}
	args = append(args, "stats", "prometheus")

	out, err := et.runCLI(ctx, inst.CLIPath, args...)
	if err != nil {
		et.logger.Warn("native stats collect failed", "network", inst.NetworkName, "error", err)
		return
	}

	et.nativeCollector.update(inst.NetworkName, string(out))
}

// --- Native Stats Passthrough ---

// nativeStatsCollector implements prometheus.Collector to pass through
// EasyTier's native Prometheus metrics with an "easytier_native_" prefix
// and a "network" label.
type nativeStatsCollector struct {
	et  *EasyTier
	mu  sync.RWMutex
	// network -> parsed metrics
	cache map[string][]nativeMetric
}

type nativeMetric struct {
	name   string
	help   string
	mtype  string // "counter", "gauge", "histogram", "summary", "untyped"
	labels map[string]string
	value  float64
}

func newNativeStatsCollector(et *EasyTier) *nativeStatsCollector {
	return &nativeStatsCollector{
		et:    et,
		cache: make(map[string][]nativeMetric),
	}
}

func (nc *nativeStatsCollector) update(network, expositionText string) {
	metrics := parseExpositionText(expositionText)
	nc.mu.Lock()
	nc.cache[network] = metrics
	nc.mu.Unlock()
}

// Describe implements prometheus.Collector. We use DescribeByCollect since
// the set of metrics is dynamic (may change between collections).
func (nc *nativeStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	// Signal unchecked collector — metrics are described dynamically.
}

// Collect implements prometheus.Collector. Emits cached native metrics
// with "easytier_native_" prefix and a "network" label added.
func (nc *nativeStatsCollector) Collect(ch chan<- prometheus.Metric) {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	for network, metrics := range nc.cache {
		for _, m := range metrics {
			prefixedName := "easytier_native_" + m.name

			// Merge the network label.
			labelNames := make([]string, 0, len(m.labels)+1)
			labelValues := make([]string, 0, len(m.labels)+1)
			labelNames = append(labelNames, "network")
			labelValues = append(labelValues, network)
			for k, v := range m.labels {
				labelNames = append(labelNames, k)
				labelValues = append(labelValues, v)
			}

			desc := prometheus.NewDesc(prefixedName, m.help, labelNames, nil)

			valType := prometheus.UntypedValue
			switch m.mtype {
			case "counter":
				valType = prometheus.CounterValue
			case "gauge":
				valType = prometheus.GaugeValue
			}

			metric, err := prometheus.NewConstMetric(desc, valType, m.value, labelValues...)
			if err != nil {
				nc.et.logger.Warn("native metric error",
					"name", prefixedName, "network", network, "error", err)
				continue
			}
			ch <- metric
		}
	}
}

// parseExpositionText parses Prometheus exposition format text into nativeMetric slices.
func parseExpositionText(text string) []nativeMetric {
	var metrics []nativeMetric
	helpMap := make(map[string]string)
	typeMap := make(map[string]string)

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "# HELP ") {
			parts := strings.SplitN(line[7:], " ", 2)
			if len(parts) == 2 {
				helpMap[parts[0]] = parts[1]
			}
			continue
		}

		if strings.HasPrefix(line, "# TYPE ") {
			parts := strings.SplitN(line[7:], " ", 2)
			if len(parts) == 2 {
				typeMap[parts[0]] = parts[1]
			}
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		// Parse: metric_name{label1="val1",label2="val2"} value
		name, labels, value, ok := parseMetricLine(line)
		if !ok {
			continue
		}

		metrics = append(metrics, nativeMetric{
			name:   name,
			help:   helpMap[name],
			mtype:  typeMap[name],
			labels: labels,
			value:  value,
		})
	}

	return metrics
}

var labelRegexp = regexp.MustCompile(`(\w+)="([^"]*)"`)

// parseMetricLine parses a single Prometheus exposition line.
func parseMetricLine(line string) (name string, labels map[string]string, value float64, ok bool) {
	// Split into name+labels part and value part.
	var metricPart string
	var valuePart string

	// Handle labels.
	if idx := strings.LastIndex(line, "}"); idx >= 0 {
		// Has labels: name{...} value
		valuePart = strings.TrimSpace(line[idx+1:])
		metricPart = line[:idx+1]
	} else {
		// No labels: name value
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return "", nil, 0, false
		}
		metricPart = parts[0]
		valuePart = parts[1]
	}

	val, err := strconv.ParseFloat(valuePart, 64)
	if err != nil {
		return "", nil, 0, false
	}

	// Extract name and labels.
	if braceIdx := strings.Index(metricPart, "{"); braceIdx >= 0 {
		name = metricPart[:braceIdx]
		labelStr := metricPart[braceIdx+1 : len(metricPart)-1]
		labels = make(map[string]string)
		for _, match := range labelRegexp.FindAllStringSubmatch(labelStr, -1) {
			labels[match[1]] = match[2]
		}
	} else {
		name = metricPart
		labels = make(map[string]string)
	}

	return name, labels, val, true
}

// --- Parsing Helpers ---

// parseFloat parses a string float, returning error for non-numeric values like "-".
func parseFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, fmt.Errorf("no value")
	}
	return strconv.ParseFloat(s, 64)
}

// parsePercentage parses "12.3%" into 0.123.
func parsePercentage(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, fmt.Errorf("no value")
	}
	s = strings.TrimSuffix(s, "%")
	pct, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return pct / 100.0, nil
}

// parseHumanBytes parses human-readable byte strings like "1.5 MB" into raw bytes.
// Supports B, kB, MB, GB, TB (decimal SI prefixes, matching humansize::DECIMAL).
func parseHumanBytes(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, fmt.Errorf("no value")
	}

	// Split into numeric part and unit.
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty value")
	}

	numStr := parts[0]
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse number %q: %w", numStr, err)
	}

	if len(parts) == 1 {
		// Plain number, assume bytes.
		return uint64(val), nil
	}

	unit := strings.ToUpper(parts[1])
	switch unit {
	case "B", "BYTES":
		return uint64(val), nil
	case "KB":
		return uint64(val * 1000), nil
	case "MB":
		return uint64(val * 1000 * 1000), nil
	case "GB":
		return uint64(val * 1000 * 1000 * 1000), nil
	case "TB":
		return uint64(val * 1000 * 1000 * 1000 * 1000), nil
	default:
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}
}

// stripCIDR removes the "/prefix" from a CIDR string, returning just the IP.
func stripCIDR(cidr string) string {
	if idx := strings.Index(cidr, "/"); idx >= 0 {
		return cidr[:idx]
	}
	return cidr
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
