package push

// TODO: Implement VictoriaMetrics remote-write push client.
// This is for router deployments where the exporter can't be scraped.
// Uses the VictoriaMetrics import/prometheus API:
//   POST /api/v1/import/prometheus
// with Prometheus exposition format body.
