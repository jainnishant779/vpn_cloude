package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"quicktunnel/server/internal/api"
	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/config"
	"quicktunnel/server/internal/database"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "run database migrations and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	zerolog.SetGlobalLevel(cfg.LogLevel)
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	ctx := context.Background()

	// ── Database ──────────────────────────────────────────────────────────────
	poolCfg := database.PoolConfig{
		MaxConns:        int32(cfg.DBMaxConns),
		MinConns:        int32(cfg.DBMinConns),
		MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime,
	}
	db, err := database.NewPostgresDB(cfg.DBURL, poolCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to postgres")
	}
	defer db.Close()

	// ── Redis ─────────────────────────────────────────────────────────────────
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse redis url")
	}
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Msg("failed to connect to redis")
	}

	// ── Migrations ────────────────────────────────────────────────────────────
	if err := db.Migrate(ctx); err != nil {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}
	if *migrateOnly {
		log.Info().Msg("migrations completed successfully")
		return
	}

	// ── JWT ───────────────────────────────────────────────────────────────────
	jwtService, err := auth.NewJWTService(cfg.JWTSecret)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize jwt service")
	}

	// ── Quick Connect Bootstrap (simple mode) ─────────────────────────────────
	var quickConnect *api.QuickConnectBootstrap
	if cfg.SimpleModeEnabled {
		quickConnect = &api.QuickConnectBootstrap{
			Enabled:     true,
			NetworkName: cfg.SimpleNetworkName,
			CIDR:        cfg.SimpleNetworkCIDR,
			MaxPeers:    cfg.SimpleNetworkMaxPeers,
		}
	}

	// ── Router ────────────────────────────────────────────────────────────────
	router := api.SetupRouter(
		db,
		redisClient,
		jwtService,
		quickConnect,
		cfg.AllowedOrigins,
		cfg.TrustedProxies,
	)

	// ── HTTP server ───────────────────────────────────────────────────────────
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.ServerPort),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errorsCh := make(chan error, 1)
	go func() {
		log.Info().
			Int("port", cfg.ServerPort).
			Str("stun_server", cfg.STUNServer).
			Msg("quicktunnel server starting")
		errorsCh <- server.ListenAndServe()
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	// ── Stale peer cleanup loop ───────────────────────────────────────────
	// Auto-expire peers that haven't sent a heartbeat in 90 seconds.
	// This implements the "auto-remove IP when disconnected" feature.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				const expireQuery = `UPDATE peers SET is_online = false WHERE is_online = true AND last_seen < NOW() - interval '90 seconds';`
				if _, err := db.Pool.Exec(context.Background(), expireQuery); err != nil {
					log.Warn().Err(err).Msg("stale peer cleanup failed")
				}
			case <-cleanupCtx.Done():
				return
			}
		}
	}()

	select {
	case sig := <-signalCh:
		log.Info().Str("signal", sig.String()).Msg("shutdown signal received")
	case err := <-errorsCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server stopped unexpectedly")
		}
	}

	cleanupCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("http server shutdown failed")
	}
	_ = redisClient.Close()
	db.Close()
	log.Info().Msg("quicktunnel server stopped")
}