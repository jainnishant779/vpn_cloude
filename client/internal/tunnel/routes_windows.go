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

// wgSockPath returns the named pipe path that wireguard-go creates for the interface.
// Windows wireguard-go exposes a named pipe at \\.\pipe\WireGuard\<iface>
func wgSockPath(deviceName string) string {
	return `\\.\pipe\WireGuard\` + deviceName
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

// configureWG configures WireGuard via named pipe (wireguard-go IPC protocol).
// This works with wintun-based interfaces created by wireguard-go.
func configureWG(deviceName, privateKey string, listenPort int) (bool, error) {
	wgExe := findWGExe()
	if wgExe == "" {
		fmt.Println("[WARN] wg.exe not found — WireGuard tools not installed")
		return false, fmt.Errorf("wg.exe not found")
	}

	// Write private key to temp file
	tmpFile := filepath.Join(os.TempDir(), "qt-wgkey.tmp")
	if err := os.WriteFile(tmpFile, []byte(privateKey+"\n"), 0o600); err != nil {
		return false, fmt.Errorf("write key file: %w", err)
	}
	defer os.Remove(tmpFile)

	// wireguard-go on Windows uses named pipe - pass via WIREGUARD_USERSPACE_IMPLEMENTATION
	// wg.exe needs the named pipe socket to be set via env var
	cmd := exec.Command(wgExe, "set", deviceName,
		"listen-port", fmt.Sprintf("%d", listenPort),
		"private-key", tmpFile)

	// Set the named pipe path so wg.exe communicates with wireguard-go
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("WIREGUARD_USERSPACE_IMPLEMENTATION=%s", wgSockPath(deviceName)),
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Try without env var override
		cmd2 := exec.Command(wgExe, "set", deviceName,
			"listen-port", fmt.Sprintf("%d", listenPort),
			"private-key", tmpFile)
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			fmt.Printf("[WARN] wg set failed (both methods): %s\n", strings.TrimSpace(string(out2)))
			fmt.Printf("       Original error: %s\n", strings.TrimSpace(string(out)))
			return false, nil // non-fatal: raw TUN fallback
		}
	}

	fmt.Printf("[INFO] WireGuard configured on %s (port %d)\n", deviceName, listenPort)
	return true, nil
}

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string) error {
	wgExe := findWGExe()
	if wgExe == "" {
		return nil // silently skip — raw TUN mode
	}
	tmpFile := filepath.Join(os.TempDir(), "qt-wgkey.tmp")
	// Reuse existing key file if present, else skip
	out, err := exec.Command(wgExe, "set", deviceName,
		"peer", publicKey,
		"endpoint", endpoint,
		"allowed-ips", allowedIP,
		"persistent-keepalive", "25").CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] add wg peer %s: %s\n", endpoint, strings.TrimSpace(string(out)))
		_ = tmpFile
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
