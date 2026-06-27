package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// EasyTier collects mesh peer/route metrics via easytier-cli.
type EasyTier struct {
	cfg    config.EasyTierConfig
	logger *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Gauges
	peerLatency    *prometheus.GaugeVec
	peerLossRate   *prometheus.GaugeVec
	peerConnType   *prometheus.GaugeVec
	peerRxBytes    *prometheus.GaugeVec
	peerTxBytes    *prometheus.GaugeVec
	routePathLen   *prometheus.GaugeVec
	routePathLat   *prometheus.GaugeVec
	peersTotal     *prometheus.GaugeVec
	peersP2P       *prometheus.GaugeVec
	peersRelay     *prometheus.GaugeVec

	// Info
	nodeInfo *prometheus.GaugeVec
}

// peerEntry matches easytier-cli peer --output json structure.
type peerEntry struct {
	Cost     int     `json:"cost"`
	Hostname string  `json:"hostname"`
	IPv4     string  `json:"ipv4"`
	Latency  float64 `json:"latency_ms"`
	LossRate float64 `json:"loss_rate"`
	RxBytes  uint64  `json:"rx_bytes"`
	TxBytes  uint64  `json:"tx_bytes"`
	Tunnel   string  `json:"tunnel_proto"`
	Version  string  `json:"version"`
	NatType  string  `json:"udp_stun_info"`
	PeerID   string  `json:"peer_id"`
	Conns    []connEntry `json:"conns"`
}

type connEntry struct {
	TunnelType string `json:"tunnel_type"`
}

// routeEntry matches easytier-cli route --output json structure.
type routeEntry struct {
	IPv4Addr    string  `json:"ipv4_addr"`
	Hostname    string  `json:"hostname"`
	NextHopIPv4 string  `json:"next_hop_ipv4"`
	Cost        int     `json:"cost"`
	LatencyMs   float64 `json:"path_latency"`
	ProxyCIDRs  []string `json:"proxy_cidrs"`
}

func NewEasyTier(cfg config.EasyTierConfig, reg *prometheus.Registry, logger *slog.Logger) (*EasyTier, error) {
	et := &EasyTier{
		cfg:    cfg,
		logger: logger.With("collector", "easytier"),

		peerLatency: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_latency_ms",
			Help: "RTT latency to peer in milliseconds",
		}, []string{"peer", "hostname"}),

		peerLossRate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_loss_rate",
			Help: "Packet loss rate to peer (0-1)",
		}, []string{"peer", "hostname"}),

		peerConnType: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_p2p",
			Help: "1 if peer connection is P2P, 0 if relay",
		}, []string{"peer", "hostname"}),

		peerRxBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_rx_bytes_total",
			Help: "Total bytes received from peer",
		}, []string{"peer", "hostname"}),

		peerTxBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peer_tx_bytes_total",
			Help: "Total bytes sent to peer",
		}, []string{"peer", "hostname"}),

		routePathLen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_route_path_length",
			Help: "Hop count to destination",
		}, []string{"destination", "hostname"}),

		routePathLat: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_route_path_latency_ms",
			Help: "Path latency to destination in milliseconds",
		}, []string{"destination", "hostname"}),

		peersTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peers_total",
			Help: "Total number of peers",
		}, []string{}),

		peersP2P: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peers_p2p",
			Help: "Number of peers connected via P2P",
		}, []string{}),

		peersRelay: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_peers_relay",
			Help: "Number of peers connected via relay",
		}, []string{}),

		nodeInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "easytier_node_info",
			Help: "Node information (always 1)",
		}, []string{"hostname", "ipv4", "peer_id"}),
	}

	reg.MustRegister(
		et.peerLatency, et.peerLossRate, et.peerConnType,
		et.peerRxBytes, et.peerTxBytes,
		et.routePathLen, et.routePathLat,
		et.peersTotal, et.peersP2P, et.peersRelay,
		et.nodeInfo,
	)

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
	et.collectPeers(ctx)
	et.collectRoutes(ctx)
}

