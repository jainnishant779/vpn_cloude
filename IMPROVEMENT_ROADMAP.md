# QuickTunnel VPN - Detailed Improvement Roadmap

---

## Priority 1: Critical Security & Stability (1-2 Weeks)

### 1.1 Input Validation & Rate Limiting
**Current State**: ⚠️ Missing
**Impact**: High - Prevents abuse and DOS attacks

```go
// NEEDED in server/internal/api/middleware.go
type RateLimiter struct {
    limiter *limiter.Limiter
}

// Apply to:
// - POST /join (prevent spam registration)
// - POST /login (prevent brute force)
// - GET /peer/sync (prevent excessive polling)
```

**Effort**: 4 hours | **Value**: High

### 1.2 TLS/HTTPS for All Endpoints
**Current State**: ✅ Infrastructure ready, needs nginx config
**Impact**: High - Encrypts all control plane traffic

**Implementation**:
```nginx
# In nginx.conf
server {
    listen 443 ssl http2;
    ssl_certificate /etc/ssl/certs/fullchain.pem;
    ssl_certificate_key /etc/ssl/private/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    
    location / {
        proxy_pass http://localhost:8080;
    }
}
```

**Effort**: 3 hours | **Value**: High

### 1.3 Audit Logging
**Current State**: ⚠️ Basic logging, no audit trail
**Impact**: Medium - For compliance and troubleshooting

```go
// NEEDED in server/internal/database/audit.go
type AuditLog struct {
    ID        string    `db:"id"`
    Actor     string    `db:"actor"` // member_id or "system"
    Action    string    `db:"action"` // "member_joined", "peer_connected"
    Resource  string    `db:"resource"` // network_id, peer_id
    Status    string    `db:"status"` // "success", "failure"
    Details   string    `db:"details"` // JSON metadata
    CreatedAt time.Time `db:"created_at"`
}
```

**Effort**: 6 hours | **Value**: Medium

### 1.4 Error Handling Consistency
**Current State**: ⚠️ Inconsistent error responses
**Impact**: Medium - Better debugging and client experience

```go
// NEEDED in server/internal/api/errors.go
type APIError struct {
    Code      string `json:"code"`      // "INVALID_NETWORK", "MEMBER_PENDING"
    Message   string `json:"message"`   // Human-readable
    Details   string `json:"details"`   // Optional additional info
    RequestID string `json:"request_id"` // For debugging
}

// Usage in handlers
if err != nil {
    return c.JSON(400, &APIError{
        Code: "MEMBER_PENDING",
        Message: "Member is pending admin approval",
        RequestID: c.Request().Header.Get("X-Request-ID"),
    })
}
```

**Effort**: 5 hours | **Value**: Medium

---

## Priority 2: Observability & Monitoring (1-2 Weeks)

### 2.1 Prometheus Metrics
**Current State**: ⚠️ Missing
**Impact**: High - Essential for production

```go
// NEEDED in server/internal/metrics/metrics.go
var (
    httpRequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "quicktunnel_http_requests_total",
            Help: "Total HTTP requests",
        },
        []string{"method", "endpoint", "status"},
    )
    
    peersConnected = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "quicktunnel_peers_connected",
            Help: "Number of connected peers by network",
        },
        []string{"network_id"},
    )
    
    tunnelLatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "quicktunnel_tunnel_latency_ms",
            Help: "WireGuard tunnel handshake latency",
        },
        []string{"network_id"},
    )
)
```

**Metrics to Track**:
- HTTP requests (count, latency, errors)
- Active connections (members, peers, relays)
- WireGuard handshake latency
- Database query latency
- Redis operations

**Effort**: 8 hours | **Value**: High

### 2.2 Distributed Tracing
**Current State**: ⚠️ Missing
**Impact**: Medium - For troubleshooting

```go
// Add to all handlers using OpenTelemetry
ctx, span := tracer.Start(ctx, "handler_name")
defer span.End()

span.SetAttributes(
    attribute.String("member_id", memberID),
    attribute.String("network_id", networkID),
)
```

