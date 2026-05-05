package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
	case "leave", "down":
		err = runDown(args)
	case "up":
		err = runUp(args)
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
	case "install":
		err = runInstall(args)
	case "uninstall":
		err = runUninstall(args)
	case "reset":
		err = runReset(args)
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		printUsage()
		err = fmt.Errorf("unknown command: %s", command)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		os.Exit(1)
	}
}

// normalizeServerURL: bare IP → http://IP:3000
func normalizeServerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return strings.TrimRight(raw, "/")
	}
	if !strings.Contains(raw, ":") {
		raw = raw + ":3000"
	}
	return "http://" + raw
}

func runJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	nameFlag := fs.String("name", "", "Device name (optional)")
	installFlag := fs.Bool("install", false, "Install as system service after joining")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("Usage: quicktunnel join <server> <network_id>\n  e.g. quicktunnel join vpn.example.com 5agrlxob7exh")
	}

	serverURL := normalizeServerURL(rest[0])
	networkID := strings.TrimSpace(rest[1])

	fmt.Printf("Server  : %s\n", serverURL)
	fmt.Printf("Network : %s\n", networkID)

	deviceName := strings.TrimSpace(*nameFlag)
	if deviceName == "" {
		deviceName, _ = os.Hostname()
	}
	if deviceName == "" {
		deviceName = "unknown-device"
	}

	fmt.Print("Generating WireGuard keys... ")
	privKey, pubKey, err := pkgcrypto.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("join: generate keys: %w", err)
	}
	fmt.Println("done")

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

	doPost := func() (*joinResp, error) {
		body, err := json.Marshal(joinReq{
			NetworkID:   networkID,
			Hostname:    deviceName,
			WGPublicKey: pubKey,
			OS:          runtime.GOOS,
			Arch:        runtime.GOARCH,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Post(serverURL+"/api/v1/join", "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("cannot reach server: %w", err)
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		if !env.Success {
			return nil, fmt.Errorf("server: %s", env.Error)
		}
		var jr joinResp
		if err := json.Unmarshal(env.Data, &jr); err != nil {
			return nil, fmt.Errorf("parse join data: %w", err)
		}
		return &jr, nil
	}

	fmt.Print("Sending join request... ")
	jr, err := doPost()
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}
	fmt.Printf("status=%s\n", jr.Status)

	if jr.Status == "pending" {
		fmt.Printf("\n⏳ Waiting for admin to approve in dashboard (network: %s)\n", jr.NetworkName)
		fmt.Println("   Dashboard → Networks → Members → Approve")
		fmt.Println("   Press Ctrl+C to cancel.\n")

		type statusEnv struct {
			Success bool `json:"success"`
			Data    struct {
				Status    string `json:"status"`
				VirtualIP string `json:"virtual_ip"`
			} `json:"data"`
		}

		client := &http.Client{Timeout: 10 * time.Second}
		dots := 0
		for {
			time.Sleep(4 * time.Second)
			req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/members/%s/status", serverURL, jr.MemberID), nil)
			if err != nil {
				continue
			}
			req.Header.Set("Authorization", "Bearer "+jr.MemberToken)
			r, err := client.Do(req)
			if err != nil {
				fmt.Print("?")
				continue
			}
			
			raw, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil {
				continue
			}

			var se statusEnv
			if json.Unmarshal(raw, &se) == nil && se.Success {
				switch se.Data.Status {
				case "approved":
					jr.Status = "approved"
					jr.VirtualIP = se.Data.VirtualIP
					fmt.Println("\n✓ Approved!")
					goto approved
				case "rejected":
					fmt.Println()
					return fmt.Errorf("join: your device was rejected by the network admin")
				}
			}
			dots++
			if dots%15 == 0 {
				fmt.Printf(" (still waiting...)\n  ")
			} else {
				fmt.Print(".")
			}
		}
	}

approved:
	if jr.Status != "approved" {
		return fmt.Errorf("join: unexpected status: %s — %s", jr.Status, jr.Message)
	}

	fmt.Printf("\n✓ Virtual IP  : %s\n", jr.VirtualIP)
	fmt.Printf("✓ Network     : %s (%s)\n", jr.NetworkName, jr.NetworkCIDR)
	fmt.Printf("✓ Member ID   : %s\n", jr.MemberID)

	cfg := &config.Config{
		ServerURL:    serverURL,
		NetworkID:    networkID,
		DeviceName:   deviceName,
		LogLevel:     "info",
		WGListenPort: 51820,
		STUNServer:   "stun.l.google.com:19302",
		HeartbeatIntervalSec:       30,
		PeerSyncIntervalSec:        15,
		EndpointRefreshIntervalSec: 60,
		QualityMonitorIntervalSec:  60,
		MemberID:     jr.MemberID,
		MemberToken:  jr.MemberToken,
		WGPrivateKey: privKey,
		VirtualIP:    jr.VirtualIP,
		NetworkCIDR:  jr.NetworkCIDR,
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("join: save config: %w", err)
	}
	fmt.Println("✓ Config saved")

	if *installFlag {
		fmt.Println("\nInstalling as system service...")
		return runInstall(nil)
	}

	fmt.Println("\nStarting tunnel... (Ctrl+C to disconnect)\n")
	return startAgent(cfg)
}

func startAgent(cfg *config.Config) error {
	configureLogging(cfg.LogLevel)

	ag := agent.NewAgent(cfg)
	ag.OnStateChange(func(from agent.AgentState, to agent.AgentState) {
		switch to {
		case agent.StateRunning:
			fmt.Printf("✓ Tunnel UP  —  virtual IP: %s\n", cfg.VirtualIP)
		case agent.StateReconnecting:
			fmt.Println("⚠ Reconnecting...")
		}
		log.Info().Str("from", string(from)).Str("to", string(to)).Msg("state")
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
	fmt.Println("\nDisconnecting...")
	_ = ag.Stop()
	return nil
}

func runUp(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("up: load config: %w\n\nRun: quicktunnel join <server> <network_id>", err)
	}
	fmt.Printf("Reconnecting to %s / %s\n", cfg.ServerURL, cfg.NetworkID)
	return startAgent(cfg)
}

func runDown(args []string) error {
	pid, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("not connected (no pid file)")
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

func formatPeerAuthError(err error) string {
	if err == nil {
		return ""
	}
	var httpErr *api_client.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden {
			return "unauthorized (member_token invalid or config mismatch)"
		}
	}
	return err.Error()
}

func runStatus(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	pid, pidErr := readPIDFile()
	out := map[string]any{
		"connected":   pidErr == nil,
		"pid":         pid,
		"server":      cfg.ServerURL,
		"network_id":  cfg.NetworkID,
		"virtual_ip":  cfg.VirtualIP,
		"device_name": cfg.DeviceName,
		"mode":        func() string {
			if cfg.MemberToken != "" {
				return "zerotier"
			}
			return "classic"
		}(),
	}
	if cfg.MemberToken != "" {
		c := api_client.NewClient(cfg.ServerURL, "")
		c.SetMemberToken(cfg.MemberToken)
		if strings.TrimSpace(cfg.MemberID) == "" {
			out["peers_error"] = "member_id missing in config"
		} else if peers, err := c.MemberGetPeers(cfg.MemberID); err == nil {
			out["peers_online"] = len(peers)
		} else {
			out["peers_error"] = formatPeerAuthError(err)
		}
	} else if cfg.APIKey != "" {
		c := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
		if peers, err := c.GetPeers(cfg.NetworkID); err == nil {
			out["peers_online"] = len(peers)
		}
	}
	return printJSON(out)
}

func runPeers(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("peers: %w", err)
	}
	if cfg.MemberToken != "" {
		c := api_client.NewClient(cfg.ServerURL, "")
		c.SetMemberToken(cfg.MemberToken)
		peers, err := c.MemberGetPeers(cfg.MemberID)
		if err != nil {
			return fmt.Errorf("peers: %s", formatPeerAuthError(err))
		}
		return printJSON(peers)
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("peers: not connected — run 'quicktunnel join <server> <network_id>'")
	}
	c := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
	peers, err := c.GetPeers(cfg.NetworkID)
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
		return fmt.Errorf("vnc: %w", err)
	}
	var peers []api_client.PeerInfo
	if cfg.MemberToken != "" {
		c := api_client.NewClient(cfg.ServerURL, "")
		c.SetMemberToken(cfg.MemberToken)
		peers, err = c.MemberGetPeers(cfg.MemberID)
	} else {
		c := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
		peers, err = c.GetPeers(cfg.NetworkID)
	}
	if err != nil {
		return fmt.Errorf("vnc: %w", err)
	}
	for _, p := range peers {
		if strings.EqualFold(p.Name, args[0]) {
			port := p.VNCPort
			if port == 0 {
				port = 5900
			}
			if err := vnc.LaunchVNCViewer(p.VirtualIP, port); err != nil {
				return fmt.Errorf("vnc: launch: %w", err)
			}
			fmt.Printf("VNC → %s (%s:%d)\n", p.Name, p.VirtualIP, port)
			return nil
		}
	}
	return fmt.Errorf("vnc: peer not found: %s", args[0])
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	email := fs.String("email", "", "")
	password := fs.String("password", "", "")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("login: parse args: %w", err)
	}
	if *email == "" || *password == "" {
		return fmt.Errorf("login: --email and --password required")
	}
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{ServerURL: "http://localhost:3000"}
	}
	c := api_client.NewClient(cfg.ServerURL, "")
	resp, err := c.Login(*email, *password)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	cfg.APIKey = resp.APIKey
	cfg.Email = *email
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("login: save config: %w", err)
	}
	fmt.Printf("Logged in as %s\n", resp.User.Email)
	return nil
}

