package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// MetricsSnapshot is a serializable view of relay health and throughput.
type MetricsSnapshot struct {
	RelayName         string    `json:"relay_name"`
	ActiveSessions    int64     `json:"active_sessions"`
	TotalBytesRelayed uint64    `json:"total_bytes_relayed"`
	PacketsPerSecond  uint64    `json:"packets_per_second"`
	Timestamp         time.Time `json:"timestamp"`
}

// Metrics tracks health and traffic counters for a relay instance.
type Metrics struct {
	relayName string
	logger    zerolog.Logger

	activeSessions atomic.Int64
	totalBytes     atomic.Uint64
	packetCount    atomic.Uint64
	packetsPerSec  atomic.Uint64
}

func NewMetrics(relayName string, logger zerolog.Logger) *Metrics {
	return &Metrics{relayName: relayName, logger: logger}
}

// Start begins periodic packet-rate calculation.
func (m *Metrics) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		var last uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := m.packetCount.Load()
				if current >= last {
					m.packetsPerSec.Store(current - last)
				} else {
					m.packetsPerSec.Store(0)
				}
				last = current
			}
		}
	}()
}

// SetActiveSessions updates the gauge exposed in health responses.
func (m *Metrics) SetActiveSessions(count int) {
	m.activeSessions.Store(int64(count))
}

// RecordPacket increments packet and byte counters.
func (m *Metrics) RecordPacket(size int) {
	if size < 0 {
		size = 0
	}
	m.packetCount.Add(1)
	m.totalBytes.Add(uint64(size))
}

// Snapshot captures an immutable metrics sample.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		RelayName:         m.relayName,
		ActiveSessions:    m.activeSessions.Load(),
		TotalBytesRelayed: m.totalBytes.Load(),
		PacketsPerSecond:  m.packetsPerSec.Load(),
		Timestamp:         time.Now().UTC(),
	}
}

// HealthHandler exposes health state and metrics for load balancers.
func (m *Metrics) HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"metrics": m.Snapshot(),
	})
}

// StartReporter periodically posts relay metrics to coordination service.
func (m *Metrics) StartReporter(ctx context.Context, coordServerURL string, interval time.Duration, client *http.Client) {
	trimmed := strings.TrimSpace(coordServerURL)
	if trimmed == "" {
		m.logger.Debug().Msg("coord server url empty; metrics reporter disabled")
		return
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}

	reportURL := strings.TrimRight(trimmed, "/") + "/api/v1/relays/report"
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.reportOnce(ctx, client, reportURL); err != nil {
					m.logger.Warn().Err(err).Msg("failed to report relay metrics")
				}
			}
		}
	}()
}

func (m *Metrics) reportOnce(ctx context.Context, client *http.Client, reportURL string) error {
	payload, err := json.Marshal(m.Snapshot())
	if err != nil {
		return fmt.Errorf("report metrics: marshal snapshot: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reportURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("report metrics: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("report metrics: perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("report metrics: unexpected status %d", resp.StatusCode)
	}

	return nil
}