**Effort**: 6 hours | **Value**: Medium

### 2.3 Health Check Enhancements
**Current State**: ✅ Basic `/health` endpoint exists
**Impact**: Medium - Better monitoring

```go
// ENHANCE server/internal/api/health.go
type HealthResponse struct {
    Status        string `json:"status"` // "ok", "degraded", "error"
    Version       string `json:"version"`
    Uptime        int64  `json:"uptime_seconds"`
    Components    map[string]ComponentHealth `json:"components"`
}

type ComponentHealth struct {
    Status   string        `json:"status"`
    LastCheck time.Time    `json:"last_check"`
    Error    string        `json:"error,omitempty"`
}

// Check: database, redis, relay connectivity
```

**Effort**: 3 hours | **Value**: Medium

---

## Priority 3: Database & Data Management (1 Week)

### 3.1 Connection Pooling Optimization
**Current State**: ✅ Configured, needs tuning
**Impact**: Medium - Better performance under load

```go
// In .env
DB_MIN_CONNS=5
DB_MAX_CONNS=20
DB_IDLE_TIMEOUT=5m
DB_MAX_LIFETIME=30m
```

**Effort**: 2 hours | **Value**: Medium

### 3.2 Automated Database Cleanup
**Current State**: ⚠️ Missing
**Impact**: Medium - Prevents DB bloat

```go
// NEEDED in server/internal/database/cleanup.go
func (db *DB) CleanupExpiredSessions(ctx context.Context) error {
    query := "DELETE FROM sessions WHERE expires_at < NOW() AND accessed_at < NOW() - INTERVAL '7 days'"
    return db.Exec(ctx, query)
}

func (db *DB) CleanupOrphanedPeers(ctx context.Context) error {
    // Delete peers where member left the network 30 days ago
    query := `DELETE FROM peers 
             WHERE member_id NOT IN (SELECT id FROM members)
             AND updated_at < NOW() - INTERVAL '30 days'`
    return db.Exec(ctx, query)
}

// Run daily via cron/scheduler
```

**Effort**: 4 hours | **Value**: Medium

### 3.3 Database Indexing Optimization
**Current State**: ⚠️ Minimal indexes
**Impact**: Medium - Query performance

```sql
-- NEEDED indexes
CREATE INDEX idx_members_network_id ON members(network_id);
CREATE INDEX idx_peers_member_id ON peers(member_id);
CREATE INDEX idx_sessions_member_id ON sessions(member_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX idx_members_approved ON members(network_id, approved_at);
```

**Effort**: 2 hours | **Value**: High

---

## Priority 4: Client Improvements (1-2 Weeks)

### 4.1 Connection Health Monitoring
**Current State**: ⚠️ Missing
**Impact**: Medium - Auto-recovery

```go
// NEEDED in client/internal/agent/health.go
type HealthMonitor struct {
    checkInterval time.Duration
    failureThreshold int
}

func (hm *HealthMonitor) Run(ctx context.Context) {
    ticker := time.NewTicker(hm.checkInterval)
    defer ticker.Stop()
    
    for range ticker.C {
        if !hm.isHealthy() {
            failures++
            if failures >= hm.failureThreshold {
                // Reconnect tunnel
                hm.reconnectTunnel()
            }
        } else {
            failures = 0
        }
    }
}

// Check: Can ping assigned virtual IP? Can reach server?
```

**Effort**: 4 hours | **Value**: Medium

### 4.2 Better Error Messages & Logging
**Current State**: ⚠️ Basic errors
**Impact**: Low - UX improvement

```go
// ENHANCE in client/cmd/quicktunnel/main.go

// Better error context:
// Current: "connection refused"
// Better: "Failed to connect to server at 192.168.1.1:8080: connection refused. 
//          Check: 1) Server is running 2) Network has connectivity 3) Firewall allows port 8080"
```

**Effort**: 3 hours | **Value**: Low

### 4.3 Configuration Validation
**Current State**: ⚠️ Minimal
**Impact**: Low - UX improvement

