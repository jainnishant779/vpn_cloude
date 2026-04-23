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
	heartbeatInterval      = 30 * time.Second
	vncDiscoveryInterval   = 60 * time.Second
	qualityMonitorInterval = 90 * time.Second
	maxReconnectBackoff    = 2 * time.Minute
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
	memberID       string // ZeroTier-style join
	memberToken    string // ZeroTier-style join
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

func (a *Agent) OnStateChange(callback OnStateChange) { a.state.OnStateChange(callback) }
func (a *Agent) CurrentState() AgentState             { return a.state.Get() }

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

	// ── Decide auth mode ──────────────────────────────────────────────────────
	//
	// Mode A  (ZeroTier-style, no API key):
	//   Config has MemberToken + MemberID + WGPrivateKey set from the join flow.
	//   Use /api/v1/members/{mid}/heartbeat  and  /api/v1/members/{mid}/peers.
	//
	// Mode B  (classic, API key):
	//   Use /api/v1/networks/{id}/peers/register  +  heartbeat.
	//
	useMemberToken := strings.TrimSpace(a.config.MemberToken) != "" &&
		strings.TrimSpace(a.config.MemberID) != "" &&
		strings.TrimSpace(a.config.WGPrivateKey) != ""

	a.apiClient = api_client.NewClient(a.config.ServerURL, a.config.APIKey)

	var (
		privateKey  string
		virtualIP   string
		networkCIDR string
		peerID      string
	)

	if useMemberToken {
		// ── Mode A: ZeroTier-style ─────────────────────────────────────────
		log.Info().Msg("using member_token auth (ZeroTier-style)")
		a.apiClient.SetMemberToken(a.config.MemberToken)

		privateKey  = a.config.WGPrivateKey
		virtualIP   = a.config.VirtualIP
		networkCIDR = a.config.NetworkCIDR
		peerID      = a.config.MemberID

		a.mu.Lock()
		a.memberID    = a.config.MemberID
		a.memberToken = a.config.MemberToken
		a.mu.Unlock()

	} else {
		// ── Mode B: classic API key ────────────────────────────────────────
		if strings.TrimSpace(a.config.APIKey) == "" {
			if strings.TrimSpace(a.config.Email) == "" || strings.TrimSpace(a.config.Password) == "" {
				return fmt.Errorf("agent start: api_key or (email/password) is required\n" +
					"Tip: run  quicktunnel join <server> <network_id>  to join without an API key")
			}
			loginResp, err := a.apiClient.Login(a.config.Email, a.config.Password)
			if err != nil {
				return fmt.Errorf("agent start: login failed: %w", err)
			}
			a.config.APIKey = loginResp.APIKey
			a.apiClient = api_client.NewClient(a.config.ServerURL, a.config.APIKey)
		}

		a.state.Set(StateRegistering)
		var pubKey string
		var err error
		privateKey, pubKey, err = pkgcrypto.GenerateKeyPair()
		if err != nil {
			return fmt.Errorf("agent start: generate key pair: %w", err)
		}

		deviceName := strings.TrimSpace(a.config.DeviceName)
		if deviceName == "" {
			deviceName = "quicktunnel-device"
		}

		registerResp, err := a.apiClient.RegisterPeer(a.config.NetworkID, api_client.PeerRegisterRequest{
			MachineID: pkgcrypto.MachineFingerprint(),
			PublicKey: pubKey,
			Name:      deviceName,
			OS:        runtimeOS(),
			Version:   "0.1.0",
			VNCPort:   maxInt(a.config.VNCPort, 5900),
		})
		if err != nil {
			return fmt.Errorf("agent start: register peer: %w", err)
		}

		virtualIP   = registerResp.VirtualIP
		networkCIDR = registerResp.NetworkCIDR
		peerID      = registerResp.PeerID
	}

	a.mu.Lock()
	a.virtualIP = virtualIP
	a.peerID    = peerID
	a.mu.Unlock()

	// ── WireGuard tunnel ──────────────────────────────────────────────────────
	var err error
	a.tunnel, err = tunnel.NewWGTunnel(privateKey, virtualIP, networkCIDR, maxInt(a.config.WGListenPort, 51820))
	if err != nil {
		return fmt.Errorf("agent start: create tunnel: %w", err)
	}
	if err := a.tunnel.Start(); err != nil {
		return fmt.Errorf("agent start: start tunnel: %w", err)
	}

	a.holePuncher, err = nat.NewHolePuncher(0)
	if err != nil {
		return fmt.Errorf("agent start: create hole puncher: %w", err)
	}

	// ── Endpoint discovery ────────────────────────────────────────────────────
	a.state.Set(StateDiscovering)
	publicIP, publicPort, err := nat.DiscoverPublicEndpoint(a.config.STUNServer)
	if err != nil {
		return fmt.Errorf("agent start: discover public endpoint: %w", err)
	}
	endpoint := net.JoinHostPort(publicIP, strconv.Itoa(publicPort))

	a.mu.Lock()
	a.publicEndpoint = endpoint
	a.mu.Unlock()

	// ── Announce endpoint ─────────────────────────────────────────────────────
	if useMemberToken {
		if err := a.apiClient.MemberAnnounce(a.config.MemberID, api_client.MemberAnnounceRequest{
			PublicEndpoint: endpoint,
			LocalEndpoints: netutil.GetLocalIPs(),
		}); err != nil {
			log.Warn().Err(err).Msg("agent start: member announce failed (non-fatal)")
		}
	} else {
		if err := a.apiClient.Announce(api_client.AnnounceRequest{
			PeerID:         peerID,
			NetworkID:      a.config.NetworkID,
			PublicEndpoint: endpoint,
			LocalEndpoints: netutil.GetLocalIPs(),
		}); err != nil {
			log.Warn().Err(err).Msg("announce failed (non-fatal, tunnel will still start)")
		}
	}

	// ── VNC discovery ─────────────────────────────────────────────────────────
	if a.config.VNCPort > 0 {
		a.mu.Lock()
		a.vncPort      = a.config.VNCPort
		a.vncAvailable = isLocalPortOpen(a.config.VNCPort)
		a.mu.Unlock()
	} else {
		port, available := vnc.DiscoverVNCServer()
		a.mu.Lock()
		a.vncPort      = port
		a.vncAvailable = available
		a.mu.Unlock()
	}

	// ── Peer manager ──────────────────────────────────────────────────────────
	a.state.Set(StateConnecting)
	a.peerMgr = peer.NewPeerManager(a.tunnel, a.apiClient, a.holePuncher, a.config.NetworkID, peerID)
	if useMemberToken {
		a.peerMgr.SetMemberID(peerID)
	}
	a.peerMgr.Start()
	a.startPacketForwarding()

	a.wg.Add(3)
	go func() { defer a.wg.Done(); a.heartbeatLoop(useMemberToken) }()
	go func() { defer a.wg.Done(); a.vncDiscoveryLoop() }()
	go func() { defer a.wg.Done(); a.qualityMonitorLoop() }()

	a.state.Set(StateRunning)
	log.Info().
		Str("peer_id", peerID).
		Str("virtual_ip", virtualIP).
		Bool("zerotier_mode", useMemberToken).
		Msg("QuickTunnel running")
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

