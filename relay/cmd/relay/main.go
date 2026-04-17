package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	relay "quicktunnel/relay/internal"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Config struct {
	RelayPort      int
	HealthPort     int
	CoordServerURL string
	RelayName      string
	LogLevel       zerolog.Level
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	zerolog.SetGlobalLevel(cfg.LogLevel)
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metrics := relay.NewMetrics(cfg.RelayName, log.With().Str("component", "metrics").Logger())
	metrics.Start(ctx)
	metrics.StartReporter(ctx, cfg.CoordServerURL, 30*time.Second, &http.Client{Timeout: 5 * time.Second})

	relayServer := relay.NewRelayServer(cfg.RelayPort)
	relayServer.SetMetrics(metrics)

	relayErrCh := make(chan error, 1)
	go func() {
		relayErrCh <- relayServer.Start()
	}()

	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/health", metrics.HealthHandler)
	healthServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HealthPort),
		Handler:           healthMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	healthErrCh := make(chan error, 1)
	go func() {
		log.Info().
			Int("relay_port", cfg.RelayPort).
			Int("health_port", cfg.HealthPort).
			Str("relay_name", cfg.RelayName).
			Msg("relay services starting")
		healthErrCh <- healthServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-relayErrCh:
		if err != nil {
			log.Error().Err(err).Msg("relay server stopped unexpectedly")
		}
		stop()
	case err := <-healthErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("health server stopped unexpectedly")
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("failed to shut down health server")
	}
	if err := relayServer.Stop(); err != nil {
		log.Error().Err(err).Msg("failed to stop relay server")
	}

	log.Info().Msg("relay shutdown complete")
}

func loadConfig() (*Config, error) {
	relayPort, err := parsePort(getEnv("RELAY_PORT", "3478"))
	if err != nil {
		return nil, fmt.Errorf("load config: RELAY_PORT: %w", err)
	}
	healthPort, err := parsePort(getEnv("HEALTH_PORT", "8081"))
	if err != nil {
		return nil, fmt.Errorf("load config: HEALTH_PORT: %w", err)
	}

	level, err := zerolog.ParseLevel(strings.ToLower(getEnv("LOG_LEVEL", "info")))
	if err != nil {
		return nil, fmt.Errorf("load config: LOG_LEVEL: %w", err)
	}

	cfg := &Config{
		RelayPort:      relayPort,
		HealthPort:     healthPort,
		CoordServerURL: strings.TrimSpace(getEnv("COORD_SERVER_URL", "http://localhost:8080")),
		RelayName:      strings.TrimSpace(getEnv("RELAY_NAME", "relay-local")),
		LogLevel:       level,
	}
	if cfg.RelayName == "" {
		return nil, fmt.Errorf("load config: RELAY_NAME is required")
	}
	return cfg, nil
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
