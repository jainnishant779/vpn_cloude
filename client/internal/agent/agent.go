package agent

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"quicktunnel/client/internal/api_client"
	"quicktunnel/client/internal/config"
	"quicktunnel/client/internal/nat"
	"quicktunnel/client/internal/peer"
	"quicktunnel/client/internal/tunnel"
	"quicktunnel/client/internal/vnc"
	pkgcrypto "quicktunnel.local/pkg/crypto"
	"quicktunnel.local/pkg/netutil"

	"github.com/rs/zerolog/log"
)

const (
	heartbeatInterval     = 30 * time.Second
	vncDiscoveryInterval  = 60 * time.Second
	qualityMonitorInterval = 90 * time.Second
	maxReconnectBackoff   = 2 * time.Minute
)

// Agent orchestrates tunnel, peer connectivity, and local VNC status.
type Agent struct {
	config      *config.Config
	apiClient   *api_client.Client
	tunnel      *tunnel.WGTunnel
	peerMgr     *peer.PeerManager
	holePuncher *nat.HolePuncher

	state *StateMachine

	mu             sync.RWMutex
	virtualIP      string
	peerID         string
	publicEndpoint string
	vncPort        int
	vncAvailable   bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewAgent(cfg *config.Config) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	return &Agent{
		config: cfg,
		state:  NewStateMachine(StateInit),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (a *Agent) OnStateChange(callback OnStateChange) {
	a.state.OnStateChange(callback)
}

func (a *Agent) CurrentState() AgentState {
	return a.state.Get()
}

func (a *Agent) Start() error {
	if a.config == nil {
		return fmt.Errorf("agent start: config is nil")
	}
	if strings.TrimSpace(a.config.ServerURL) == "" {
		return fmt.Errorf("agent start: server_url is required")
	}
	if strings.TrimSpace(a.config.NetworkID) == "" {
		return fmt.Errorf("agent start: network_id is required")
	}

	a.state.Set(StateAuthenticating)
	a.apiClient = api_client.NewClient(a.config.ServerURL, a.config.APIKey)

	if strings.TrimSpace(a.config.APIKey) == "" {
		if strings.TrimSpace(a.config.Email) == "" || strings.TrimSpace(a.config.Password) == "" {
			return fmt.Errorf("agent start: api_key or (email/password) is required")
		}
		loginResp, err := a.apiClient.Login(a.config.Email, a.config.Password)
		if err != nil {
			return fmt.Errorf("agent start: login failed: %w", err)
		}
		a.config.APIKey = loginResp.APIKey
	}

	a.state.Set(StateRegistering)
	privateKey, publicKey, err := pkgcrypto.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("agent start: generate key pair: %w", err)
	}

	deviceName := strings.TrimSpace(a.config.DeviceName)
	if deviceName == "" {
		deviceName = "quicktunnel-device"
	}

	registerResp, err := a.apiClient.RegisterPeer(a.config.NetworkID, api_client.PeerRegisterRequest{
		MachineID: pkgcrypto.MachineFingerprint(),
		PublicKey: publicKey,
		Name:      deviceName,
		OS:        runtimeOS(),
		Version:   "0.1.0",
		VNCPort:   maxInt(a.config.VNCPort, 5900),
	})
	if err != nil {
		return fmt.Errorf("agent start: register peer: %w", err)
	}

	a.mu.Lock()
	a.virtualIP = registerResp.VirtualIP
	a.peerID = registerResp.PeerID
	a.mu.Unlock()

	a.tunnel, err = tunnel.NewWGTunnel(privateKey, registerResp.VirtualIP, registerResp.NetworkCIDR, maxInt(a.config.WGListenPort, 51820))
	if err != nil {
		return fmt.Errorf("agent start: create tunnel: %w", err)
	}
	if err := a.tunnel.Start(); err != nil {
		return fmt.Errorf("agent start: start tunnel: %w", err)
	}

	a.holePuncher, err = nat.NewHolePuncher(maxInt(a.config.WGListenPort, 51820))
	if err != nil {
		return fmt.Errorf("agent start: create hole puncher: %w", err)
	}

	a.state.Set(StateDiscovering)
	publicIP, publicPort, err := nat.DiscoverPublicEndpoint(a.config.STUNServer)
	if err != nil {
		return fmt.Errorf("agent start: discover public endpoint: %w", err)
	}
	endpoint := net.JoinHostPort(publicIP, strconv.Itoa(publicPort))

	a.mu.Lock()
	a.publicEndpoint = endpoint
	a.mu.Unlock()

	if err := a.apiClient.Announce(api_client.AnnounceRequest{
		PeerID:         registerResp.PeerID,
		NetworkID:      a.config.NetworkID,
		PublicEndpoint: endpoint,
		LocalEndpoints: netutil.GetLocalIPs(),
	}); err != nil {
		return fmt.Errorf("agent start: announce endpoint: %w", err)
	}

	if a.config.VNCPort > 0 {
		a.mu.Lock()
		a.vncPort = a.config.VNCPort
		a.vncAvailable = isLocalPortOpen(a.config.VNCPort)
		a.mu.Unlock()
	} else {
		port, available := vnc.DiscoverVNCServer()
		a.mu.Lock()
		a.vncPort = port
		a.vncAvailable = available
		a.mu.Unlock()
	}

	a.state.Set(StateConnecting)
	a.peerMgr = peer.NewPeerManager(a.tunnel, a.apiClient, a.holePuncher, a.config.NetworkID, registerResp.PeerID)
	a.peerMgr.Start()

	a.wg.Add(3)
	go func() {
		defer a.wg.Done()
		a.heartbeatLoop()
	}()
	go func() {
		defer a.wg.Done()
		a.vncDiscoveryLoop()
	}()
	go func() {
		defer a.wg.Done()
		a.qualityMonitorLoop()
	}()

	a.state.Set(StateRunning)
	log.Info().
		Str("peer_id", registerResp.PeerID).
		Str("virtual_ip", registerResp.VirtualIP).
		Int("vnc_port", a.vncPort).
		Msg("QuickTunnel running. VNC accessible through tunnel")
	return nil
}

func (a *Agent) Stop() error {
	a.cancel()
	a.wg.Wait()

	var firstErr error
	if a.peerMgr != nil {
		if err := a.peerMgr.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("agent stop: stop peer manager: %w", err)
		}
	}
	if a.holePuncher != nil {
		if err := a.holePuncher.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("agent stop: close hole puncher: %w", err)
		}
	}
	if a.tunnel != nil {
		if err := a.tunnel.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("agent stop: stop tunnel: %w", err)
		}
	}

	a.state.Set(StateStopped)
	return firstErr
}

