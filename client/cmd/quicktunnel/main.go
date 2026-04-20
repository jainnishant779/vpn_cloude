package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"quicktunnel/client/internal/agent"
	"quicktunnel/client/internal/api_client"
	"quicktunnel/client/internal/config"
	"quicktunnel/client/internal/vnc"
	pkgcrypto "quicktunnel.local/pkg/crypto"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := strings.ToLower(strings.TrimSpace(os.Args[1]))
	args := os.Args[2:]

	var err error
	switch command {
	case "join":
		err = runJoin(args)
	case "leave":
		err = runDown(args)
	case "up":
		err = runUp(args)
	case "down":
		err = runDown(args)
	case "status":
		err = runStatus(args)
	case "peers":
		err = runPeers(args)
	case "vnc":
		err = runVNC(args)
	case "login":
		err = runLogin(args)
	case "config":
		err = runConfig(args)
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		printUsage()
		err = fmt.Errorf("unknown command: %s", command)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// normalizeServerURL turns a bare IP or IP:port into a proper http:// URL.
// Examples:
//   54.89.232.16         → http://54.89.232.16:3000
//   54.89.232.16:8080    → http://54.89.232.16:8080
//   http://example.com   → http://example.com  (unchanged)
func normalizeServerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return strings.TrimRight(raw, "/")
	}
	// bare IP or IP:port
	if !strings.Contains(raw, ":") {
		raw = raw + ":3000"
	}
	return "http://" + raw
}

// ─── ZeroTier-style join ─────────────────────────────────────────────────────
//
// Usage:
//   quicktunnel join <server>  <network_id>
//   quicktunnel join 54.89.232.16 5agrlxob7exh
//   quicktunnel join http://54.89.232.16:3000 5agrlxob7exh
//
// No API key, no flags needed.
// Generates WireGuard keys, calls POST /api/v1/join, polls for approval,
// then starts the tunnel automatically.
func runJoin(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf(
			"Usage: quicktunnel join <server> <network_id>\n" +
			"  e.g. quicktunnel join 54.89.232.16 5agrlxob7exh\n\n" +
			"  server     — EC2 IP or URL (port 3000 is default)\n" +
			"  network_id — from the dashboard")
	}

	serverURL := normalizeServerURL(args[0])
	networkID := strings.TrimSpace(args[1])

	if networkID == "" {
		return fmt.Errorf("join: network_id cannot be empty")
	}

	fmt.Printf("Server  : %s\n", serverURL)
	fmt.Printf("Network : %s\n", networkID)

	// Generate WireGuard key pair
	fmt.Print("Generating WireGuard keys... ")
	privKey, pubKey, err := pkgcrypto.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("join: generate keys: %w", err)
	}
	fmt.Println("done")

	// Device name
	deviceName, _ := os.Hostname()
	if deviceName == "" {
		deviceName = "unknown-device"
	}

	// POST /api/v1/join  — NO auth required
	type joinReq struct {
		NetworkID   string `json:"network_id"`
		Hostname    string `json:"hostname"`
		WGPublicKey string `json:"wg_public_key"`
		OS          string `json:"os"`
		Arch        string `json:"arch"`
	}
	type joinResp struct {
		MemberID    string `json:"member_id"`
		MemberToken string `json:"member_token"`
		Status      string `json:"status"`
		VirtualIP   string `json:"virtual_ip"`
		NetworkCIDR string `json:"network_cidr"`
		NetworkName string `json:"network_name"`
		Message     string `json:"message"`
	}
	type envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}

	doJoinRequest := func() (*joinResp, error) {
		body, _ := json.Marshal(joinReq{
			NetworkID:   networkID,
			Hostname:    deviceName,
			WGPublicKey: pubKey,
			OS:          runtime.GOOS,
			Arch:        runtime.GOARCH,
		})
		resp, err := http.Post(serverURL+"/api/v1/join", "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("join: connect to server: %w", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("join: parse response: %w", err)
		}
		if !env.Success {
			return nil, fmt.Errorf("join: server error: %s", env.Error)
		}
		var jr joinResp
		if err := json.Unmarshal(env.Data, &jr); err != nil {
			return nil, fmt.Errorf("join: parse join data: %w", err)
		}
		return &jr, nil
	}

	fmt.Print("Requesting to join... ")
	jr, err := doJoinRequest()
	if err != nil {
		return err
	}
	fmt.Printf("status=%s\n", jr.Status)

	// If pending — poll until approved or rejected
	if jr.Status == "pending" {
		fmt.Printf("⏳ Waiting for admin to approve in dashboard (network: %s)...\n", jr.NetworkName)
		fmt.Println("   Press Ctrl+C to cancel.")

		type statusEnvelope struct {
			Success bool `json:"success"`
			Data    struct {
				Status    string `json:"status"`
				VirtualIP string `json:"virtual_ip"`
			} `json:"data"`
		}

		client := &http.Client{Timeout: 10 * time.Second}
		for {
			time.Sleep(5 * time.Second)
			req, _ := http.NewRequest("GET",
				fmt.Sprintf("%s/api/v1/members/%s/status", serverURL, jr.MemberID), nil)
			req.Header.Set("Authorization", "Bearer "+jr.MemberToken)
			r, err := client.Do(req)
			if err != nil {
				fmt.Printf("   (poll error: %v, retrying...)\n", err)
				continue
			}
			raw, _ := io.ReadAll(r.Body)
			r.Body.Close()

			var se statusEnvelope
			if json.Unmarshal(raw, &se) == nil && se.Success {
				switch se.Data.Status {
				case "approved":
					jr.Status = "approved"
					jr.VirtualIP = se.Data.VirtualIP
					fmt.Println("✓ Approved!")
					goto approved
				case "rejected":
					return fmt.Errorf("join: your device was rejected by the network admin")
				}
			}
			fmt.Print(".")
		}
	}

