package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"quicktunnel/server/internal/api/middleware"
	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/config"
	"quicktunnel/server/internal/database"
	"quicktunnel/server/internal/database/queries"
)

type rateLimiters struct {
	global *middleware.RateLimiter
	auth   *middleware.RateLimiter
	agent  *middleware.RateLimiter
}

func newRateLimiters() *rateLimiters {
	return &rateLimiters{
		global: middleware.NewRateLimiter(120, 200),
		auth:   middleware.NewRateLimiter(5, 10),
		agent:  middleware.NewRateLimiter(60, 120),
	}
}

func SetupRouter(db *database.DB, redisClient *redis.Client, authService *auth.JWTService, quickConnect *QuickConnectBootstrap, allowedOrigins []string, trustedProxies []string) *chi.Mux {
	rl := newRateLimiters()

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(middleware.RealIP(trustedProxies))
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.MaxBodySize(4 * 1024 * 1024))
	r.Use(middleware.CORS(allowedOrigins))
	r.Use(rl.global.Limit(middleware.RemoteIPKey))

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if !strings.HasPrefix(r.URL.Path, "/health") {
				log.Debug().
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Int("status", ww.Status()).
					Str("request_id", chimiddleware.GetReqID(r.Context())).
					Msg("request")
			}
		})
	})

	userStore    := queries.NewUserStore(db)
	networkStore := queries.NewNetworkStore(db)
	peerStore    := queries.NewPeerStore(db)
	relayStore   := queries.NewRelayStore(db)

	authHandler          := NewAuthHandler(userStore, authService)
	networkHandler       := NewNetworkHandler(networkStore, peerStore)
	peerHandler          := NewPeerHandler(networkStore, peerStore)
	coordHandler         := NewCoordHandler(redisClient, peerStore, relayStore)
	joinHandler          := NewJoinHandler(networkStore, peerStore)
	memberHandler        := NewMemberHandler(networkStore, peerStore)
	memberTunnelHandler  := NewMemberTunnelHandler(peerStore, redisClient)
	cfg, _               := config.Load()
	relayAssignHandler   := RelayAssignHandler(cfg)
	quickConnectHandler  := NewQuickConnectHandler(quickConnect)
	clientDownloadHandler := NewClientDownloadHandler()
	installHandler       := NewInstallScriptHandler(GetServerURL())

	apiKeyAuth := auth.NewAPIKeyAuth(func(ctx context.Context, apiKey string) (string, error) {
		user, err := userStore.GetUserByAPIKey(ctx, apiKey)
		if err != nil {
			if errors.Is(err, queries.ErrNotFound) {
				return "", err
			}
			return "", err
		}
		return user.ID, nil
	})

	// ── Install / ZeroTier-style join scripts ────────────────────────────────
	// curl http://<server>/join/<network_id> | sudo bash
	r.Get("/install.sh", installHandler.ServeScript)
	r.Head("/install.sh", installHandler.ServeScript)
	r.Get("/join/{network_id}", installHandler.ServeJoin)
	r.Get("/join/{network_id}/ps1", installHandler.ServeJoinPS1)

	// ── Health ───────────────────────────────────────────────────────────────
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		checks := map[string]string{"server": "ok"}
		allOK := true
		if err := db.Pool.Ping(ctx); err != nil {
			checks["postgres"] = "degraded: " + err.Error()
			allOK = false
		} else {
			checks["postgres"] = "ok"
		}
		if err := redisClient.Ping(ctx).Err(); err != nil {
			checks["redis"] = "degraded: " + err.Error()
			allOK = false
		} else {
			checks["redis"] = "ok"
		}
		code := http.StatusOK
		status := "ok"
		if !allOK {
			code = http.StatusServiceUnavailable
			status = "degraded"
		}
		s := db.PoolStats()
		writeSuccess(w, code, map[string]any{
			"status": status,
			"checks": checks,
			"db_pool": map[string]int32{
				"total": s.TotalConns, "idle": s.IdleConns,
				"acquired": s.AcquiredConns, "max": s.MaxConns,
			},
		})
	})

	r.Route("/api/v1", func(v1 chi.Router) {
		if quickConnect != nil && quickConnect.Enabled {
			v1.Get("/quick-connect", quickConnectHandler.Get)
		}
		v1.Get("/downloads/client/{os}/{arch}", clientDownloadHandler.Get)

		// ── ZeroTier-style join (no auth) ─────────────────────────────────
		v1.Group(func(joinRoutes chi.Router) {
			joinRoutes.Use(rl.auth.Limit(middleware.RemoteIPKey))
			joinRoutes.Post("/join", joinHandler.Join)
		})

		// ── Member-token tunnel endpoints (ZeroTier peers) ────────────────
		// Auth: Bearer <member_token>  (no API key needed)
		v1.Group(func(memberTunnel chi.Router) {
			memberTunnel.Use(rl.agent.Limit(middleware.RemoteIPKey))
			memberTunnel.Get("/members/{mid}/status", memberHandler.MemberStatus)
			memberTunnel.Put("/members/{mid}/heartbeat", memberTunnelHandler.Heartbeat)
			memberTunnel.Get("/members/{mid}/peers", memberTunnelHandler.Peers)
			memberTunnel.Post("/members/{mid}/announce", memberTunnelHandler.Announce)
		})

		// ── Auth ──────────────────────────────────────────────────────────
		v1.Group(func(authRoutes chi.Router) {
			authRoutes.Use(rl.auth.Limit(middleware.RemoteIPKey))
			authRoutes.Post("/auth/register", authHandler.Register)
			authRoutes.Post("/auth/login", authHandler.Login)
			authRoutes.Post("/auth/refresh", authHandler.Refresh)
		})

		// ── Dashboard (JWT) ───────────────────────────────────────────────
		v1.Group(func(userProtected chi.Router) {
			userProtected.Use(authService.AuthMiddleware)
			userProtected.Route("/networks", func(networks chi.Router) {
				networks.Post("/", networkHandler.CreateNetwork)
				networks.Get("/", networkHandler.ListNetworks)
				networks.Get("/{id}", networkHandler.GetNetwork)
				networks.Put("/{id}", networkHandler.UpdateNetwork)
				networks.Delete("/{id}", networkHandler.DeleteNetwork)
				networks.Get("/{id}/peers", peerHandler.ListPeers)
				networks.Get("/{id}/members", memberHandler.ListMembers)
				networks.Post("/{id}/members/{mid}/approve", memberHandler.ApproveMember)
				networks.Post("/{id}/members/{mid}/reject", memberHandler.RejectMember)
				networks.Delete("/{id}/members/{mid}", memberHandler.KickMember)
			})
		})

		// ── Agent / API-key endpoints ─────────────────────────────────────
		v1.Group(func(agentProtected chi.Router) {
			agentProtected.Use(apiKeyAuth.APIKeyMiddleware)
			agentProtected.Use(rl.agent.Limit(middleware.RemoteIPKey))
			agentProtected.Post("/networks/{id}/peers/register", peerHandler.RegisterPeer)
			agentProtected.Post("/networks/{id}/peers/unregister", peerHandler.UnregisterPeer)
			agentProtected.Put("/networks/{id}/peers/{peerId}/heartbeat", peerHandler.Heartbeat)
			agentProtected.Post("/coord/announce", coordHandler.Announce)
			agentProtected.Get("/coord/peers/{networkId}", coordHandler.ListPeers)
			agentProtected.Get("/coord/relay/assign", relayAssignHandler)
		})
	})

	return r
}