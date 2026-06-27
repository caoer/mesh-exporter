# mesh-exporter

Lightweight Prometheus exporter for EasyTier mesh networks and sing-box proxies.

Part of [locus](../../CLAUDE.md).

## Build

```bash
go build ./cmd/mesh-exporter
go test ./...
golangci-lint run
```

## Cross-compile (router)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o mesh-exporter-arm64 ./cmd/mesh-exporter
```

## Config

YAML config with independent collector toggles. See `internal/config/config.go` for schema.

Each collector (easytier, singbox) can be independently enabled/disabled.
Some hosts have only EasyTier, some have both.

## Architecture

- `internal/collector/easytier.go` - EasyTier peer/route/stats via `easytier-cli --output json`
- `internal/collector/singbox.go` - sing-box via native gRPC API or Clash API fallback
- `internal/server/server.go` - HTTP /metrics server
- `internal/push/remote_write.go` - Optional VictoriaMetrics remote-write push
- `nix/module.nix` - NixOS deployment module
