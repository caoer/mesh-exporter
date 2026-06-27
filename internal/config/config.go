package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen   string          `yaml:"listen"`
	Collectors CollectorsConfig `yaml:"collectors"`
	Push     PushConfig      `yaml:"push"`
	Topology TopologyConfig  `yaml:"topology"`
}

type CollectorsConfig struct {
	EasyTier EasyTierConfig `yaml:"easytier"`
	SingBox  SingBoxConfig  `yaml:"singbox"`
}

type EasyTierConfig struct {
	Enabled         bool          `yaml:"enabled"`
	RPCAddress      string        `yaml:"rpc_address"`
	CLIPath         string        `yaml:"cli_path"`
	CollectInterval time.Duration `yaml:"collect_interval"`
}

type SingBoxConfig struct {
	Enabled         bool          `yaml:"enabled"`
	APIType         string        `yaml:"api_type"` // "grpc" or "clash"
	GRPCAddress     string        `yaml:"grpc_address"`
	ClashAddress    string        `yaml:"clash_address"`
	ClashSecret     string        `yaml:"clash_secret"`
	CollectInterval time.Duration `yaml:"collect_interval"`
}

type PushConfig struct {
	Enabled        bool          `yaml:"enabled"`
	RemoteWriteURL string        `yaml:"remote_write_url"`
	PushInterval   time.Duration `yaml:"push_interval"`
	InstanceLabel  string        `yaml:"instance_label"`
}

type TopologyConfig struct {
	LocalPeers []string `yaml:"local_peers"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		Listen: ":9550",
		Collectors: CollectorsConfig{
			EasyTier: EasyTierConfig{
				RPCAddress:      "127.0.0.1:15888",
				CLIPath:         "easytier-cli",
				CollectInterval: 15 * time.Second,
			},
			SingBox: SingBoxConfig{
				APIType:         "grpc",
				GRPCAddress:     "127.0.0.1:9191",
				ClashAddress:    "127.0.0.1:9090",
				CollectInterval: 15 * time.Second,
			},
		},
		Push: PushConfig{
			PushInterval: 30 * time.Second,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if !cfg.Collectors.EasyTier.Enabled && !cfg.Collectors.SingBox.Enabled {
		return nil, fmt.Errorf("at least one collector must be enabled")
	}

	return cfg, nil
}