func runConfig(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	setVal := fs.String("set", "", "key=value")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("config: parse args: %w", err)
	}
	if *setVal != "" {
		parts := strings.SplitN(*setVal, "=", 2)
		if len(parts) == 2 {
			k, v := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			switch k {
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
			case "heartbeat_interval_sec":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.HeartbeatIntervalSec = n
				}
			case "peer_sync_interval_sec":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.PeerSyncIntervalSec = n
				}
			case "endpoint_refresh_interval_sec":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.EndpointRefreshIntervalSec = n
				}
			case "quality_monitor_interval_sec":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.QualityMonitorIntervalSec = n
				}
			}
		}
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("config: save config: %w", err)
		}
	}
	return printJSON(cfg)
}

func printUsage() {
	fmt.Println(`QuickTunnel — ZeroTier-style mesh VPN

CONNECT (no binary needed):
  curl http://<server>/join/<network_id> | sudo bash

CONNECT (if already installed):
  quicktunnel join <server> <network_id>
  quicktunnel join vpn.example.com 5agrlxob7exh

COMMANDS:
  join      <server> <network_id>  connect (generates keys, polls for approval)
  leave                            disconnect
  up                               reconnect using saved config
  status                           show connection info
  peers                            list network peers
  vnc       <peer-name>            open VNC to a peer
  install                          install as system startup service
  uninstall                        remove system startup service
  reset                            completely wipe config and uninstall service (to switch networks)

  server     = EC2 IP (default port 3000) or full URL
  network_id = from the dashboard`)
}