approved:
	if jr.Status != "approved" {
		return fmt.Errorf("join: unexpected status: %s", jr.Status)
	}

	fmt.Printf("✓ Virtual IP : %s\n", jr.VirtualIP)
	fmt.Printf("✓ Network    : %s (%s)\n", jr.NetworkName, jr.NetworkCIDR)

	// Save config
	cfg := &config.Config{
		ServerURL:    serverURL,
		NetworkID:    networkID,
		DeviceName:   deviceName,
		LogLevel:     "info",
		WGListenPort: 51820,
		STUNServer:   "stun.l.google.com:19302",
		MemberID:     jr.MemberID,
		MemberToken:  jr.MemberToken,
		WGPrivateKey: privKey,
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("join: save config: %w", err)
	}
	fmt.Println("✓ Config saved to ~/.quicktunnel/config.json")
	fmt.Println("\nStarting tunnel... (Ctrl+C to disconnect)")

	return startAgent(cfg)
}

func startAgent(cfg *config.Config) error {
	configureLogging(cfg.LogLevel)

	ag := agent.NewAgent(cfg)
	ag.OnStateChange(func(from agent.AgentState, to agent.AgentState) {
		log.Info().Str("from", string(from)).Str("to", string(to)).Msg("agent state changed")
	})

	if err := ag.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	defer ag.Stop()

	if err := writePIDFile(os.Getpid()); err != nil {
		log.Warn().Err(err).Msg("failed to write pid file")
	}
	defer removePIDFile()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	<-sigCh
	log.Info().Msg("shutdown signal received")
	if err := ag.Stop(); err != nil {
		return fmt.Errorf("stop agent: %w", err)
	}
	return nil
}

func runUp(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("up: does not accept arguments")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("up: load config: %w\n\nRun: quicktunnel join <server> <network_id>", err)
	}
	return startAgent(cfg)
}

func runDown(args []string) error {
	pid, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("down: find process: %w", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		_ = process.Kill()
	}
	removePIDFile()
	fmt.Printf("Disconnected (pid %d)\n", pid)
	return nil
}

func runStatus(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("status: load config: %w", err)
	}
	pid, pidErr := readPIDFile()
	out := map[string]any{
		"connected":   pidErr == nil,
		"pid":         pid,
		"server_url":  cfg.ServerURL,
		"network_id":  cfg.NetworkID,
		"device_name": cfg.DeviceName,
	}
	if cfg.APIKey != "" && cfg.NetworkID != "" {
		client := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
		if peers, err := client.GetPeers(cfg.NetworkID); err == nil {
			out["peer_count"] = len(peers)
		}
	}
	return printJSON(out)
}

