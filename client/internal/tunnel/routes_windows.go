//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

func enableIPForwarding() {
	_ = exec.Command("powershell", "-Command",
		"Set-NetIPInterface -Forwarding Enabled 2>$null").Run()
}

func addSubnetRoute(networkCIDR, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		networkCIDR, deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add subnet route: %s\n", strings.TrimSpace(string(out)))
	}
	return nil
}

func addHostRoute(peerIP, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		peerIP+"/32", deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add host route: %s\n", strings.TrimSpace(string(out)))
	}
	return nil
}

func removeHostRoute(peerIP, deviceName string) error {
	_, _ = exec.Command("netsh", "interface", "ipv4", "delete", "route",
		peerIP+"/32", deviceName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not found") {
		fmt.Printf("[WARN] remove peer route failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func configureWG(deviceName, privateKey string, listenPort int) (bool, error) {
	// Windows WireGuard uses config files, not wg set
	// Write temp config and install tunnel service
	_, err := exec.LookPath("wireguard.exe")
	if err != nil {
		return false, fmt.Errorf("wireguard.exe not found")
	}
	// For now, attempt wg.exe if available
	wgPath, err := exec.LookPath("wg")
	if err != nil {
		return false, fmt.Errorf("wg.exe not found")
	}
	cmd := exec.Command(wgPath, "set", deviceName,
		"listen-port", fmt.Sprintf("%d", listenPort),
		"private-key", "CON")
	cmd.Stdin = strings.NewReader(privateKey)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%s — %v", strings.TrimSpace(string(out)), err)
	}
	return true, nil
}

// configureWGViaConfigFile writes a WireGuard .conf and loads it.
func configureWGViaConfigFile(deviceName, privateKey string, listenPort int) (bool, error) {
	confDir := filepath.Join(os.Getenv("ProgramData"), "WireGuard")
	_ = os.MkdirAll(confDir, 0o700)
	confPath := filepath.Join(confDir, deviceName+".conf")

	tmpl := template.Must(template.New("wg").Parse(wgConfTemplate))
	f, err := os.OpenFile(confPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false, fmt.Errorf("write wg config: %w", err)
	}
	err = tmpl.Execute(f, map[string]interface{}{
		"PrivateKey": privateKey,
		"ListenPort": listenPort,
	})
	_ = f.Close()
	if err != nil {
		return false, fmt.Errorf("render wg config: %w", err)
	}

	// Try wireguard.exe syncconf
	wgExePaths := []string{
		`C:\Program Files\WireGuard\wireguard.exe`,
		`C:\Program Files (x86)\WireGuard\wireguard.exe`,
	}
	for _, p := range wgExePaths {
		if _, err := os.Stat(p); err == nil {
			cmd := exec.Command(p, "/syncconf", deviceName, confPath)
			if out, err := cmd.CombinedOutput(); err == nil {
				return true, nil
			} else {
				fmt.Printf("[WARN] wireguard syncconf: %s\n", strings.TrimSpace(string(out)))
			}
			break
		}
	}

	return false, fmt.Errorf("wireguard tools not available on Windows — install from https://www.wireguard.com/install/")
}

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string) error {
	out, err := exec.Command("wg", "set", deviceName,
		"peer", publicKey,
		"endpoint", endpoint,
		"allowed-ips", allowedIP,
		"persistent-keepalive", "25").CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] add wg peer failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func removeWGPeer(deviceName, publicKey string) error {
	wgExe := findWGExe()
	if wgExe == "" {
		return nil
	}
	_, _ = exec.Command(wgExe, "set", deviceName, "peer", publicKey, "remove").CombinedOutput()
	return nil
}

func updateWGPeerEndpoint(deviceName, publicKey, endpoint string) error {
	wgExe := findWGExe()
	if wgExe == "" {
		return nil
	}
	out, err := exec.Command(wgExe, "set", deviceName, "peer", publicKey, "endpoint", endpoint).CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] update endpoint: %s\n", strings.TrimSpace(string(out)))
	}
	return nil
}