```go
// NEEDED in client/internal/config/validate.go
func (c *Config) Validate() error {
    if c.ServerURL == "" {
        return fmt.Errorf("SERVER_URL not set")
    }
    
    if _, err := url.Parse(c.ServerURL); err != nil {
        return fmt.Errorf("invalid SERVER_URL: %w", err)
    }
    
    if c.NetworkID == "" && c.MemberToken == "" {
        return fmt.Errorf("must set NETWORK_ID or join a network first")
    }
    
    return nil
}
```

**Effort**: 2 hours | **Value**: Low

---

## Priority 5: Web Dashboard Enhancements (2-3 Weeks)

### 5.1 Network Topology Visualization
**Current State**: ⚠️ Missing
**Impact**: Medium - Better UX

```tsx
// NEEDED in web/src/components/NetworkTopology.tsx
// Show visual graph of:
// - Members as nodes
// - Connections as edges
// - Color: green (direct), yellow (relay), gray (pending)
// - Hover to see handshake info

import ReactFlow from 'reactflow';

export function NetworkTopology({ members, peers }) {
    const nodes = members.map(m => ({
        id: m.id,
        data: { label: m.name },
        position: { x: Math.random() * 500, y: Math.random() * 500 },
    }));
    
    const edges = peers.map(p => ({
        id: `${p.from}-${p.to}`,
        source: p.from,
        target: p.to,
        data: { relay: p.using_relay },
    }));
    
    return <ReactFlow nodes={nodes} edges={edges} />;
}
```

**Effort**: 8 hours | **Value**: Medium

### 5.2 Traffic Statistics Dashboard
**Current State**: ⚠️ Missing
**Impact**: Low - Analytics

```tsx
// Show per-member/peer statistics:
// - Data sent/received (MB)
// - Packet count
// - Connection uptime
// - Last handshake time
// - Latency to each peer
```

**Effort**: 6 hours | **Value**: Low

### 5.3 Client Download Page
**Current State**: ⚠️ Missing
**Impact**: Medium - Better onboarding

```tsx
// NEEDED in web/src/pages/Downloads.tsx
// Show:
// - Linux amd64, arm64 binaries
// - Windows amd64 binary
// - macOS amd64, arm64 binary
// - Installation instructions
// - One-liner installation commands
```

**Effort**: 4 hours | **Value**: Medium

---

## Priority 6: Advanced Networking (2-3 Weeks)

### 6.1 Network Policies / Firewall Rules
**Current State**: ❌ Not implemented
**Impact**: High - For multi-tenant security

```go
// NEEDED in server/internal/models/policy.go
type NetworkPolicy struct {
    ID          string
    NetworkID   string
    Name        string
    Rules       []PolicyRule // Allow/Deny between members
    CreatedBy   string
    CreatedAt   time.Time
}

type PolicyRule struct {
    FromMember  string // Member ID or "*" for all
    ToMember    string
    AllowedPorts []int // []int{443, 8080} or nil for all
    Protocol    string // "tcp", "udp", or "both"
    Action      string // "allow" or "deny"
}

// Evaluate in peer.ConnectToPeer():
// if !policyAllows(fromMember, toPeer) { return error }
```

**Effort**: 10 hours | **Value**: High

### 6.2 IPv6 Support
**Current State**: ❌ Not implemented
**Impact**: Medium - Future-proofing

**Changes needed**:
- Extend virtual network to IPv6 (fd00::/64)
- Update WireGuard configs to include IPv6
- Update routing for IPv6 on all platforms
- Test dual-stack connectivity

**Effort**: 8 hours | **Value**: Medium

### 6.3 VNC Server Discovery Enhancement
**Current State**: ✅ Partially implemented
**Impact**: Medium - Better VNC experience

```go
// ENHANCE in client/internal/vnc/discovery.go

// Currently: Only works on macOS via network scanning
// TODO: Implement for Linux:
// - Check for running vncserver processes
// - Parse ~/.vnc/config for port
// - Check /etc/vncserver.conf

// TODO: Implement for Windows:
// - Check VNC registry keys
// - Detect RDP service
// - Check for remote-desktop-enabled users
```

