package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	// Register a test gauge to have something in /metrics
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "test_gauge",
		Help: "A test gauge",
	})
	reg.MustRegister(g)
	g.Set(42)

	srv := New(":0", reg)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "test_gauge") {
		t.Error("response body missing test_gauge metric")
	}
	if !strings.Contains(string(body), "42") {
		t.Error("response body missing gauge value 42")
	}
}

func TestHealthEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := New(":0", reg)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("body = %q, want %q", strings.TrimSpace(string(body)), "ok")
	}
}

func TestRootEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := New(":0", reg)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "<html>") {
		t.Error("root should return HTML")
	}
	if !strings.Contains(s, "/metrics") {
		t.Error("root HTML should link to /metrics")
	}
	if !strings.Contains(s, "mesh-exporter") {
		t.Error("root HTML should mention mesh-exporter")
	}
}

func TestMetricsEndpoint_EmptyRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := New(":0", reg)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
