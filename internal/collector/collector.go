package collector

import (
	"log/slog"

	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// Collector is the interface all metric collectors implement.
type Collector interface {
	// Name returns a human-readable collector name.
	Name() string
	// Start begins background collection.
	Start() error
	// Stop halts collection and cleans up.
	Stop()
}

// BuildFromConfig creates enabled collectors based on config.
func BuildFromConfig(cfg *config.Config, reg *prometheus.Registry, logger *slog.Logger) ([]Collector, error) {
	var collectors []Collector

	if cfg.Collectors.EasyTier.Enabled {
		et, err := NewEasyTier(cfg.Collectors.EasyTier, reg, logger)
		if err != nil {
			return nil, err
		}
		instances := cfg.Collectors.EasyTier.ResolvedInstances()
		logger.Info("easytier collector configured",
			"instances", len(instances),
			"native_stats", cfg.Collectors.EasyTier.NativeStats)
		collectors = append(collectors, et)
	}

	if cfg.Collectors.SingBox.Enabled {
		sb, err := NewSingBox(cfg.Collectors.SingBox, reg, logger)
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, sb)
	}

	return collectors, nil
}
