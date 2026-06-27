package collector

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func newTestSingBox(t *testing.T, clashAddr string) (*SingBox, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	cfg := config.SingBoxConfig{
		Enabled:         true,
		APIType:         "clash",
		ClashAddress:    clashAddr,
		CollectInterval: 15 * time.Second,
	}
	sb, err := NewSingBox(cfg, reg, slog.Default())
	if err != nil {
		t.Fatalf("NewSingBox: %v", err)
	}
	return sb, reg
}

func getGaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetGauge().GetValue()
}

func getGaugeVecValue(t *testing.T, gv *prometheus.GaugeVec, labels prometheus.Labels) float64 {
	t.Helper()
	g, err := gv.GetMetricWith(labels)
	if err != nil {
		t.Fatalf("get metric with labels %v: %v", labels, err)
	}
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetGauge().GetValue()
}

func TestSingBox_CollectClashConnections(t *testing.T) {
	resp := clashConnectionsResponse{
		DownloadTotal: 5000,
		UploadTotal:   3000,
		Connections: []clashConnection{
			{ID: "1", Upload: 100, Download: 200},
			{ID: "2", Upload: 50, Download: 150},
			{ID: "3", Upload: 75, Download: 300},
		},
	}
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connections" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	sb, _ := newTestSingBox(t, addr)
	sb.collectClashConnections(t.Context())

	connCount := getGaugeValue(t, sb.connectionsIn)
	if connCount != 3 {
		t.Errorf("connectionsIn = %v, want 3", connCount)
	}

	uplink := getGaugeValue(t, sb.uplinkTotal)
	if uplink != 3000 {
		t.Errorf("uplinkTotal = %v, want 3000", uplink)
	}

	downlink := getGaugeValue(t, sb.downlinkTotal)
	if downlink != 5000 {
		t.Errorf("downlinkTotal = %v, want 5000", downlink)
	}
}

func TestSingBox_CollectClashProxies(t *testing.T) {
	resp := clashProxiesResponse{
		Proxies: map[string]clashProxy{
			"proxy-hk": {
				Type: "Shadowsocks",
				Name: "proxy-hk",
				History: []clashDelayEntry{
					{Delay: 150, MeanDelay: 145, Time: "2025-01-01T00:00:00Z"},
				},
			},
			"proxy-us": {
				Type: "VMess",
				Name: "proxy-us",
				History: []clashDelayEntry{
					{Delay: 0, MeanDelay: 0, Time: "2025-01-01T00:00:00Z"},
				},
			},
			"proxy-jp": {
				Type:    "Trojan",
				Name:    "proxy-jp",
				History: []clashDelayEntry{}, // no history
			},
		},
	}
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxies" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	sb, _ := newTestSingBox(t, addr)
	sb.collectClashProxies(t.Context())

	// proxy-hk: delay=150, alive=1
	delay := getGaugeVecValue(t, sb.outboundDelay, prometheus.Labels{"outbound": "proxy-hk", "type": "Shadowsocks"})
	if delay != 150 {
		t.Errorf("proxy-hk delay = %v, want 150", delay)
	}
	alive := getGaugeVecValue(t, sb.outboundAlive, prometheus.Labels{"outbound": "proxy-hk", "type": "Shadowsocks"})
	if alive != 1 {
		t.Errorf("proxy-hk alive = %v, want 1", alive)
	}

	// proxy-us: delay=0, alive=0
	delay = getGaugeVecValue(t, sb.outboundDelay, prometheus.Labels{"outbound": "proxy-us", "type": "VMess"})
	if delay != 0 {
		t.Errorf("proxy-us delay = %v, want 0", delay)
	}
	alive = getGaugeVecValue(t, sb.outboundAlive, prometheus.Labels{"outbound": "proxy-us", "type": "VMess"})
	if alive != 0 {
		t.Errorf("proxy-us alive = %v, want 0", alive)
	}
}

func TestSingBox_ClashGetHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	sb, _ := newTestSingBox(t, addr)

	_, err := sb.clashGet(t.Context(), "/connections")
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want mention of 500", err.Error())
	}
}

func TestSingBox_ClashGetMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	sb, _ := newTestSingBox(t, addr)

	// clashGet returns raw bytes without parsing — the error surfaces at json.Unmarshal
	body, err := sb.clashGet(t.Context(), "/test")
	if err != nil {
		t.Fatalf("clashGet should succeed (raw bytes): %v", err)
	}

	var resp clashConnectionsResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		t.Error("expected JSON unmarshal error for malformed JSON")
	}
}

func TestSingBox_ClashGetConnectionRefused(t *testing.T) {
	// Use an address where nothing listens
	sb, _ := newTestSingBox(t, "127.0.0.1:1")

	_, err := sb.clashGet(t.Context(), "/connections")
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
}

func TestSingBox_ClashSecret(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"connections":[]}`))
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	reg := prometheus.NewRegistry()
	cfg := config.SingBoxConfig{
		Enabled:         true,
		APIType:         "clash",
		ClashAddress:    addr,
		ClashSecret:     "my-secret-token",
		CollectInterval: 15 * time.Second,
	}
	sb, err := NewSingBox(cfg, reg, slog.Default())
	if err != nil {
		t.Fatalf("NewSingBox: %v", err)
	}

	_, _ = sb.clashGet(t.Context(), "/connections")

	want := "Bearer my-secret-token"
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestSingBox_MetricRegistration(t *testing.T) {
	// Verify clean registration (no panic)
	_, _ = newTestSingBox(t, "127.0.0.1:1")
}

func TestSingBox_DoubleRegisterPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	cfg := config.SingBoxConfig{Enabled: true, CollectInterval: 15 * time.Second}
	logger := slog.Default()

	_, err := NewSingBox(cfg, reg, logger)
	if err != nil {
		t.Fatalf("first NewSingBox: %v", err)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on double-register, got nil")
		}
	}()
	_, _ = NewSingBox(cfg, reg, logger)
}
