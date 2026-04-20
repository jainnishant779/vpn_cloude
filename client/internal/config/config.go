package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config defines runtime options for quicktunnel client agent.
type Config struct {
	ServerURL    string `json:"server_url"`
	APIKey       string `json:"api_key,omitempty"`
	NetworkID    string `json:"network_id"`
	NetworkCIDR  string `json:"network_cidr,omitempty"`
	VirtualIP    string `json:"virtual_ip,omitempty"`
	DeviceName   string `json:"device_name"`
	VNCPort      int    `json:"vnc_port,omitempty"`
	LogLevel     string `json:"log_level"`
	WGListenPort int    `json:"wg_listen_port"`
	STUNServer   string `json:"stun_server"`

	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`

	// ZeroTier-style join — no API key required
	MemberID     string `json:"member_id,omitempty"`
	MemberToken  string `json:"member_token,omitempty"`
	WGPrivateKey string `json:"wg_private_key,omitempty"`
}

func defaultConfig() *Config {
	return &Config{
		ServerURL:    "http://localhost:8080",
		LogLevel:     "info",
		WGListenPort: 51820,
		STUNServer:   "stun.l.google.com:19302",
	}
}

func Load() (*Config, error) {
	cfg := defaultConfig()
	path, err := ConfigPath()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("load config: parse: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load config: read: %w", err)
	}
	applyEnvOverrides(cfg)
	return cfg, nil
}

func Save(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("save config: nil")
	}
	path, err := ConfigPath()
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("save config: mkdir: %w", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("save config: marshal: %w", err)
	}
	return os.WriteFile(path, b, 0o600)
}

func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config path: %w", err)
	}
	return filepath.Join(home, ".quicktunnel", "config.json"), nil
}

func applyEnvOverrides(cfg *Config) {
	if v := env("SERVER_URL"); v != "" {
		cfg.ServerURL = v
	}
	if v := env("API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := env("NETWORK_ID"); v != "" {
		cfg.NetworkID = v
	}
	if v := env("DEVICE_NAME"); v != "" {
		cfg.DeviceName = v
	}
	if v := env("LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := env("STUN_SERVER"); v != "" {
		cfg.STUNServer = v
	}
	if v := env("WG_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.WGListenPort = n
		}
	}
}

func env(key string) string { return strings.TrimSpace(os.Getenv(key)) }
