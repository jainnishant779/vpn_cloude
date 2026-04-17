package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"quicktunnel/client/internal/agent"
	"quicktunnel/client/internal/api_client"
	"quicktunnel/client/internal/config"
	"quicktunnel/client/internal/vnc"

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

func runUp(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("up command does not accept arguments")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("up: load config: %w", err)
	}
	configureLogging(cfg.LogLevel)

	ag := agent.NewAgent(cfg)
	ag.OnStateChange(func(from agent.AgentState, to agent.AgentState) {
		log.Info().Str("from", string(from)).Str("to", string(to)).Msg("agent state changed")
	})

	if err := ag.Start(); err != nil {
		return fmt.Errorf("up: start agent: %w", err)
	}
	defer ag.Stop()

	if err := writePIDFile(os.Getpid()); err != nil {
		log.Warn().Err(err).Msg("failed to write pid file")
	}
	defer removePIDFile()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	log.Info().Msg("quicktunnel agent running in foreground")
	<-sigCh
	log.Info().Msg("shutdown signal received")

	if err := ag.Stop(); err != nil {
		return fmt.Errorf("up: stop agent: %w", err)
	}
	return nil
}

func runDown(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("down command does not accept arguments")
	}

	pid, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("down: find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		if killErr := process.Kill(); killErr != nil {
			return fmt.Errorf("down: stop process: %w", err)
		}
	}

	removePIDFile()
	fmt.Printf("Sent stop signal to quicktunnel process %d\n", pid)
	return nil
}

func runStatus(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("status command does not accept arguments")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("status: load config: %w", err)
	}

	pid, pidErr := readPIDFile()
	running := pidErr == nil

	out := map[string]any{
		"running":    running,
		"pid":        pid,
		"server_url": cfg.ServerURL,
		"network_id": cfg.NetworkID,
		"device_name": cfg.DeviceName,
	}

	if cfg.APIKey != "" && cfg.NetworkID != "" {
		client := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
		peers, err := client.GetPeers(cfg.NetworkID)
		if err == nil {
			out["peer_count"] = len(peers)
		}
	}

	return printJSON(out)
}

func runPeers(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("peers command does not accept arguments")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("peers: load config: %w", err)
	}
	if cfg.APIKey == "" || cfg.NetworkID == "" {
		return fmt.Errorf("peers: api_key and network_id are required")
	}

	client := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
	peers, err := client.GetPeers(cfg.NetworkID)
	if err != nil {
		return fmt.Errorf("peers: fetch peers: %w", err)
	}

	return printJSON(peers)
}

func runVNC(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("vnc usage: quicktunnel vnc <peer-name>")
	}
	peerName := strings.TrimSpace(args[0])
	if peerName == "" {
		return fmt.Errorf("vnc: peer-name is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("vnc: load config: %w", err)
	}
	if cfg.APIKey == "" || cfg.NetworkID == "" {
		return fmt.Errorf("vnc: api_key and network_id are required")
	}

	client := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
	peers, err := client.GetPeers(cfg.NetworkID)
	if err != nil {
		return fmt.Errorf("vnc: fetch peers: %w", err)
	}

	for _, peerInfo := range peers {
		if strings.EqualFold(strings.TrimSpace(peerInfo.Name), peerName) {
			port := peerInfo.VNCPort
			if port == 0 {
				port = 5900
			}
			if err := vnc.LaunchVNCViewer(peerInfo.VirtualIP, port); err != nil {
				return fmt.Errorf("vnc: launch viewer: %w", err)
			}
			fmt.Printf("Launched VNC viewer for %s (%s:%d)\n", peerInfo.Name, peerInfo.VirtualIP, port)
			return nil
		}
	}

	return fmt.Errorf("vnc: peer not found: %s", peerName)
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	email := fs.String("email", "", "user email")
	password := fs.String("password", "", "user password")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("login: parse flags: %w", err)
	}

	if strings.TrimSpace(*email) == "" || strings.TrimSpace(*password) == "" {
		return fmt.Errorf("login: --email and --password are required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("login: load config: %w", err)
	}

	client := api_client.NewClient(cfg.ServerURL, cfg.APIKey)
	resp, err := client.Login(*email, *password)
	if err != nil {
		return fmt.Errorf("login: authenticate: %w", err)
	}

	cfg.APIKey = resp.APIKey
	cfg.Email = strings.TrimSpace(*email)
	cfg.Password = strings.TrimSpace(*password)
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("login: save config: %w", err)
	}

	fmt.Printf("Login successful. API key saved for %s\n", resp.User.Email)
	return nil
}

func runConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	setValues := multiValueFlag{}
	fs.Var(&setValues, "set", "set config value (key=value), repeatable")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: load config: %w", err)
	}

	if len(setValues) == 0 {
		return printJSON(cfg)
	}

	for _, item := range setValues {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("config: invalid --set value: %s", item)
		}
		if err := applyConfigValue(cfg, strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("config: save file: %w", err)
	}
	return printJSON(cfg)
}

func applyConfigValue(cfg *config.Config, key, value string) error {
	switch strings.ToLower(key) {
	case "server_url":
		cfg.ServerURL = value
	case "api_key":
		cfg.APIKey = value
	case "network_id":
		cfg.NetworkID = value
	case "device_name":
		cfg.DeviceName = value
	case "vnc_port":
		v, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid vnc_port: %w", err)
		}
		cfg.VNCPort = v
	case "log_level":
		cfg.LogLevel = value
	case "wg_listen_port":
		v, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid wg_listen_port: %w", err)
		}
		cfg.WGListenPort = v
	case "stun_server":
		cfg.STUNServer = value
	case "email":
		cfg.Email = value
	case "password":
		cfg.Password = value
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

func configureLogging(levelRaw string) {
	level, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(levelRaw)))
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
}

func printUsage() {
	fmt.Println("QuickTunnel CLI")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  quicktunnel up")
	fmt.Println("  quicktunnel down")
	fmt.Println("  quicktunnel status")
	fmt.Println("  quicktunnel peers")
	fmt.Println("  quicktunnel vnc <peer-name>")
	fmt.Println("  quicktunnel login --email <email> --password <password>")
	fmt.Println("  quicktunnel config [--set key=value]")
}

func printJSON(value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("print json: marshal value: %w", err)
	}
	fmt.Println(string(payload))
	return nil
}

type multiValueFlag []string

func (m *multiValueFlag) String() string {
	if m == nil {
		return ""
	}
	return strings.Join(*m, ",")
}

func (m *multiValueFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func pidFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pid path: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".quicktunnel", "quicktunnel.pid"), nil
}

func writePIDFile(pid int) error {
	path, err := pidFilePath()
	if err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write pid file: create directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0o644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	return nil
}

func readPIDFile() (int, error) {
	path, err := pidFilePath()
	if err != nil {
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return 0, fmt.Errorf("read pid file: parse pid: %w", err)
	}
	return pid, nil
}

func removePIDFile() {
	path, err := pidFilePath()
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

func init() {
	// Keep timestamps deterministic in short-lived commands.
	time.Local = time.UTC
}
