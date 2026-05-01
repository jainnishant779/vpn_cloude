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
	defaultHeartbeatInterval       = 30 * time.Second
	defaultPeerSyncInterval        = 15 * time.Second
	defaultVNCDiscoveryInterval    = 60 * time.Second
	defaultQualityMonitorInterval  = 60 * time.Second
	defaultEndpointRefreshInterval = 1 * time.Minute
	maxReconnectBackoff            = 2 * time.Minute
)

type Agent struct {
	config      *config.Config
	apiClient   *api_client.Client
	tunnel      *tunnel.WGTunnel
	peerMgr     *peer.PeerManager
	holePuncher *nat.HolePuncher

	state *StateMachine

	mu                  sync.RWMutex
	virtualIP           string
	peerID              string
	memberID            string
	memberToken         string
	publicEndpoint      string
	lastEndpointRefresh time.Time
	wgListenPort        int
	vncPort             int
	vncAvailable        bool

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
		log.Info().Msg("using member_token auth (zerotier-style)")
		a.apiClient.SetMemberToken(a.config.MemberToken)

		privateKey = a.config.WGPrivateKey
		virtualIP = a.config.VirtualIP
		networkCIDR = a.config.NetworkCIDR
		peerID = a.config.MemberID

		a.mu.Lock()
		a.memberID = a.config.MemberID
		a.memberToken = a.config.MemberToken
		a.mu.Unlock()
	} else {
		if strings.TrimSpace(a.config.APIKey) == "" {
			if strings.TrimSpace(a.config.Email) == "" || strings.TrimSpace(a.config.Password) == "" {
				return fmt.Errorf("agent start: api_key or (email/password) is required\nTip: run quicktunnel join <server> <network_id>")
			}
			loginResp, err := a.apiClient.Login(a.config.Email, a.config.Password)
			if err != nil {
				return fmt.Errorf("agent start: login failed: %w", err)
			}
			a.config.APIKey = loginResp.APIKey
			a.apiClient = api_client.NewClient(a.config.ServerURL, a.config.APIKey)
		}

		a.state.Set(StateRegistering)
		pubKey := ""
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

		virtualIP = registerResp.VirtualIP
		networkCIDR = registerResp.NetworkCIDR
		peerID = registerResp.PeerID
	}

	a.mu.Lock()
	a.virtualIP = virtualIP
	a.peerID = peerID
	a.mu.Unlock()

	wgPort := maxInt(a.config.WGListenPort, 51820)
	a.wgListenPort = wgPort

	var err error
	a.tunnel, err = tunnel.NewWGTunnel(privateKey, virtualIP, networkCIDR, wgPort)
	if err != nil {
		return fmt.Errorf("agent start: create tunnel: %w", err)
	}
	if err := a.tunnel.Start(); err != nil {
		return fmt.Errorf("agent start: start tunnel: %w", err)
	}

	a.holePuncher, err = nat.NewHolePuncher(0)
	if err != nil {
		log.Warn().Err(err).Msg("agent start: create hole puncher failed (non-fatal)")
	}

	a.state.Set(StateDiscovering)
	endpoint, _, epErr := a.refreshEndpointIfNeeded(true)
	if epErr != nil {
		log.Warn().Err(epErr).Msg("agent start: endpoint discovery failed, using relay-first mode")
		endpoint = ""
	}

	if endpoint != "" {
		log.Info().
			Str("public_endpoint", endpoint).
			Int("wg_port", wgPort).
			Bool("wg_ready", a.tunnel.IsWGReady()).
			Msg("endpoint discovered")
	} else {
		log.Warn().
			Int("wg_port", wgPort).
			Bool("wg_ready", a.tunnel.IsWGReady()).
			Msg("public endpoint unavailable, peers will use relay fallback")
	}

	localEndpoints := getLocalIPsWithPort(wgPort)
	log.Info().Strs("local_endpoints", localEndpoints).Msg("discovered local endpoints")

	if useMemberToken {
		// Announce to Redis (local_endpoints + public_endpoint)
		if err := a.apiClient.MemberAnnounce(a.config.MemberID, api_client.MemberAnnounceRequest{
			PublicEndpoint: endpoint,
			LocalEndpoints: localEndpoints,
		}); err != nil {
			log.Warn().Err(err).Msg("agent start: member announce failed (non-fatal)")
		}
		// Immediate heartbeat to populate DB with local_endpoints NOW.
		// Without this, DB has empty local_endpoints for 30s after join,
		// and peers that sync during that window can't discover our LAN IPs.
		_ = a.apiClient.MemberHeartbeat(a.config.MemberID, api_client.MemberHeartbeatRequest{
			PublicEndpoint: endpoint,
			LocalEndpoints: localEndpoints,
		})
	} else {
		if endpoint != "" {
			if err := a.apiClient.Announce(api_client.AnnounceRequest{
				PeerID:         peerID,
				NetworkID:      a.config.NetworkID,
				PublicEndpoint: endpoint,
				LocalEndpoints: localEndpoints,
			}); err != nil {
				log.Warn().Err(err).Msg("agent start: announce failed (non-fatal)")
			}
		}
		// Immediate heartbeat
		_ = a.apiClient.Heartbeat(a.config.NetworkID, peerID, api_client.PeerStatus{
			PublicEndpoint: endpoint,
			LocalEndpoints: localEndpoints,
		})
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
	a.peerMgr = peer.NewPeerManager(a.tunnel, a.apiClient, a.holePuncher, a.config.NetworkID, peerID)
	if useMemberToken {
		a.peerMgr.SetMemberID(peerID)
	}
	// Feed our public endpoint to PeerManager so samePublicNAT() can detect
	// peers behind the same NAT and prefer LAN endpoints over hairpin.
	if endpoint != "" {
		a.peerMgr.SetLocalPublicEndpoint(endpoint)
	}
	a.peerMgr.SetSyncInterval(a.peerSyncInterval())
	a.peerMgr.Start()

	if !a.tunnel.IsWGReady() {
		log.Warn().Msg("wireguard not ready, using raw tun packet forwarding")
		a.startPacketForwarding()
	} else {
		log.Info().Msg("wireguard mode active, kernel/userspace handles packet forwarding")
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
		Msg("quicktunnel running")
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
	ticker := time.NewTicker(a.heartbeatInterval())
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
	memberID := a.memberID
	endpoint := a.publicEndpoint
	vncAvail := a.vncAvailable
	a.mu.RUnlock()

	if memberID == "" {
		return fmt.Errorf("send member heartbeat: member_id empty")
	}

	fresh, _, err := a.refreshEndpointIfNeeded(false)
	if err != nil {
		log.Warn().Err(err).Msg("member heartbeat: endpoint refresh failed")
	} else if fresh != "" {
		endpoint = fresh
	}

	wgPort := a.currentWGPort()
	localEndpoints := getLocalIPsWithPort(wgPort)

	// Always re-announce to keep Redis data fresh (TTL=60s, heartbeat=30s).
	// Without this, Redis expires after initial announce and peers lose
	// each other's local_endpoints — breaking same-NAT LAN connectivity.
	_ = a.apiClient.MemberAnnounce(memberID, api_client.MemberAnnounceRequest{
		PublicEndpoint: endpoint,
		LocalEndpoints: localEndpoints,
	})

	// Keep PeerManager's public endpoint in sync for samePublicNAT detection
	if a.peerMgr != nil {
		a.peerMgr.SetLocalPublicEndpoint(endpoint)
	}

	stats := a.tunnel.GetStats()
	return a.apiClient.MemberHeartbeat(memberID, api_client.MemberHeartbeatRequest{
		PublicEndpoint: endpoint,
		LocalEndpoints: localEndpoints,
		VNCAvailable:   vncAvail,
		RXBytes:        int64(stats.RXBytes),
		TXBytes:        int64(stats.TXBytes),
	})
}

func (a *Agent) sendHeartbeat() error {
	a.mu.RLock()
	peerID := a.peerID
	endpoint := a.publicEndpoint
	vncAvail := a.vncAvailable
	a.mu.RUnlock()

	if peerID == "" {
		return fmt.Errorf("send heartbeat: peer id is empty")
	}

	fresh, _, err := a.refreshEndpointIfNeeded(false)
	if err != nil {
		log.Warn().Err(err).Msg("heartbeat: endpoint refresh failed")
	} else if fresh != "" {
		endpoint = fresh
	}

	wgPort := a.currentWGPort()
	localEndpoints := getLocalIPsWithPort(wgPort)

	// Always re-announce to keep Redis fresh (TTL=60s, heartbeat=30s)
	_ = a.apiClient.Announce(api_client.AnnounceRequest{
		PeerID:         peerID,
		NetworkID:      a.config.NetworkID,
		PublicEndpoint: endpoint,
		LocalEndpoints: localEndpoints,
	})

	if a.peerMgr != nil {
		a.peerMgr.SetLocalPublicEndpoint(endpoint)
	}

	stats := a.tunnel.GetStats()
	return a.apiClient.Heartbeat(a.config.NetworkID, peerID, api_client.PeerStatus{
		PublicEndpoint: endpoint,
		LocalEndpoints: localEndpoints,
		VNCAvailable:   vncAvail,
		RXBytes:        int64(stats.RXBytes),
		TXBytes:        int64(stats.TXBytes),
	})
}

func (a *Agent) vncDiscoveryLoop() {
	ticker := time.NewTicker(a.vncDiscoveryInterval())
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
			a.vncPort = port
			a.vncAvailable = available
			a.mu.Unlock()
		}
	}
}

