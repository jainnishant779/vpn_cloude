package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Config defines runtime settings loaded from environment variables.
type Config struct {
	DBURL                    string
	RedisURL                 string
	JWTSecret                string
	ServerPort               int
	STUNServer               string
	LogLevel                 zerolog.Level
	AllowedOrigins           []string
	TrustedProxies           []string
	DBMaxConns               int
	DBMinConns               int
	DBMaxConnLifetime        time.Duration
	DBMaxConnIdleTime        time.Duration
	SimpleModeEnabled        bool
	SimpleOwnerEmail         string
	SimpleOwnerName          string
	SimpleNetworkName        string
	SimpleNetworkCIDR        string
	SimpleNetworkDescription string
	SimpleNetworkMaxPeers    int
        RelaySessionSecret        string
}

// Load reads and validates configuration from environment variables.
func Load() (*Config, error) {
	loadDotEnvDefaults()

	serverPort, err := parsePort(getEnv("SERVER_PORT", "8080"))
	if err != nil {
		return nil, fmt.Errorf("load config: parse SERVER_PORT: %w", err)
	}

	level, err := zerolog.ParseLevel(strings.ToLower(getEnv("LOG_LEVEL", "info")))
	if err != nil {
		return nil, fmt.Errorf("load config: parse LOG_LEVEL: %w", err)
	}

	dbMaxConns, err := parsePositiveInt(getEnv("DB_MAX_CONNS", "20"))
	if err != nil {
		return nil, fmt.Errorf("load config: parse DB_MAX_CONNS: %w", err)
	}
	dbMinConns, err := parsePositiveInt(getEnv("DB_MIN_CONNS", "2"))
	if err != nil {
		return nil, fmt.Errorf("load config: parse DB_MIN_CONNS: %w", err)
	}
	dbMaxConnLifetime, err := parseDuration(getEnv("DB_MAX_CONN_LIFETIME", "30m"))
	if err != nil {
		return nil, fmt.Errorf("load config: parse DB_MAX_CONN_LIFETIME: %w", err)
	}
	dbMaxConnIdleTime, err := parseDuration(getEnv("DB_MAX_CONN_IDLE_TIME", "5m"))
	if err != nil {
		return nil, fmt.Errorf("load config: parse DB_MAX_CONN_IDLE_TIME: %w", err)
	}

	allowedOrigins := parseStringList(getEnv("ALLOWED_ORIGINS", ""))
	trustedProxies := parseStringList(getEnv("TRUSTED_PROXIES", "127.0.0.1,::1"))

	simpleModeEnabled, err := parseBool(getEnv("SIMPLE_MODE_ENABLED", "true"))
	if err != nil {
		return nil, fmt.Errorf("load config: parse SIMPLE_MODE_ENABLED: %w", err)
	}

	simpleNetworkMaxPeers, err := parsePositiveInt(getEnv("SIMPLE_NETWORK_MAX_PEERS", "25"))
	if err != nil {
		return nil, fmt.Errorf("load config: parse SIMPLE_NETWORK_MAX_PEERS: %w", err)
	}

	cfg := &Config{
		DBURL:                    strings.TrimSpace(os.Getenv("DB_URL")),
		RedisURL:                 strings.TrimSpace(getEnv("REDIS_URL", "redis://localhost:6379/0")),
		JWTSecret:                strings.TrimSpace(os.Getenv("JWT_SECRET")),
		ServerPort:               serverPort,
		STUNServer:               strings.TrimSpace(getEnv("STUN_SERVER", "stun.l.google.com:19302")),
		LogLevel:                 level,
		AllowedOrigins:           allowedOrigins,
		TrustedProxies:           trustedProxies,
		DBMaxConns:               dbMaxConns,
		DBMinConns:               dbMinConns,
		DBMaxConnLifetime:        dbMaxConnLifetime,
		DBMaxConnIdleTime:        dbMaxConnIdleTime,
		SimpleModeEnabled:        simpleModeEnabled,
		SimpleOwnerEmail:         strings.TrimSpace(getEnv("SIMPLE_OWNER_EMAIL", "quickconnect@quicktunnel.local")),
		SimpleOwnerName:          strings.TrimSpace(getEnv("SIMPLE_OWNER_NAME", "Quick Connect Owner")),
		SimpleNetworkName:        strings.TrimSpace(getEnv("SIMPLE_NETWORK_NAME", "Quick Connect Network")),
		SimpleNetworkCIDR:        strings.TrimSpace(getEnv("SIMPLE_NETWORK_CIDR", "10.7.0.0/16")),
		SimpleNetworkDescription: strings.TrimSpace(getEnv("SIMPLE_NETWORK_DESCRIPTION", "Auto-created network for quick connect flow")),
		SimpleNetworkMaxPeers:    simpleNetworkMaxPeers,
                RelaySessionSecret:        strings.TrimSpace(getEnv("RELAY_SESSION_SECRET", "")),
	}

	if cfg.DBURL == "" {
		return nil, fmt.Errorf("load config: DB_URL is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("load config: JWT_SECRET is required")
	}
	if cfg.SimpleOwnerEmail == "" {
		return nil, fmt.Errorf("load config: SIMPLE_OWNER_EMAIL is required")
	}
	if cfg.SimpleNetworkName == "" {
		return nil, fmt.Errorf("load config: SIMPLE_NETWORK_NAME is required")
	}
	if cfg.SimpleNetworkCIDR == "" {
		return nil, fmt.Errorf("load config: SIMPLE_NETWORK_CIDR is required")
	}

	return cfg, nil
}

func loadDotEnvDefaults() {
	loadDotEnvFile(".env")
	loadDotEnvFile("../.env")
}

func loadDotEnvFile(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		if strings.HasPrefix(entry, "export ") {
			entry = strings.TrimSpace(strings.TrimPrefix(entry, "export "))
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse port: %w", err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("parse port: out of range")
	}
	return port, nil
}

func parseBool(raw string) (bool, error) {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("parse bool: %w", err)
	}
	return value, nil
}

func parsePositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse int: %w", err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("parse int: must be positive")
	}
	return value, nil
}

func parseDuration(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse duration: %w", err)
	}
	return d, nil
}

// parseStringList splits a comma-separated string, trims whitespace, and
// discards empty entries.
func parseStringList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			result = append(result, v)
		}
	}
	return result
}
