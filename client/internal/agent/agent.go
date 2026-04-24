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
	memberID       string
	memberToken    string
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
	wgPort := maxInt(a.config.WGListenPort, 51820)
	var err error
<<<<<<< HEAD
=======
	wgPort := maxInt(a.config.WGListenPort, 51820)
>>>>>>> 91700f6385828200369dbdd7345eaad063a90c27
	a.tunnel, err = tunnel.NewWGTunnel(privateKey, virtualIP, networkCIDR, wgPort)
	if err != nil {
		return fmt.Errorf("agent start: create tunnel: %w", err)
	}
	if err := a.tunnel.Start(); err != nil {
		return fmt.Errorf("agent start: start tunnel: %w", err)
	}

	// ── Hole puncher (for relay fallback + NAT traversal) ────────────────────
	// Use port 0 (random) — WireGuard owns the real port
	a.holePuncher, err = nat.NewHolePuncher(0)
	if err != nil {
		// Non-fatal: relay fallback won't work but tunnel still functions
		log.Warn().Err(err).Msg("agent start: create hole puncher failed (non-fatal)")
	}

	// ── Endpoint discovery ────────────────────────────────────────────────────
	// CRITICAL FIX: Announce WireGuard's actual listen port, not the STUN socket port.
	// The STUN socket uses a random port — WireGuard listens on wgPort.
	// We discover the public IP via STUN but always pair it with wgPort.
	a.state.Set(StateDiscovering)
<<<<<<< HEAD
	publicIP, _, stunErr := nat.DiscoverPublicEndpoint(a.config.STUNServer)
	if stunErr != nil {
		// Fallback: try to get public IP from local interfaces
		publicIP = getOutboundIP()
		if publicIP == "" {
			log.Warn().Err(stunErr).Msg("agent start: STUN discovery failed — using loopback")
			publicIP = "127.0.0.1"
		}
	}
	// Use WireGuard's configured listen port so peers connect to the right port
=======
	publicIP, _, stunDiscoverErr := nat.DiscoverPublicEndpoint(a.config.STUNServer)
	if stunDiscoverErr != nil {
		publicIP = netutil.GetOutboundIP()
		if publicIP == "" {
			publicIP = "127.0.0.1"
		}
	}
	// CRITICAL: always use WireGuard listen port (51820), NOT the STUN socket port
