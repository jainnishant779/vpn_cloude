//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	return nil
}

func findWGExe() string {
	candidates := []string{
		`C:\Program Files\WireGuard\wg.exe`,
		`C:\Program Files (x86)\WireGuard\wg.exe`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("wg.exe"); err == nil {
		return p
	}
	return ""
}

func configureWG(deviceName, privateKey string, listenPort int) (bool, error) {
	wgExe := findWGExe()
	if wgExe == "" {
		return false, fmt.Errorf("wg.exe not found — install WireGuard from https://wireguard.com/install")
	}

	// Write private key to temp file (stdin piping unreliable on Windows)
	tmpFile := filepath.Join(os.TempDir(), "qt-wgkey.tmp")
	if err := os.WriteFile(tmpFile, []byte(privateKey), 0o600); err != nil {
		return false, fmt.Errorf("write key file: %w", err)
	}
	defer os.Remove(tmpFile)

	out, err := exec.Command(wgExe, "set", deviceName,
		"listen-port", fmt.Sprintf("%d", listenPort),
		"private-key", tmpFile).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("wg set interface: %s — %w", strings.TrimSpace(string(out)), err)
	}
	fmt.Printf("[INFO] WireGuard active on %s (port %d)\n", deviceName, listenPort)
	return true, nil
}

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string) error {
	wgExe := findWGExe()
	if wgExe == "" {
		return fmt.Errorf("wg.exe not found")
	}
	out, err := exec.Command(wgExe, "set", deviceName,
		"peer", publicKey,
		"endpoint", endpoint,
		"allowed-ips", allowedIP,
		"persistent-keepalive", "25").CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] add wg peer: %s\n", strings.TrimSpace(string(out)))
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