func (et *EasyTier) collectPeers(ctx context.Context) {
	args := []string{"peer", "--output", "json"}
	if et.cfg.RPCAddress != "" {
		args = append(args, "-p", et.cfg.RPCAddress)
	}

	out, err := et.runCLI(ctx, args...)
	if err != nil {
		et.logger.Warn("peer collect failed", "error", err)
		return
	}

	var peers []peerEntry
	if err := json.Unmarshal(out, &peers); err != nil {
		et.logger.Warn("peer parse failed", "error", err, "output", string(out))
		return
	}

	// Reset all peer metrics before setting new values
	et.peerLatency.Reset()
	et.peerLossRate.Reset()
	et.peerConnType.Reset()
	et.peerRxBytes.Reset()
	et.peerTxBytes.Reset()

	var p2pCount, relayCount int

	for _, p := range peers {
		labels := prometheus.Labels{"peer": p.IPv4, "hostname": p.Hostname}

		et.peerLatency.With(labels).Set(p.Latency)
		et.peerLossRate.With(labels).Set(p.LossRate)
		et.peerRxBytes.With(labels).Set(float64(p.RxBytes))
		et.peerTxBytes.With(labels).Set(float64(p.TxBytes))

		isP2P := et.isPeerP2P(p)
		if isP2P {
			et.peerConnType.With(labels).Set(1)
			p2pCount++
		} else {
			et.peerConnType.With(labels).Set(0)
			relayCount++
		}
	}

	et.peersTotal.With(nil).Set(float64(len(peers)))
	et.peersP2P.With(nil).Set(float64(p2pCount))
	et.peersRelay.With(nil).Set(float64(relayCount))
}

func (et *EasyTier) isPeerP2P(p peerEntry) bool {
	// Check tunnel_proto field for "p2p" indicator
	tunnel := strings.ToLower(p.Tunnel)
	if strings.Contains(tunnel, "p2p") {
		return true
	}
	// Check connection entries
	for _, c := range p.Conns {
		ct := strings.ToLower(c.TunnelType)
		if strings.Contains(ct, "p2p") || strings.Contains(ct, "direct") {
			return true
		}
	}
	// Cost 1 typically means direct connection
	return p.Cost == 1
}

func (et *EasyTier) collectRoutes(ctx context.Context) {
	args := []string{"route", "--output", "json"}
	if et.cfg.RPCAddress != "" {
		args = append(args, "-p", et.cfg.RPCAddress)
	}

	out, err := et.runCLI(ctx, args...)
	if err != nil {
		et.logger.Warn("route collect failed", "error", err)
		return
	}

	var routes []routeEntry
	if err := json.Unmarshal(out, &routes); err != nil {
		et.logger.Warn("route parse failed", "error", err)
		return
	}

	et.routePathLen.Reset()
	et.routePathLat.Reset()

	for _, r := range routes {
		labels := prometheus.Labels{"destination": r.IPv4Addr, "hostname": r.Hostname}
		et.routePathLen.With(labels).Set(float64(r.Cost))
		if r.LatencyMs > 0 {
			et.routePathLat.With(labels).Set(r.LatencyMs)
		}
	}
}

func (et *EasyTier) runCLI(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, et.cfg.CLIPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}

// CollectNativeStats runs easytier-cli stats prometheus and returns the raw output.
// The caller can parse and register these metrics separately.
func (et *EasyTier) CollectNativeStats(ctx context.Context) (string, error) {
	args := []string{"stats", "prometheus"}
	if et.cfg.RPCAddress != "" {
		args = append(args, "-p", et.cfg.RPCAddress)
	}

	out, err := et.runCLI(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("native stats: %w", err)
	}

	return string(out), nil
}

// ParseNativeStats extracts metric values from Prometheus exposition text.
// Returns metric name -> value for simple counters/gauges (ignores labels for now).
func ParseNativeStats(text string) map[string]float64 {
	result := make(map[string]float64)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Simple format: metric_name{labels} value
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		// Strip labels from name for simple lookup
		if idx := strings.Index(name, "{"); idx >= 0 {
			name = name[:idx]
		}
		if val, err := strconv.ParseFloat(parts[len(parts)-1], 64); err == nil {
			result[name] = val
		}
	}
	return result
}
