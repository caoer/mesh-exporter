# mesh-exporter

Prometheus exporter for [EasyTier](https://github.com/EasyTier/EasyTier) mesh networks and [sing-box](https://github.com/SagerNet/sing-box) proxies.

## Why

EasyTier mesh networks silently degrade when same-LAN nodes route through a distant relay instead of connecting peer-to-peer. A sub-millisecond hop becomes 70ms+ with no visible error. Diagnosis requires SSH into each node and running `easytier-cli peer` manually. mesh-exporter runs per-node, exposes latency, loss, and connection type as Prometheus metrics, and makes relay-path detection a Grafana alert instead of a firefight.

## Quick Start

Build from source (requires Go 1.24+):

```bash
go install github.com/caoer/mesh-exporter/cmd/mesh-exporter@latest
```

Create a minimal config:

```yaml
listen: ":9550"
collectors:
  easytier:
    enabled: true
```

Run:

```bash
mesh-exporter -config config.yaml
```

Metrics at `http://localhost:9550/metrics`. Health check at `/health`.

## Configuration

```yaml
# HTTP listen address for /metrics endpoint
listen: ":9550"                          # default: ":9550"

collectors:
  easytier:
    enabled: false                       # default: false
    rpc_address: "127.0.0.1:15888"       # default: "127.0.0.1:15888"
    cli_path: "easytier-cli"             # default: "easytier-cli"
    collect_interval: 15s                # default: 15s

  singbox:
    enabled: false                       # default: false
    api_type: "clash"                    # "grpc" or "clash"; default: "grpc"
    grpc_address: "127.0.0.1:9191"       # default: "127.0.0.1:9191"
    clash_address: "127.0.0.1:9090"      # default: "127.0.0.1:9090"
    clash_secret: ""                     # Bearer token for Clash API
    collect_interval: 15s                # default: 15s

# Push mode for routers that can't be scraped (planned, not yet implemented)
push:
  enabled: false
  remote_write_url: "http://victoria:8428/api/v1/import/prometheus"
  push_interval: 30s                     # default: 30s
  instance_label: ""                     # auto-detected from hostname if empty

# Topology hints for suboptimal-routing alerts
topology:
  local_peers: []                        # hostnames/IPs expected to be LAN-local
```

At least one collector must be enabled. Each collector runs independently — enable EasyTier only, sing-box only, or both depending on the host.

The `api_type` option for sing-box selects the collection backend. The gRPC native API is not yet implemented; both `"grpc"` and `"clash"` currently use the Clash API.

## Metrics Reference

### EasyTier

Collected via `easytier-cli peer --output json` and `easytier-cli route --output json`.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `easytier_peer_latency_ms` | gauge | peer, hostname | RTT latency to peer in milliseconds |
| `easytier_peer_loss_rate` | gauge | peer, hostname | Packet loss rate to peer (0.0 - 1.0) |
| `easytier_peer_p2p` | gauge | peer, hostname | 1 if P2P connection, 0 if relay |
| `easytier_peer_rx_bytes_total` | gauge | peer, hostname | Total bytes received from peer |
| `easytier_peer_tx_bytes_total` | gauge | peer, hostname | Total bytes sent to peer |
| `easytier_route_path_length` | gauge | destination, hostname | Hop count to destination |
| `easytier_route_path_latency_ms` | gauge | destination, hostname | Path latency to destination in milliseconds |
| `easytier_peers_total` | gauge | | Total number of peers |
| `easytier_peers_p2p` | gauge | | Number of peers connected via P2P |
| `easytier_peers_relay` | gauge | | Number of peers connected via relay |
| `easytier_node_info` | gauge | hostname, ipv4, peer_id | Node info metric (always 1) |

### sing-box

Collected via the Clash API (`/connections` and `/proxies` endpoints).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `singbox_uplink_bytes_total` | counter | | Cumulative uplink bytes |
| `singbox_downlink_bytes_total` | counter | | Cumulative downlink bytes |
| `singbox_connections_in` | gauge | | Active inbound connections |
| `singbox_connections_out` | gauge | | Active outbound connections |
| `singbox_outbound_delay_ms` | gauge | outbound, type | Last URL test delay in milliseconds |
| `singbox_outbound_alive` | gauge | outbound, type | 1 if outbound responded to URL test, 0 otherwise |
| `singbox_memory_bytes` | gauge | | Memory usage in bytes |

## Push Mode

**Planned.** For router deployments (OpenWrt, aarch64) where Prometheus cannot scrape the node. Configure `push.remote_write_url` to POST metrics in Prometheus exposition format to a VictoriaMetrics endpoint. Not yet implemented.

## Deployment

### NixOS Module

Add the flake input and enable the module:

```nix
# flake.nix
{
  inputs.mesh-exporter.url = "github:caoer/mesh-exporter";
}
```

```nix
# configuration.nix
{ inputs, ... }:
{
  imports = [ inputs.mesh-exporter.nixosModules.default ];

  services.mesh-exporter = {
    enable = true;
    port = 9550;        # default
    openFirewall = false; # set true to allow scraping from remote Prometheus

    settings = {
      collectors = {
        easytier = {
          enabled = true;
          rpc_address = "127.0.0.1:15888";
        };
        singbox = {
          enabled = true;
          api_type = "clash";
          clash_address = "127.0.0.1:9090";
        };
      };
    };
  };
}
```

The module runs as a hardened systemd service with `DynamicUser`, `ProtectSystem=strict`, and automatic restart.

### Binary on OpenWrt / Router

Cross-compile a static arm64 binary:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o mesh-exporter-arm64 ./cmd/mesh-exporter
```

Or via Nix:

```bash
nix build .#arm64-static
```

Copy to the router and create a config file:

```bash
scp mesh-exporter-arm64 root@router:/usr/local/bin/mesh-exporter
scp config.yaml root@router:/etc/mesh-exporter/config.yaml
```

Run with procd or as a background process:

```bash
mesh-exporter -config /etc/mesh-exporter/config.yaml
```

## Building from Source

```bash
go build ./cmd/mesh-exporter
go test ./...
golangci-lint run
```

Cross-compile for arm64:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o mesh-exporter-arm64 ./cmd/mesh-exporter
```

Nix dev shell (includes Go 1.24, gopls, golangci-lint, delve, goreleaser):

```bash
nix develop
```

## License

MIT
