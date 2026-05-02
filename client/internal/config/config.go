package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Config defines runtime options for quicktunnel client agent.
type Config struct {
	ServerURL    string `json:"server_url"`
	APIKey       string `json:"api_key,omitempty"`
	NetworkID    string `json:"network_id"`
	DeviceName   string `json:"device_name"`
	VNCPort      int    `json:"vnc_port,omitempty"`
	LogLevel     string `json:"log_level"`
	WGListenPort int    `json:"wg_listen_port"`
	STUNServer   string `json:"stun_server"`

	HeartbeatIntervalSec       int `json:"heartbeat_interval_sec,omitempty"`
	PeerSyncIntervalSec        int `json:"peer_sync_interval_sec,omitempty"`
	EndpointRefreshIntervalSec int `json:"endpoint_refresh_interval_sec,omitempty"`
	QualityMonitorIntervalSec  int `json:"quality_monitor_interval_sec,omitempty"`

	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`

	// ZeroTier-style join — set by `quicktunnel join`, no API key needed
	MemberID     string `json:"member_id,omitempty"`
	MemberToken  string `json:"member_token,omitempty"`
	WGPrivateKey string `json:"wg_private_key,omitempty"`
	VirtualIP    string `json:"virtual_ip,omitempty"`
	NetworkCIDR  string `json:"network_cidr,omitempty"`
}

func defaultConfig() *Config {
	return &Config{
		ServerURL:    "http://localhost:8080",
		LogLevel:     "info",
		WGListenPort: 51820,
		STUNServer:   "stun.l.google.com:19302",
		HeartbeatIntervalSec:       30,
		PeerSyncIntervalSec:        15,
		EndpointRefreshIntervalSec: 60,
		QualityMonitorIntervalSec:  60,
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
	if runtime.GOOS == "windows" {
		programData := strings.TrimSpace(os.Getenv("ProgramData"))
		if programData == "" {
			programData = `C:\ProgramData`
		}
		return filepath.Join(programData, "QuickTunnel", "config.json"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config path: %w", err)
	}
	return filepath.Join(home, ".quicktunnel", "config.json"), nil
}

func applyEnvOverrides(cfg *Config) {
	if v := env("SERVER_URL"); v != "" { cfg.ServerURL = v }
	if v := env("API_KEY"); v != "" { cfg.APIKey = v }
	if v := env("NETWORK_ID"); v != "" { cfg.NetworkID = v }
	if v := env("DEVICE_NAME"); v != "" { cfg.DeviceName = v }
	if v := env("LOG_LEVEL"); v != "" { cfg.LogLevel = v }
	if v := env("STUN_SERVER"); v != "" { cfg.STUNServer = v }
	if v := env("WG_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil { cfg.WGListenPort = n }
	}
	if v := env("HEARTBEAT_INTERVAL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil { cfg.HeartbeatIntervalSec = n }
	}
	if v := env("PEER_SYNC_INTERVAL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil { cfg.PeerSyncIntervalSec = n }
	}
	if v := env("ENDPOINT_REFRESH_INTERVAL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil { cfg.EndpointRefreshIntervalSec = n }
	}
	if v := env("QUALITY_MONITOR_INTERVAL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil { cfg.QualityMonitorIntervalSec = n }
	}
}

func env(key string) string { return strings.TrimSpace(os.Getenv(key)) }