>>>>>>> 74e04af0d3a2a2cc71dbc1789ea446fcce0660c4
	endpoint := net.JoinHostPort(publicIP, strconv.Itoa(wgPort))

	a.mu.Lock()
	a.publicEndpoint = endpoint
	a.mu.Unlock()

	log.Info().
		Str("public_endpoint", endpoint).
		Int("wg_port", wgPort).
		Bool("wg_ready", a.tunnel.IsWGReady()).
		Msg("endpoint discovered")

	// ── Announce endpoint ─────────────────────────────────────────────────────
	localIPs := netutil.GetLocalIPs()
	// Also add local IPs with WireGuard port for LAN direct connection
	localIPsWithPort := make([]string, 0, len(localIPs))
	for _, ip := range localIPs {
		host, _, err := net.SplitHostPort(ip)
		if err != nil {
			host = ip
		}
		localIPsWithPort = append(localIPsWithPort, net.JoinHostPort(host, strconv.Itoa(wgPort)))
	}

	if useMemberToken {
		if err := a.apiClient.MemberAnnounce(a.config.MemberID, api_client.MemberAnnounceRequest{
			PublicEndpoint: endpoint,
<<<<<<< HEAD
			LocalEndpoints: localIPsWithPort,
=======
			LocalEndpoints: localIPsWithPort(netutil.GetLocalIPs(), wgPort),
>>>>>>> 91700f6385828200369dbdd7345eaad063a90c27
		}); err != nil {
			log.Warn().Err(err).Msg("agent start: member announce failed (non-fatal)")
		}
	} else {
		if err := a.apiClient.Announce(api_client.AnnounceRequest{
			PeerID:         peerID,
			NetworkID:      a.config.NetworkID,
			PublicEndpoint: endpoint,
<<<<<<< HEAD
			LocalEndpoints: localIPsWithPort,
=======
			LocalEndpoints: localIPsWithPort(netutil.GetLocalIPs(), wgPort),
>>>>>>> 91700f6385828200369dbdd7345eaad063a90c27
		}); err != nil {
			log.Warn().Err(err).Msg("announce failed (non-fatal)")
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

	// CRITICAL FIX: Only start raw packet forwarding when WireGuard kernel/userspace
	// mode is NOT active. With real WireGuard, the kernel handles ALL packet I/O —
	// calling startPacketForwarding causes a CPU-spinning busy loop because
	// LinuxWGDevice.Read() returns (0, nil) and never blocks.
	if !a.tunnel.IsWGReady() {
		log.Warn().Msg("WireGuard not ready — using raw TUN packet forwarding (ICMP only, TCP/UDP limited)")
		a.startPacketForwarding()
	} else {
		log.Info().Msg("WireGuard kernel/userspace mode active — kernel handles packet forwarding")
	}

	a.wg.Add(3)
	go func() { defer a.wg.Done(); a.heartbeatLoop(useMemberToken) }()
	go func() { defer a.wg.Done(); a.vncDiscoveryLoop() }()
	go func() { defer a.wg.Done(); a.qualityMonitorLoop() }()

	a.state.Set(StateRunning)
	log.Info().
		Str("peer_id", peerID).
		Str("virtual_ip", virtualIP).
		Str("endpoint", endpoint).
		Bool("zerotier_mode", useMemberToken).
		Bool("wg_ready", a.tunnel.IsWGReady()).
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
				log.Warn().Err(err).Dur("backoff", wait).Int("failures", failures).Msg("heartbeat failed")
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
	memberID  := a.memberID
	endpoint  := a.publicEndpoint
	vncAvail  := a.vncAvailable
	wgPort    := maxInt(a.config.WGListenPort, 51820)
	a.mu.RUnlock()
	wgPort := maxInt(a.config.WGListenPort, 51820)
	wgPort := maxInt(a.config.WGListenPort, 51820)

	if memberID == "" {
		return fmt.Errorf("send member heartbeat: member_id empty")
	}

<<<<<<< HEAD
	// Re-discover public IP (handles IP changes) but keep WireGuard port
=======
	// Refresh public endpoint
>>>>>>> 74e04af0d3a2a2cc71dbc1789ea446fcce0660c4
	if ip, _, err := nat.DiscoverPublicEndpoint(a.config.STUNServer); err == nil {
		fresh := net.JoinHostPort(ip, strconv.Itoa(wgPort))
		if fresh != endpoint {
			a.mu.Lock()
			a.publicEndpoint = fresh
			a.mu.Unlock()
			endpoint = fresh
			_ = a.apiClient.MemberAnnounce(memberID, api_client.MemberAnnounceRequest{
				PublicEndpoint: fresh,
				LocalEndpoints: getLocalIPsWithPort(wgPort),
			})
		}
	}

	stats := a.tunnel.GetStats()
	return a.apiClient.MemberHeartbeat(memberID, api_client.MemberHeartbeatRequest{
		PublicEndpoint: endpoint,
		LocalEndpoints: getLocalIPsWithPort(wgPort),
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
	wgPort   := maxInt(a.config.WGListenPort, 51820)
	a.mu.RUnlock()
	wgPort := maxInt(a.config.WGListenPort, 51820)
	wgPort := maxInt(a.config.WGListenPort, 51820)

	if peerID == "" {
		return fmt.Errorf("send heartbeat: peer id is empty")
	}

	if ip, _, err := nat.DiscoverPublicEndpoint(a.config.STUNServer); err == nil {
		fresh := net.JoinHostPort(ip, strconv.Itoa(wgPort))
		if fresh != endpoint {
			a.mu.Lock()
			a.publicEndpoint = fresh
			a.mu.Unlock()
			endpoint = fresh
			_ = a.apiClient.Announce(api_client.AnnounceRequest{
				PeerID:         peerID,
				NetworkID:      a.config.NetworkID,
				PublicEndpoint: fresh,
				LocalEndpoints: getLocalIPsWithPort(wgPort),
			})
		}
	}

	stats := a.tunnel.GetStats()
	return a.apiClient.Heartbeat(a.config.NetworkID, peerID, api_client.PeerStatus{
		PublicEndpoint: endpoint,
		LocalEndpoints: getLocalIPsWithPort(wgPort),
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

// startPacketForwarding runs the raw TUN data plane.
// Only called when real WireGuard (kernel or userspace) is NOT available.
// With real WireGuard, the kernel handles all packet I/O — calling this
// would cause a 100% CPU busy loop.
func (a *Agent) startPacketForwarding() {
	if a.holePuncher == nil {
		return
	}
	conn := a.holePuncher.Conn()
	if conn == nil {
		return
	}
	magic := []byte{0x51, 0x54, 0x44, 0x54}

	// outbound: TUN → UDP
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		buf := make([]byte, 65536)
		for {
			select {
			case <-a.ctx.Done():
				return
			default:
			}
			n, err := a.tunnel.ReadPacket(buf)
			if err != nil || n < 20 {
				if n == 0 {
					// Avoid busy loop — sleep briefly when no data
					time.Sleep(1 * time.Millisecond)
				}
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
		"wg_ready":        a.tunnel != nil && a.tunnel.IsWGReady(),
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

// getOutboundIP returns the device's outbound IP by connecting to a public DNS.
func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

<<<<<<< HEAD
// getLocalIPsWithPort returns local IPs with the given port appended.
func getLocalIPsWithPort(port int) []string {
	raw := netutil.GetLocalIPs()
	result := make([]string, 0, len(raw))
	for _, ip := range raw {
=======
// localIPsWithPort appends the given port to each local IP.
// This ensures peers connect to the WireGuard port (51820), not a random port.
func localIPsWithPort(ips []string, port int) []string {
	result := make([]string, 0, len(ips))
	for _, ip := range ips {
>>>>>>> 91700f6385828200369dbdd7345eaad063a90c27
		host, _, err := net.SplitHostPort(ip)
		if err != nil {
			host = ip
		}
		result = append(result, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	return result
<<<<<<< HEAD
}
=======
}
>>>>>>> 91700f6385828200369dbdd7345eaad063a90c27
