package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// SingBox collects sing-box metrics via Clash API (v1) or native gRPC API (v2).
// v1 (Clash API) is implemented first; gRPC will be added when protobuf definitions are available.
type SingBox struct {
	cfg    config.SingBoxConfig
	logger *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup
	client *http.Client

	// Traffic (cumulative totals from external source — use Gauge, not Counter)
	uplinkTotal   prometheus.Gauge
	downlinkTotal prometheus.Gauge

	// Connections
	connectionsIn prometheus.Gauge

	// Outbound health
	outboundDelay *prometheus.GaugeVec
	outboundAlive *prometheus.GaugeVec
}

// clashProxiesResponse matches GET /proxies from Clash API.
type clashProxiesResponse struct {
	Proxies map[string]clashProxy `json:"proxies"`
}

type clashProxy struct {
	Type    string             `json:"type"`
	Name    string             `json:"name"`
	History []clashDelayEntry  `json:"history"`
	All     []string           `json:"all,omitempty"`
	Now     string             `json:"now,omitempty"`
}

type clashDelayEntry struct {
	Delay     int    `json:"delay"`
	MeanDelay int    `json:"meanDelay"`
	Time      string `json:"time"`
}

// clashConnectionsResponse matches GET /connections from Clash API.
type clashConnectionsResponse struct {
	DownloadTotal int64             `json:"downloadTotal"`
	UploadTotal   int64             `json:"uploadTotal"`
	Connections   []clashConnection `json:"connections"`
}

type clashConnection struct {
	ID       string                 `json:"id"`
	Metadata map[string]interface{} `json:"metadata"`
	Upload   int64                  `json:"upload"`
	Download int64                  `json:"download"`
}

func NewSingBox(cfg config.SingBoxConfig, reg *prometheus.Registry, logger *slog.Logger) (*SingBox, error) {
	sb := &SingBox{
		cfg:    cfg,
		logger: logger.With("collector", "singbox"),
		client: &http.Client{Timeout: 5 * time.Second},

		uplinkTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "singbox_uplink_bytes_total",
			Help: "Cumulative uplink bytes (from Clash API)",
		}),
		downlinkTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "singbox_downlink_bytes_total",
			Help: "Cumulative downlink bytes (from Clash API)",
		}),
		connectionsIn: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "singbox_connections_in",
			Help: "Active connections (from /connections count)",
		}),
		outboundDelay: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "singbox_outbound_delay_ms",
			Help: "Last URL test delay for outbound in milliseconds",
		}, []string{"outbound", "type"}),
		outboundAlive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "singbox_outbound_alive",
			Help: "1 if outbound has responded to URL test, 0 otherwise",
		}, []string{"outbound", "type"}),
	}

	reg.MustRegister(
		sb.uplinkTotal, sb.downlinkTotal,
		sb.connectionsIn,
		sb.outboundDelay, sb.outboundAlive,
	)

	return sb, nil
}

func (sb *SingBox) Name() string { return "singbox" }

func (sb *SingBox) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	sb.cancel = cancel

	// Initial collect
	sb.collect(ctx)

	sb.wg.Add(1)
	go func() {
		defer sb.wg.Done()
		ticker := time.NewTicker(sb.cfg.CollectInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sb.collect(ctx)
			}
		}
	}()

	return nil
}

func (sb *SingBox) Stop() {
	if sb.cancel != nil {
		sb.cancel()
	}
	sb.wg.Wait()
}

func (sb *SingBox) collect(ctx context.Context) {
	switch sb.cfg.APIType {
	case "grpc":
		// TODO: gRPC native API via SubscribeStatus/SubscribeOutbounds
		// Fallback to Clash API until gRPC client is implemented
		sb.logger.Debug("gRPC collector not yet implemented, falling back to Clash API")
		sb.collectClash(ctx)
	case "clash":
		sb.collectClash(ctx)
	default:
		sb.collectClash(ctx)
	}
}

func (sb *SingBox) collectClash(ctx context.Context) {
	sb.collectClashConnections(ctx)
	sb.collectClashProxies(ctx)
}

func (sb *SingBox) collectClashConnections(ctx context.Context) {
	body, err := sb.clashGet(ctx, "/connections")
	if err != nil {
		sb.logger.Warn("connections fetch failed", "error", err)
		return
	}

	var resp clashConnectionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		sb.logger.Warn("connections parse failed", "error", err)
		return
	}

	sb.uplinkTotal.Set(float64(resp.UploadTotal))
	sb.downlinkTotal.Set(float64(resp.DownloadTotal))
	sb.connectionsIn.Set(float64(len(resp.Connections)))
}

func (sb *SingBox) collectClashProxies(ctx context.Context) {
	body, err := sb.clashGet(ctx, "/proxies")
	if err != nil {
		sb.logger.Warn("proxies fetch failed", "error", err)
		return
	}

	var resp clashProxiesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		sb.logger.Warn("proxies parse failed", "error", err)
		return
	}

	sb.outboundDelay.Reset()
	sb.outboundAlive.Reset()

	for name, proxy := range resp.Proxies {
		labels := prometheus.Labels{"outbound": name, "type": proxy.Type}

		if len(proxy.History) > 0 {
			last := proxy.History[len(proxy.History)-1]
			sb.outboundDelay.With(labels).Set(float64(last.Delay))
			if last.Delay > 0 {
				sb.outboundAlive.With(labels).Set(1)
			} else {
				sb.outboundAlive.With(labels).Set(0)
			}
		}
	}
}

func (sb *SingBox) clashGet(ctx context.Context, path string) ([]byte, error) {
	addr := sb.cfg.ClashAddress
	if addr == "" {
		addr = "127.0.0.1:9090"
	}

	url := fmt.Sprintf("http://%s%s", addr, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	if sb.cfg.ClashSecret != "" {
		req.Header.Set("Authorization", "Bearer "+sb.cfg.ClashSecret)
	}

	resp, err := sb.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clash API %s returned %d", path, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