**Effort**: 4 hours | **Value**: Low

---

## Priority 7: Testing & Automation (2 Weeks)

### 7.1 Unit Tests
**Current State**: ⚠️ ~40% coverage
**Impact**: High - Code reliability

**Need tests for**:
- `server/internal/auth/` (JWT generation/validation)
- `client/internal/peer/` (connection logic)
- `pkg/protocol/` (message encoding)
- `pkg/crypto/` (key operations)

```bash
# Target: >80% coverage
go test ./... -v -cover
```

**Effort**: 12 hours | **Value**: High

### 7.2 Integration Tests
**Current State**: ⚠️ Basic e2e test exists
**Impact**: High - End-to-end validation

```go
// ENHANCE tests/e2e/full_flow_test.go

// Should test:
// 1. Server starts with clean DB
// 2. Create network via API
// 3. Client A joins, approval pending
// 4. Admin approves Client A
// 5. Client B joins and gets approved
// 6. Both clients can ping each other
// 7. Relay fallback works (disconnect direct)
// 8. Client disconnects, cleanup works
```

**Effort**: 8 hours | **Value**: High

### 7.3 Load Testing
**Current State**: ❌ Missing
**Impact**: Medium - Performance baseline

```bash
# NEEDED: Load test with k6 or locust
# Scenarios:
# - 50 clients joining simultaneously
# - Constant load of 10 req/sec to API
# - Measure: latency, success rate, server resource usage
```

**Effort**: 6 hours | **Value**: Medium

---

## Priority 8: Documentation & Operations (1 Week)

### 8.1 Operations Runbook
**Current State**: ⚠️ Basic setup docs exist
**Impact**: High - Production support

**Create**:
- Troubleshooting guide
- Common issues & solutions
- Scaling procedures
- Backup/restore procedures
- Monitoring & alerting setup
- On-call playbook

**Effort**: 6 hours | **Value**: High

### 8.2 API Versioning
**Current State**: ⚠️ No versioning
**Impact**: Medium - Future compatibility

```go
// ENHANCE API routes
// Current: /api/join, /api/peer/sync
// Better: /api/v1/join, /api/v1/peer/sync

// Benefits: Can add v2 later without breaking clients
```

**Effort**: 3 hours | **Value**: Medium

### 8.3 Client CLI Enhancements
**Current State**: ⚠️ Basic commands
**Impact**: Low - UX polish

**Add**:
- `quicktunnel config show` (show merged config)
- `quicktunnel debug logs` (tail logs)
- `quicktunnel benchmark` (test peer latency)
- `quicktunnel export` (export network data)

**Effort**: 4 hours | **Value**: Low

---

## Quick Implementation Priority List

**This Week (High Impact, Low Effort)**:
1. Add Prometheus metrics (8h) → **HIGH VALUE**
2. Implement rate limiting (4h) → **HIGH VALUE**
3. Add TLS/HTTPS config (3h) → **HIGH VALUE**
4. Database indexes (2h) → **HIGH VALUE**

**Next Week (Medium Priority)**:
1. Audit logging (6h)
2. Health check enhancements (3h)
3. Connection health monitoring client (4h)
4. Database cleanup jobs (4h)

**Following Week (Nice to Have)**:
1. Network policies (10h)
2. Dashboard topology visualization (8h)
3. Load testing (6h)

---

## Estimated Total Effort

| Priority | Effort | Value |
|----------|--------|-------|
| P1: Security & Stability | 18 hours | Critical |
| P2: Monitoring | 20 hours | Critical |
| P3: Database | 8 hours | High |
| P4: Client | 9 hours | Medium |
| P5: Dashboard | 18 hours | Medium |
| P6: Networking | 26 hours | High |
| P7: Testing | 26 hours | High |
| P8: Ops & Docs | 13 hours | High |
| **TOTAL** | **~138 hours** | — |

**Recommended for MVP Launch**: P1 + P2 + P7 (64 hours over 2 weeks)

---

**Last Updated**: May 1, 2026