func runInstall(args []string) error {
	cfg, err := config.Load()
	if err != nil || cfg.NetworkID == "" {
		return fmt.Errorf("install: no config found — run 'quicktunnel join <server> <network_id>' first")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("install: cannot determine executable path: %w", err)
	}

	switch runtime.GOOS {
	case "linux":
		return installSystemdService(exePath)
	case "windows":
		return installWindowsService(exePath)
	default:
		return fmt.Errorf("install: unsupported OS %s (supported: linux, windows)", runtime.GOOS)
	}
}

func runUninstall(args []string) error {
	switch runtime.GOOS {
	case "linux":
		return uninstallSystemdService()
	case "windows":
		return uninstallWindowsService()
	default:
		return fmt.Errorf("uninstall: unsupported OS %s", runtime.GOOS)
	}
}

func installSystemdService(exePath string) error {
	serviceContent := fmt.Sprintf(`[Unit]
Description=QuickTunnel VPN Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s up
Restart=always
RestartSec=5
LimitNOFILE=65535
Environment=LOG_LEVEL=info

[Install]
WantedBy=multi-user.target
`, exePath)

	servicePath := "/etc/systemd/system/quicktunnel.service"
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0o644); err != nil {
		return fmt.Errorf("install: write service file: %w\n  Try running with sudo", err)
	}

	cmds := []struct {
		name string
		args []string
	}{
		{"systemctl", []string{"daemon-reload"}},
		{"systemctl", []string{"enable", "quicktunnel"}},
		{"systemctl", []string{"start", "quicktunnel"}},
	}
	for _, c := range cmds {
		if out, err := exec.Command(c.name, c.args...).CombinedOutput(); err != nil {
			return fmt.Errorf("install: %s %v: %w\n%s", c.name, c.args, err, string(out))
		}
	}

	fmt.Println("✓ QuickTunnel installed as systemd service")
	fmt.Println("  Service name : quicktunnel")
	fmt.Println("  Auto-start   : enabled")
	fmt.Println("  Check status : sudo systemctl status quicktunnel")
	fmt.Println("  View logs    : sudo journalctl -u quicktunnel -f")
	return nil
}