func (a *Agent) heartbeatLoop(useMemberToken bool) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			var err error
			if useMemberToken {
				err = a.sendMemberHeartbeat()
			} else {
				err = a.sendHeartbeat()
			}
			if err != nil {
				failures++
				a.state.Set(StateReconnecting)
				wait := time.Duration(1<<uint(minInt(failures, 6))) * time.Second
				if wait > maxReconnectBackoff {
					wait = maxReconnectBackoff
				}
				log.Warn().Err(err).Dur("backoff", wait).Msg("heartbeat failed")
				select {
				case <-time.After(wait):
				case <-a.ctx.Done():
					return
				}
				continue
			}
			if failures > 0 {
				failures = 0
				a.state.Set(StateRunning)
			}
		}
	}
}

func (a *Agent) sendMemberHeartbeat() error {
	a.mu.RLock()
	memberID := a.memberID
	endpoint := a.publicEndpoint
	vncAvail := a.vncAvailable
	a.mu.RUnlock()

	if memberID == "" {
		return fmt.Errorf("send member heartbeat: member_id empty")
	}

	// Refresh public endpoint
	if ip, port, err := nat.DiscoverPublicEndpoint(a.config.STUNServer); err == nil {
		fresh := net.JoinHostPort(ip, strconv.Itoa(port))
		if fresh != endpoint {
			a.mu.Lock()
			a.publicEndpoint = fresh
			a.mu.Unlock()
			endpoint = fresh
			_ = a.apiClient.MemberAnnounce(memberID, api_client.MemberAnnounceRequest{
				PublicEndpoint: fresh,
				LocalEndpoints: netutil.GetLocalIPs(),
			})
		}
	}

	stats := a.tunnel.GetStats()
	return a.apiClient.MemberHeartbeat(memberID, api_client.MemberHeartbeatRequest{
		PublicEndpoint: endpoint,
		LocalEndpoints: netutil.GetLocalIPs(),
		VNCAvailable:   vncAvail,
		RXBytes:        int64(stats.RXBytes),
		TXBytes:        int64(stats.TXBytes),
	})
}

