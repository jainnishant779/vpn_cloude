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
	APIKey       string `json:"api_key"`
	NetworkID    string `json:"network_id"`
	DeviceName   string `json:"device_name"`
	VNCPort      int    `json:"vnc_port"`
	LogLevel     string `json:"log_level"`
	WGListenPort int    `json:"wg_listen_port"`
	STUNServer   string `json:"stun_server"`

	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`
}

func defaultConfig() *Config {
	return &Config{
		ServerURL:    "http://localhost:8080",
		NetworkID:    "",
		DeviceName:   "",
		VNCPort:      0,
		LogLevel:     "info",
		WGListenPort: 51820,
		STUNServer:   "stun.l.google.com:19302",
	}
}

// Load reads config from ~/.quicktunnel/config.json and applies env overrides.
func Load() (*Config, error) {
	cfg := defaultConfig()

	path, err := ConfigPath()
	if err != nil {
		return nil, fmt.Errorf("load config: resolve config path: %w", err)
	}

	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("load config: parse json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load config: read file: %w", err)
	}

	applyEnvOverrides(cfg)
	return cfg, nil
}

// Save writes config to ~/.quicktunnel/config.json.
func Save(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("save config: cfg is nil")
	}

	path, err := ConfigPath()
	if err != nil {
		return fmt.Errorf("save config: resolve config path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("save config: create parent dir: %w", err)
	}

	serialized, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("save config: marshal json: %w", err)
	}
	if err := os.WriteFile(path, serialized, 0o600); err != nil {
		return fmt.Errorf("save config: write file: %w", err)
	}
	return nil
}

// ConfigPath returns ~/.quicktunnel/config.json path.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config path: resolve home dir: %w", err)
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
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.WGListenPort = parsed
		}
	}
	if v := env("VNC_PORT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.VNCPort = parsed
		}
	}
	if v := env("QT_EMAIL"); v != "" {
		cfg.Email = v
	}
	if v := env("QT_PASSWORD"); v != "" {
		cfg.Password = v
	}
}

func env(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