func (a *Agent) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	failures := 0

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if err := a.sendHeartbeat(); err != nil {
				failures++
				a.state.Set(StateReconnecting)

				wait := time.Duration(1<<uint(minInt(failures, 6))) * time.Second
				if wait > maxReconnectBackoff {
					wait = maxReconnectBackoff
				}
				log.Warn().Err(err).Dur("backoff", wait).Msg("heartbeat failed; entering reconnect backoff")

				select {
				case <-time.After(wait):
				case <-a.ctx.Done():
					return
				}
				continue
			}

			if failures > 0 {
				log.Info().Int("failures", failures).Msg("heartbeat recovered")
				failures = 0
				a.state.Set(StateRunning)
			}
		}
	}
}

func (a *Agent) sendHeartbeat() error {
	a.mu.RLock()
	peerID := a.peerID
	endpoint := a.publicEndpoint
	vncAvailable := a.vncAvailable
	a.mu.RUnlock()

	if peerID == "" {
		return fmt.Errorf("send heartbeat: peer id is empty")
	}

	if ip, port, err := nat.DiscoverPublicEndpoint(a.config.STUNServer); err == nil {
		fresh := net.JoinHostPort(ip, strconv.Itoa(port))
		if fresh != endpoint {
			a.mu.Lock()
			a.publicEndpoint = fresh
			a.mu.Unlock()
			endpoint = fresh

			if err := a.apiClient.Announce(api_client.AnnounceRequest{
				PeerID:         peerID,
				NetworkID:      a.config.NetworkID,
				PublicEndpoint: fresh,
				LocalEndpoints: netutil.GetLocalIPs(),
			}); err != nil {
				return fmt.Errorf("send heartbeat: re-announce endpoint: %w", err)
			}
		}
	}

	stats := a.tunnel.GetStats()
	if err := a.apiClient.Heartbeat(a.config.NetworkID, peerID, api_client.PeerStatus{
		PublicEndpoint: endpoint,
		LocalEndpoints: netutil.GetLocalIPs(),
		VNCAvailable:   vncAvailable,
		RXBytes:        int64(stats.RXBytes),
		TXBytes:        int64(stats.TXBytes),
	}); err != nil {
		return fmt.Errorf("send heartbeat: post heartbeat: %w", err)
	}

	return nil
}

func (a *Agent) vncDiscoveryLoop() {
	ticker := time.NewTicker(vncDiscoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if a.config.VNCPort > 0 {
				a.mu.Lock()
				a.vncPort = a.config.VNCPort
				a.vncAvailable = isLocalPortOpen(a.config.VNCPort)
				a.mu.Unlock()
				continue
			}

			port, available := vnc.DiscoverVNCServer()
			a.mu.Lock()
			a.vncPort = port
			a.vncAvailable = available
			a.mu.Unlock()
		}
	}
}

func (a *Agent) qualityMonitorLoop() {
	ticker := time.NewTicker(qualityMonitorInterval)
	defer ticker.Stop()

	probeCount := 0
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			probeCount++
			if a.peerMgr == nil {
				continue
			}

			connections := a.peerMgr.ListConnections()
			for _, connection := range connections {
				latency := vnc.MeasureLatency(connection.VirtualIP)
				if connection.ConnectedVia == "p2p" && latency > 300*time.Millisecond {
					if err := a.peerMgr.ForceRelay(connection.PeerID); err == nil {
						log.Warn().
							Str("peer_id", connection.PeerID).
							Dur("latency", latency).
							Msg("switched connection to relay due to degraded quality")
					}
					continue
				}

				if connection.ConnectedVia == "relay" && probeCount%3 == 0 {
					if err := a.peerMgr.AttemptDirect(connection.PeerID); err == nil {
						log.Info().
							Str("peer_id", connection.PeerID).
							Msg("switched relay connection back to direct p2p")
					}
				}
			}
		}
	}
}

func (a *Agent) Status() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()

	connections := []peer.PeerConnection{}
	if a.peerMgr != nil {
		connections = a.peerMgr.ListConnections()
	}

	return map[string]any{
		"state":          a.state.Get(),
		"peer_id":        a.peerID,
		"virtual_ip":     a.virtualIP,
		"public_endpoint": a.publicEndpoint,
		"vnc_port":       a.vncPort,
		"vnc_available":  a.vncAvailable,
		"connections":    connections,
	}
}

func (a *Agent) Connections() []peer.PeerConnection {
	if a.peerMgr == nil {
		return nil
	}
	return a.peerMgr.ListConnections()
}

func runtimeOS() string {
	return strings.ToLower(runtime.GOOS)
}

func maxInt(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func isLocalPortOpen(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