func uninstallSystemdService() error {
	cmds := []struct {
		name string
		args []string
	}{
		{"systemctl", []string{"stop", "quicktunnel"}},
		{"systemctl", []string{"disable", "quicktunnel"}},
	}
	for _, c := range cmds {
		_ = exec.Command(c.name, c.args...).Run()
	}
	_ = os.Remove("/etc/systemd/system/quicktunnel.service")
	_ = exec.Command("systemctl", "daemon-reload").Run()

	fmt.Println("✓ QuickTunnel service removed")
	return nil
}

func installWindowsService(exePath string) error {
	// Use schtasks to run the console app as SYSTEM on boot without needing SCM integration
	uninstallWindowsService()

	cmdStr := fmt.Sprintf("\"%s\" up", exePath)
	out, err := exec.Command("schtasks", "/create", "/tn", "QuickTunnel", "/tr", cmdStr, "/sc", "onstart", "/ru", "SYSTEM", "/rl", "HIGHEST", "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("install: schtasks create: %w\n%s\n  Try running as Administrator", err, string(out))
	}

	if startOut, err := exec.Command("schtasks", "/run", "/tn", "QuickTunnel").CombinedOutput(); err != nil {
		fmt.Printf("[WARN] Task created but failed to start now: %s\n", string(startOut))
	}

	fmt.Println("✓ QuickTunnel installed as Windows Startup Task")
	fmt.Println("  Task name  : QuickTunnel")
	fmt.Println("  Auto-start : enabled (on boot as SYSTEM)")
	fmt.Println("  To verify  : Task Scheduler -> QuickTunnel")
	return nil
}

func uninstallWindowsService() error {
	_ = runDown(nil) // Stop it first
	_ = exec.Command("schtasks", "/end", "/tn", "QuickTunnel").Run()
	time.Sleep(1 * time.Second)
	out, err := exec.Command("schtasks", "/delete", "/tn", "QuickTunnel", "/f").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "The system cannot find the file specified") {
		return fmt.Errorf("uninstall: schtasks delete: %w\n%s\n  Try running as Administrator", err, string(out))
	}
	fmt.Println("✓ QuickTunnel startup task removed")
	return nil
}



func runReset(args []string) error {
	fmt.Println("Resetting QuickTunnel configuration...")

	// 1. Stop if running
	_ = runDown(nil)

	// 2. Uninstall service based on OS
	switch runtime.GOOS {
	case "linux":
		_ = uninstallSystemdService()
	case "windows":
		_ = uninstallWindowsService()
	}

	// 3. Delete config file
	cfgPath, err := config.ConfigPath()
	if err == nil {
		if err := os.Remove(cfgPath); err == nil {
			fmt.Printf("✓ Removed configuration file (%s)\n", cfgPath)
		} else if !os.IsNotExist(err) {
			fmt.Printf("⚠ Failed to remove config file: %v\n", err)
		} else {
			fmt.Println("✓ No configuration file found")
		}
	}

	// 4. Optionally delete global config file in linux
	if runtime.GOOS == "linux" {
		if err := os.Remove("/etc/quicktunnel/config.json"); err == nil {
			fmt.Println("✓ Removed global configuration file (/etc/quicktunnel/config.json)")
		}
	}

	fmt.Println("\n✓ QuickTunnel has been completely reset.")
	fmt.Println("You can now join a new network: quicktunnel join <server> <network_id>")
	return nil
}

func configureLogging(levelRaw string) {
	level, _ := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(levelRaw)))
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

func pidFilePath() string {
	return filepath.Join(os.TempDir(), "quicktunnel.pid")
}

func writePIDFile(pid int) error {
	path := pidFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0o644)
}

func readPIDFile() (int, error) {
	path := pidFilePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func removePIDFile() {
	_ = os.Remove(pidFilePath())
}

func init() { time.Local = time.UTC }