func runPeers(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("peers: load config: %w", err)
	}
	if cfg.APIKey == "" || cfg.NetworkID == "" {
		return fmt.Errorf("peers: api_key and network_id required")
	}
	client := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
	peers, err := client.GetPeers(cfg.NetworkID)
	if err != nil {
		return fmt.Errorf("peers: %w", err)
	}
	return printJSON(peers)
}

func runVNC(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: quicktunnel vnc <peer-name>")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("vnc: load config: %w", err)
	}
	client := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
	peers, err := client.GetPeers(cfg.NetworkID)
	if err != nil {
		return fmt.Errorf("vnc: fetch peers: %w", err)
	}
	for _, p := range peers {
		if strings.EqualFold(p.Name, args[0]) {
			port := p.VNCPort
			if port == 0 {
				port = 5900
			}
			if err := vnc.LaunchVNCViewer(p.VirtualIP, port); err != nil {
				return fmt.Errorf("vnc: launch viewer: %w", err)
			}
			fmt.Printf("VNC → %s (%s:%d)\n", p.Name, p.VirtualIP, port)
			return nil
		}
	}
	return fmt.Errorf("vnc: peer not found: %s", args[0])
}

func runLogin(args []string) error {
	var email, password string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--email":
			email = args[i+1]
		case "--password":
			password = args[i+1]
		}
	}
	if email == "" || password == "" {
		return fmt.Errorf("login: --email and --password required")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("login: load config: %w", err)
	}
	client := api_client.NewClient(cfg.ServerURL, "")
	resp, err := client.Login(email, password)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	cfg.APIKey = resp.APIKey
	cfg.Email = email
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("login: save config: %w", err)
	}
	fmt.Printf("Logged in as %s\n", resp.User.Email)
	return nil
}

func runConfig(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: load: %w", err)
	}
	for _, item := range args {
		if strings.HasPrefix(item, "--set=") || strings.HasPrefix(item, "--set ") {
			item = strings.TrimPrefix(item, "--set=")
			item = strings.TrimPrefix(item, "--set ")
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k, v := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch strings.ToLower(k) {
		case "server_url":
			cfg.ServerURL = v
		case "api_key":
			cfg.APIKey = v
		case "network_id":
			cfg.NetworkID = v
		case "device_name":
			cfg.DeviceName = v
		case "log_level":
			cfg.LogLevel = v
		case "wg_listen_port":
			if n, err := strconv.Atoi(v); err == nil {
				cfg.WGListenPort = n
			}
		}
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("config: save: %w", err)
	}
	return printJSON(cfg)
}

func printUsage() {
	fmt.Println("QuickTunnel — ZeroTier-style VPN")
	fmt.Println("")
	fmt.Println("CONNECT (no binary needed — runs the one-liner first):")
	fmt.Println("  curl http://<server>/join/<network_id> | sudo bash")
	fmt.Println("")
	fmt.Println("OR if already installed:")
	fmt.Println("  quicktunnel join <server>  <network_id>")
	fmt.Println("  quicktunnel join 54.89.232.16 5agrlxob7exh")
	fmt.Println("")
	fmt.Println("COMMANDS:")
	fmt.Println("  join   <server> <network_id>  — connect to a network")
	fmt.Println("  leave  / down                 — disconnect")
	fmt.Println("  status                        — show connection info")
	fmt.Println("  peers                         — list network peers")
	fmt.Println("  up                            — reconnect (uses saved config)")
	fmt.Println("")
	fmt.Println("  server   = EC2 IP (port 3000 is default) or full URL")
	fmt.Println("  network  = ID from the dashboard")
}

func configureLogging(levelRaw string) {
	level, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(levelRaw)))
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func pidFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".quicktunnel", "quicktunnel.pid"), nil
}

func writePIDFile(pid int) error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0o644)
}

func readPIDFile() (int, error) {
	path, err := pidFilePath()
	if err != nil {
		return 0, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func removePIDFile() {
	if path, err := pidFilePath(); err == nil {
		_ = os.Remove(path)
	}
}

func init() {
	time.Local = time.UTC


}