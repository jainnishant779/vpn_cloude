package api

import (
	"context"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// startTime records when the server process started.
var startTime = time.Now()

// unhealthyCount tracks consecutive health check failures for alerting.
var unhealthyCount atomic.Int64

// healthChecker can probe dependencies.
type healthChecker interface {
	pingPostgres(ctx context.Context) error
	pingRedis(ctx context.Context) error
	dbPoolStats() map[string]int32
}

// HealthResponse is the JSON body returned by GET /health.
type HealthResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	Uptime    string            `json:"uptime"`
	GoVersion string            `json:"go_version"`
	Checks    map[string]string `json:"checks"`
	Pool      map[string]int32  `json:"db_pool,omitempty"`
}

// healthHandler returns a 200 with rich diagnostics when all deps are healthy,
// or 503 with a partial payload if any dep is degraded.
func healthHandler(checker healthChecker, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		checks := make(map[string]string, 3)
		allOK := true

		// Check postgres
		if err := checker.pingPostgres(ctx); err != nil {
			checks["postgres"] = "degraded: " + err.Error()
			allOK = false
			log.Warn().Err(err).Msg("health: postgres ping failed")
		} else {
			checks["postgres"] = "ok"
		}

		// Check redis
		if err := checker.pingRedis(ctx); err != nil {
			checks["redis"] = "degraded: " + err.Error()
			allOK = false
			log.Warn().Err(err).Msg("health: redis ping failed")
		} else {
			checks["redis"] = "ok"
		}

		checks["server"] = "ok"

		status := "ok"
		code := http.StatusOK
		if !allOK {
			status = "degraded"
			code = http.StatusServiceUnavailable
			unhealthyCount.Add(1)
		} else {
			unhealthyCount.Store(0)
		}

		body := HealthResponse{
			Status:    status,
			Version:   version,
			Uptime:    time.Since(startTime).Round(time.Second).String(),
			GoVersion: runtime.Version(),
			Checks:    checks,
			Pool:      checker.dbPoolStats(),
		}

		writeSuccess(w, code, body)
	}
}