func (a *Agent) qualityMonitorLoop() {
	ticker := time.NewTicker(a.qualityMonitorInterval())
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

func (a *Agent) startPacketForwarding() {
	if a.holePuncher == nil {
		return
	}
	conn := a.holePuncher.Conn()
	if conn == nil {
		return
	}
	magic := []byte{0x51, 0x54, 0x44, 0x54}

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

func (a *Agent) refreshEndpointIfNeeded(force bool) (string, bool, error) {
	a.mu.RLock()
	current := a.publicEndpoint
	last := a.lastEndpointRefresh
	a.mu.RUnlock()

	now := time.Now().UTC()
	if !force && current != "" && now.Sub(last) < a.endpointRefreshInterval() {
		return current, false, nil
	}

	ip, err := a.discoverPublicIP()
	if err != nil {
		if current != "" {
			return current, false, nil
		}
		return "", false, err
	}

	fresh := net.JoinHostPort(ip, strconv.Itoa(a.currentWGPort()))
	a.mu.Lock()
	changed := fresh != a.publicEndpoint
	a.publicEndpoint = fresh
	a.lastEndpointRefresh = now
	a.mu.Unlock()
	return fresh, changed, nil
}

func (a *Agent) discoverPublicIP() (string, error) {
	ip, _, err := nat.DiscoverPublicEndpoint(a.config.STUNServer)
	if err == nil {
		parsed := net.ParseIP(strings.TrimSpace(ip))
		if isRoutableIPv4(parsed) {
			return parsed.String(), nil
		}
	}

	fallback := getOutboundIP()
	fallbackIP := net.ParseIP(strings.TrimSpace(fallback))
	if isRoutableIPv4(fallbackIP) {
		return fallbackIP.String(), nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("no routable public endpoint available")
}

func (a *Agent) currentWGPort() int {
	a.mu.RLock()
	port := a.wgListenPort
	a.mu.RUnlock()
	if port > 0 {
		return port
	}
	return maxInt(a.config.WGListenPort, 51820)
}

func (a *Agent) heartbeatInterval() time.Duration {
	return boundedSeconds(a.config.HeartbeatIntervalSec, defaultHeartbeatInterval, 10, 600)
}

func (a *Agent) peerSyncInterval() time.Duration {
	return boundedSeconds(a.config.PeerSyncIntervalSec, defaultPeerSyncInterval, 10, 600)
}

func (a *Agent) vncDiscoveryInterval() time.Duration {
	return defaultVNCDiscoveryInterval
}

func (a *Agent) qualityMonitorInterval() time.Duration {
	return boundedSeconds(a.config.QualityMonitorIntervalSec, defaultQualityMonitorInterval, 30, 1800)
}

func (a *Agent) endpointRefreshInterval() time.Duration {
	return boundedSeconds(a.config.EndpointRefreshIntervalSec, defaultEndpointRefreshInterval, 60, 3600)
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

func boundedSeconds(value int, fallback time.Duration, minSec, maxSec int) time.Duration {
	if value <= 0 {
		return fallback
	}
	if value < minSec {
		value = minSec
	}
	if value > maxSec {
		value = maxSec
	}
	return time.Duration(value) * time.Second
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

func isRoutableIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.IsPrivate() {
		return false
	}
	// Link-local 169.254.0.0/16
	if v4[0] == 169 && v4[1] == 254 {
		return false
	}
	// Carrier-grade NAT 100.64.0.0/10
	if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

func getLocalIPsWithPort(port int) []string {
	raw := netutil.GetLocalIPs()
	result := make([]string, 0, len(raw))
	for _, ip := range raw {
		host, _, err := net.SplitHostPort(ip)
		if err != nil {
			host = ip
		}
		result = append(result, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	return result
}