func (a *Agent) sendHeartbeat() error {
	a.mu.RLock()
	peerID   := a.peerID
	endpoint := a.publicEndpoint
	vncAvail := a.vncAvailable
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
			_ = a.apiClient.Announce(api_client.AnnounceRequest{
				PeerID:         peerID,
				NetworkID:      a.config.NetworkID,
				PublicEndpoint: fresh,
				LocalEndpoints: netutil.GetLocalIPs(),
			})
		}
	}

	stats := a.tunnel.GetStats()
	return a.apiClient.Heartbeat(a.config.NetworkID, peerID, api_client.PeerStatus{
		PublicEndpoint: endpoint,
		LocalEndpoints: netutil.GetLocalIPs(),
		VNCAvailable:   vncAvail,
		RXBytes:        int64(stats.RXBytes),
		TXBytes:        int64(stats.TXBytes),
	})
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
				a.vncAvailable = isLocalPortOpen(a.config.VNCPort)
				a.mu.Unlock()
				continue
			}
			port, available := vnc.DiscoverVNCServer()
			a.mu.Lock()
			a.vncPort      = port
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
			for _, connection := range a.peerMgr.ListConnections() {
				latency := vnc.MeasureLatency(connection.VirtualIP)
				if connection.ConnectedVia == "p2p" && latency > 300*time.Millisecond {
					_ = a.peerMgr.ForceRelay(connection.PeerID)
				} else if connection.ConnectedVia == "relay" && probeCount%3 == 0 {
					_ = a.peerMgr.AttemptDirect(connection.PeerID)
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
		"state":           a.state.Get(),
		"peer_id":         a.peerID,
		"virtual_ip":      a.virtualIP,
		"public_endpoint": a.publicEndpoint,
		"vnc_port":        a.vncPort,
		"vnc_available":   a.vncAvailable,
		"connections":     connections,
	}
}

func (a *Agent) Connections() []peer.PeerConnection {
	if a.peerMgr == nil {
		return nil
	}
	return a.peerMgr.ListConnections()
}

func runtimeOS() string { return strings.ToLower(runtime.GOOS) }

func maxInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func minInt(a, b int) int {
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

func (a *Agent) startPacketForwarding() {
	conn := a.holePuncher.Conn()
	if conn == nil {
		return
	}
	// outbound: TUN → UDP
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		buf := make([]byte, 65536)
		magic := []byte{0x51, 0x54, 0x44, 0x54}
		for {
			select {
			case <-a.ctx.Done():
				return
			default:
			}
			n, err := a.tunnel.ReadPacket(buf)
			if err != nil || n < 20 {
				continue
			}
			destIP := net.IP(buf[16:20]).String()
			endpoint, ok := a.tunnel.FindEndpointByVirtualIP(destIP)
			if !ok {
				continue
			}
			peerAddr, err := net.ResolveUDPAddr("udp", endpoint)
			if err != nil {
				continue
			}
			pkt := make([]byte, 4+n)
			copy(pkt, magic)
			copy(pkt[4:], buf[:n])
			_, _ = conn.WriteToUDP(pkt, peerAddr)
		}
	}()
	// inbound: UDP → TUN
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		buf := make([]byte, 65536)
		magic := []byte{0x51, 0x54, 0x44, 0x54}
		for {
			select {
			case <-a.ctx.Done():
				return
			default:
			}
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil || n < 24 {
				continue
			}
			if buf[0] != magic[0] || buf[1] != magic[1] || buf[2] != magic[2] || buf[3] != magic[3] {
				continue
			}
			_, _ = a.tunnel.WritePacket(buf[4:n])
		}
	}()
}
