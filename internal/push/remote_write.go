package push

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

// PushClient periodically gathers metrics from a prometheus.Registry
// and POSTs them to a VictoriaMetrics-compatible remote-write endpoint.
type PushClient struct {
	reg    *prometheus.Registry
	cfg    config.PushConfig
	logger *slog.Logger

	client *http.Client
	url    string // precomputed URL with extra_label param
	buf    bytes.Buffer

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

const (
	maxRetries     = 3
	initialBackoff = 1 * time.Second
	requestTimeout = 10 * time.Second
	contentType    = "text/plain; version=0.0.4; charset=utf-8"
)

// NewPushClient creates a push client. Call Start() to begin pushing.
func NewPushClient(reg *prometheus.Registry, cfg config.PushConfig, logger *slog.Logger) (*PushClient, error) {
	instance := cfg.InstanceLabel
	if instance == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("push: resolve instance label: %w", err)
		}
		instance = h
	}

	// VictoriaMetrics /api/v1/import/prometheus supports extra_label query param
	// to inject labels without mutating metric families in memory.
	u := cfg.RemoteWriteURL
	if u == "" {
		return nil, fmt.Errorf("push: remote_write_url is required when push is enabled")
	}
	sep := "?"
	for i := range u {
		if u[i] == '?' {
			sep = "&"
			break
		}
	}
	u += sep + "extra_label=instance=" + instance

	return &PushClient{
		reg:    reg,
		cfg:    cfg,
		logger: logger,
		client: &http.Client{Timeout: requestTimeout},
		url:    u,
	}, nil
}

// Start launches the background push goroutine.
func (p *PushClient) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(1)
	go p.loop(ctx)
	p.logger.Info("push client started", "url", p.url, "interval", p.cfg.PushInterval)
}

// Stop cancels the push goroutine and waits for it to finish.
func (p *PushClient) Stop() {
	p.cancel()
	p.wg.Wait()
	p.logger.Info("push client stopped")
}

func (p *PushClient) loop(ctx context.Context) {
	defer p.wg.Done()

	ticker := time.NewTicker(p.cfg.PushInterval)
	defer ticker.Stop()

	// Push once immediately on start.
	p.push(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.push(ctx)
		}
	}
}

func (p *PushClient) push(ctx context.Context) {
	// Gather metrics from registry.
	mfs, err := p.reg.Gather()
	if err != nil {
		p.logger.Warn("push: gather failed", "error", err)
		return
	}
	if len(mfs) == 0 {
		return
	}

	// Encode to exposition format, reusing buffer.
	p.buf.Reset()
	enc := expfmt.NewEncoder(&p.buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range mfs {
		if err := enc.Encode(mf); err != nil {
			p.logger.Warn("push: encode failed", "error", err)
			return
		}
	}

	body := p.buf.Bytes()

	// POST with retry + exponential backoff.
	backoff := initialBackoff
	for attempt := range maxRetries {
		if err := ctx.Err(); err != nil {
			return // shutting down
		}

		if err := p.post(ctx, body); err != nil {
			p.logger.Warn("push: attempt failed",
				"attempt", attempt+1,
				"max", maxRetries,
				"error", err,
				"next_backoff", backoff,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}
		return // success
	}
	p.logger.Warn("push: all retries exhausted")
}

func (p *PushClient) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup
	_, _ = io.Copy(io.Discard, resp.Body) // drain to reuse connection

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
