package push

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

// newTestRegistry returns a registry with one gauge pre-set.
func newTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "test_metric",
		Help: "A test metric.",
	})
	g.Set(42)
	reg.MustRegister(g)
	return reg
}

func TestPushClient_ValidExposition(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = body
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	reg := newTestRegistry(t)
	cfg := config.PushConfig{
		Enabled:        true,
		RemoteWriteURL: srv.URL + "/api/v1/import/prometheus",
		PushInterval:   50 * time.Millisecond,
		InstanceLabel:  "test-router",
	}

	pc, err := NewPushClient(reg, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	pc.Start()
	time.Sleep(150 * time.Millisecond)
	pc.Stop()

	mu.Lock()
	data := received
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("no data received by mock server")
	}

	// Parse the exposition format to verify validity.
	parser := &expfmt.TextParser{}
	mfs, err := parser.TextToMetricFamilies(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("invalid exposition format: %v", err)
	}
	if _, ok := mfs["test_metric"]; !ok {
		t.Error("test_metric not found in pushed data")
	}
}

func TestPushClient_InstanceLabel(t *testing.T) {
	var mu sync.Mutex
	var queryString string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queryString = r.URL.RawQuery
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	reg := newTestRegistry(t)
	cfg := config.PushConfig{
		Enabled:        true,
		RemoteWriteURL: srv.URL + "/api/v1/import/prometheus",
		PushInterval:   50 * time.Millisecond,
		InstanceLabel:  "my-router",
	}

	pc, err := NewPushClient(reg, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	pc.Start()
	time.Sleep(100 * time.Millisecond)
	pc.Stop()

	mu.Lock()
	qs := queryString
	mu.Unlock()

	if !strings.Contains(qs, "extra_label=instance=my-router") {
		t.Errorf("expected extra_label query param, got: %s", qs)
	}
}

func TestPushClient_InstanceLabelAutoDetect(t *testing.T) {
	cfg := config.PushConfig{
		Enabled:        true,
		RemoteWriteURL: "http://localhost:9999/api/v1/import/prometheus",
		PushInterval:   time.Second,
		InstanceLabel:  "", // auto-detect
	}
	pc, err := NewPushClient(prometheus.NewRegistry(), cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pc.url, "extra_label=instance=") {
		t.Errorf("expected auto-detected instance label in URL, got: %s", pc.url)
	}
}

func TestPushClient_RetryOnServerError(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	reg := newTestRegistry(t)
	cfg := config.PushConfig{
		Enabled:        true,
		RemoteWriteURL: srv.URL + "/api/v1/import/prometheus",
		PushInterval:   10 * time.Second, // long interval — we rely on the initial push
		InstanceLabel:  "retry-test",
	}

	pc, err := NewPushClient(reg, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	// Shorten backoff for test speed.
	origBackoff := initialBackoff
	_ = origBackoff // unused — constant, so we accept slower test

	pc.Start()
	// Wait enough for initial push + 2 retries (1s + 2s backoff) + final success.
	time.Sleep(5 * time.Second)
	pc.Stop()

	got := attempts.Load()
	if got < 3 {
		t.Errorf("expected at least 3 attempts (2 failures + 1 success), got %d", got)
	}
}

func TestPushClient_StopIsClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	reg := newTestRegistry(t)
	cfg := config.PushConfig{
		Enabled:        true,
		RemoteWriteURL: srv.URL + "/api/v1/import/prometheus",
		PushInterval:   50 * time.Millisecond,
		InstanceLabel:  "clean-stop",
	}

	pc, err := NewPushClient(reg, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	pc.Start()
	time.Sleep(100 * time.Millisecond)

	// Stop must return (goroutine joined). If it hangs, test times out.
	done := make(chan struct{})
	go func() {
		pc.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK — clean stop
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5s — goroutine leak")
	}
}

func TestPushClient_MissingURL(t *testing.T) {
	cfg := config.PushConfig{
		Enabled:       true,
		PushInterval:  time.Second,
		InstanceLabel: "x",
	}
	_, err := NewPushClient(prometheus.NewRegistry(), cfg, slog.Default())
	if err == nil {
		t.Fatal("expected error for missing remote_write_url")
	}
}
