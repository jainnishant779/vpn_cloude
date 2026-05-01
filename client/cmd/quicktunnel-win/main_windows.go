//go:build windows
// +build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"quicktunnel/client/internal/agent"
	"quicktunnel/client/internal/config"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	appName    = "QuickTunnel"
	appVersion = "1.0.0"
	configDir  = `C:\ProgramData\QuickTunnel`
	configFile = `C:\ProgramData\QuickTunnel\config.json`
	logFile    = `C:\ProgramData\QuickTunnel\quicktunnel.log`
	serviceName = "QuickTunnelSvc"
)

func main() {
	// Setup logging to file + console
	setupLogging()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch strings.ToLower(os.Args[1]) {
	case "join":
		if len(os.Args) < 4 {
			fmt.Println("Usage: quicktunnel-win.exe join <server> <network_id>")
			os.Exit(1)
		}
		joinNetwork(os.Args[2], os.Args[3])

	case "leave":
		leaveNetwork()

	case "status":
		showStatus()

	case "start":
		startDaemon()

	case "install":
		installService()

	case "uninstall":
		uninstallService()

	case "version":
		fmt.Printf("%s v%s (Windows)\n", appName, appVersion)

	default:
		// If first arg looks like a server URL, treat as: join <server> <network>
		if len(os.Args) >= 3 && (strings.HasPrefix(os.Args[1], "http") || strings.Contains(os.Args[1], ".")) {
			joinNetwork(os.Args[1], os.Args[2])
		} else {
			printUsage()
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Printf(`
%s v%s — ZeroTier-style VPN for Windows

USAGE:
  quicktunnel-win.exe join <server> <network_id>   Join a network
  quicktunnel-win.exe leave                         Leave current network
  quicktunnel-win.exe status                        Show connection status
  quicktunnel-win.exe start                         Start daemon (foreground)
  quicktunnel-win.exe install                       Install as Windows service
  quicktunnel-win.exe uninstall                     Remove Windows service
  quicktunnel-win.exe version                       Show version

EXAMPLES:
  quicktunnel-win.exe join <server-ip> <network-id>
  quicktunnel-win.exe status

`, appName, appVersion)
}

func joinNetwork(server, networkID string) {
	fmt.Printf("\n  %s — Joining Network\n", appName)
	fmt.Printf("  ══════════════════════════════════════\n")
	fmt.Printf("  Server  : %s\n", server)
	fmt.Printf("  Network : %s\n", networkID)
	fmt.Println()

	// Ensure config directory exists
	_ = os.MkdirAll(configDir, 0755)

	// Build server URL
	serverURL := server
	if !strings.HasPrefix(serverURL, "http") {
		serverURL = "http://" + serverURL + ":3000"
	}

	// Join network via API (same as Linux join script)
	fmt.Print("  [1/4] Generating WireGuard keys... ")
	cfg, err := config.JoinNetwork(serverURL, networkID, configFile)
	if err != nil {
		fmt.Printf("FAILED\n  Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")

	fmt.Printf("\n  ✓ Virtual IP  : %s\n", cfg.VirtualIP)
	fmt.Printf("  ✓ Network     : %s\n", cfg.NetworkCIDR)
	fmt.Printf("  ✓ Member ID   : %s\n", cfg.MemberID)
	fmt.Printf("  ✓ Config saved: %s\n", configFile)
	fmt.Println()

	// Start tunnel
	fmt.Println("  [2/4] Starting WireGuard tunnel...")
	startAgent(cfg)
}

func leaveNetwork() {
	fmt.Printf("  Leaving network...\n")
	// Stop service if running
	_ = exec.Command("sc", "stop", serviceName).Run()
	_ = exec.Command("sc", "delete", serviceName).Run()

	// Remove config
	if err := os.Remove(configFile); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  Warning: %v\n", err)
	}
	fmt.Println("  ✓ Left network successfully")
}

func showStatus() {
	fmt.Printf("\n  %s Status\n", appName)
	fmt.Printf("  ══════════════════════════════════════\n")

	// Check if config exists
	cfg, err := config.LoadFromFile(configFile)
	if err != nil {
		fmt.Println("  Status: Not joined to any network")
		fmt.Printf("  Run: quicktunnel-win.exe join <server> <network_id>\n\n")
		return
	}

	fmt.Printf("  Network    : %s\n", cfg.NetworkID)
	fmt.Printf("  Virtual IP : %s\n", cfg.VirtualIP)
	fmt.Printf("  Server     : %s\n", cfg.ServerURL)
	fmt.Printf("  Member ID  : %s\n", cfg.MemberID)

	// Check if tunnel interface exists
	out, _ := exec.Command("powershell", "-Command",
		"Get-NetAdapter -Name 'qtun0' -ErrorAction SilentlyContinue | Select-Object Status").CombinedOutput()
	status := strings.TrimSpace(string(out))
	if strings.Contains(status, "Up") {
		fmt.Println("  Tunnel     : ✓ Connected")
	} else {
		fmt.Println("  Tunnel     : ✗ Disconnected")
	}

	// Check ping to gateway
	gateway := strings.Split(cfg.VirtualIP, ".")
	if len(gateway) == 4 {
		gw := gateway[0] + "." + gateway[1] + "." + gateway[2] + ".1"
		out, err := exec.Command("ping", "-n", "1", "-w", "1000", gw).CombinedOutput()
		if err == nil && strings.Contains(string(out), "TTL=") {
			fmt.Printf("  Gateway    : ✓ Reachable (%s)\n", gw)
		} else {
			fmt.Printf("  Gateway    : ✗ Unreachable (%s)\n", gw)
		}
	}

	fmt.Println()
}

func startDaemon() {
	cfg, err := config.LoadFromFile(configFile)
	if err != nil {
		fmt.Printf("  No config found. Run: quicktunnel-win.exe join <server> <network_id>\n")
		os.Exit(1)
	}
	
	// Check if running as Windows Service
	isService := os.Getenv("QUICKTUNNEL_SERVICE") == "1"
	if isService {
		startAgentBackground(cfg)
	} else {
		startAgent(cfg)
	}
}

func startAgentBackground(cfg *config.Config) {
	// Background mode for service - don't wait for console input
	a := agent.NewAgent(cfg)

	if err := a.Start(); err != nil {
		log.Fatal().Err(err).Msg("failed to start agent")
	}

	log.Info().Str("virtual_ip", cfg.VirtualIP).Msg("QuickTunnel started")

	// Keep running
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info().Msg("shutting down")
	if err := a.Stop(); err != nil {
		log.Warn().Err(err).Msg("stop error")
	}
}

func startAgent(cfg *config.Config) {
	a := agent.NewAgent(cfg)

	a.OnStateChange(func(from, to agent.AgentState) {
		icon := "●"
		switch to {
		case agent.StateRunning:
			icon = "✓"
		case agent.StateReconnecting:
			icon = "⟳"
		case agent.StateStopped:
			icon = "✗"
		}
		fmt.Printf("  %s %s → %s\n", icon, from, to)
	})

	if err := a.Start(); err != nil {
		fmt.Printf("\n  ✗ Failed to start: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n  ════════════════════════════════════\n")
	fmt.Printf("  ✓ %s is running!\n", appName)
	fmt.Printf("  ✓ Virtual IP: %s\n", cfg.VirtualIP)
	fmt.Printf("  ✓ Press Ctrl+C to disconnect\n")
	fmt.Printf("  ════════════════════════════════════\n\n")

	// Wait for Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n  Shutting down...")
	if err := a.Stop(); err != nil {
		fmt.Printf("  Warning: %v\n", err)
	}
	fmt.Println("  ✓ Disconnected")
}

func installService() {
	exePath, _ := os.Executable()
	absPath, _ := filepath.Abs(exePath)

	fmt.Printf("  Installing %s as Windows service...\n", appName)

	// Create service with QUICKTUNNEL_SERVICE env var
	binpath := fmt.Sprintf(`"%s" start`, absPath)
	out, err := exec.Command("sc", "create", serviceName,
		"binpath=", binpath,
		"start=", "auto",
		"DisplayName=", appName+" VPN Service",
	).CombinedOutput()
	if err != nil {
		fmt.Printf("  Failed: %s\n", strings.TrimSpace(string(out)))
		os.Exit(1)
	}

	// Set environment variable for background mode
	_ = exec.Command("reg", "add",
		"HKLM\\SYSTEM\\CurrentControlSet\\Services\\"+serviceName+"\\Parameters",
		"/v", "Environment",
		"/t", "REG_MULTI_SZ",
		"/d", "QUICKTUNNEL_SERVICE=1",
		"/f").Run()

	// Set description
	_ = exec.Command("sc", "description", serviceName,
		"QuickTunnel VPN — ZeroTier-style mesh networking").Run()

	// Set recovery (restart on failure)
	_ = exec.Command("sc", "failure", serviceName,
		"reset=", "60", "actions=", "restart/5000/restart/10000/restart/30000").Run()

	// Allow service to interact with desktop (for tun device)
	_ = exec.Command("sc", "config", serviceName,
		"type=", "own",
		"interact=", "own").Run()

	// Start service
	time.Sleep(1 * time.Second)
	_ = exec.Command("sc", "start", serviceName).Run()

	fmt.Printf("  ✓ Service '%s' installed and started\n", serviceName)
	fmt.Println("  ✓ Will auto-start on boot")
	fmt.Println("  ✓ Run: sc query QuickTunnelSvc to check status")
}

func uninstallService() {
	fmt.Printf("  Removing %s service...\n", appName)
	_ = exec.Command("sc", "stop", serviceName).Run()
	time.Sleep(2 * time.Second)
	out, err := exec.Command("sc", "delete", serviceName).CombinedOutput()
	if err != nil {
		fmt.Printf("  Warning: %s\n", strings.TrimSpace(string(out)))
	}
	fmt.Printf("  ✓ Service removed\n")
}

func setupLogging() {
	_ = os.MkdirAll(configDir, 0755)
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// Console only
		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"}).
			With().Timestamp().Logger()
		return
	}
	// Both file and console
	multi := zerolog.MultiLevelWriter(
		zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05", NoColor: true},
		f,
	)
	log.Logger = zerolog.New(multi).With().Timestamp().Logger()
}
