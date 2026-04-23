//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os/exec"
	"strings"
)

func enableIPForwarding(deviceName string) {
	_ = exec.Command("powershell", "-Command",
		fmt.Sprintf("Set-NetIPInterface -InterfaceAlias \"%s\" -Forwarding Enabled", deviceName)).Run()
}

func addSubnetRoute(networkCIDR, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		networkCIDR, deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add subnet route failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func addHostRoute(peerIP, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "add", "route",
		peerIP+"/32", deviceName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		fmt.Printf("[WARN] add peer route failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func removeHostRoute(peerIP, deviceName string) error {
	out, err := exec.Command("netsh", "interface", "ipv4", "delete", "route",
		peerIP+"/32", deviceName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not found") {
		fmt.Printf("[WARN] remove peer route failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func configureWG(deviceName, privateKey string, listenPort int, wgPath string) (bool, error) {
	if wgPath == "" {
		// Try wg first, if not found, we can't configure kernel wg
		path, err := exec.LookPath("wg")
		if err != nil {
			path, err = exec.LookPath("wireguard.exe")
			if err != nil {
				return false, fmt.Errorf("wg.exe not found")
			}
			wgPath = path
		} else {
			wgPath = path
		}
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

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string, wgPath string) error {
	if wgPath == "" {
		wgPath = "wg"
	}
	out, err := exec.Command(wgPath, "set", deviceName,
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

func removeWGPeer(deviceName, publicKey string, wgPath string) error {
	if wgPath == "" {
		wgPath = "wg"
	}
	out, err := exec.Command(wgPath, "set", deviceName,
		"peer", publicKey, "remove").CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] remove wg peer failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

func updateWGPeerEndpoint(deviceName, publicKey, endpoint string, wgPath string) error {
	if wgPath == "" {
		wgPath = "wg"
	}
	out, err := exec.Command(wgPath, "set", deviceName,
		"peer", publicKey,
		"endpoint", endpoint).CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] update peer endpoint failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

