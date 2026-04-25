//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ── WG commands (no-ops — WindowsTUNDevice handles in-process) ──────────────

func configureWG(deviceName, privateKey string, listenPort int) (bool, error) {
	return true, nil
}

func addWGPeer(deviceName, publicKey, endpoint, allowedIP string) error {
	return nil
}

func removeWGPeer(deviceName, publicKey string) error {
	return nil
}

func updateWGPeerEndpoint(deviceName, publicKey, endpoint string) error {
	return nil
}

// ── System ──────────────────────────────────────────────────────────────────

func enableIPForwarding() {
	// Enable IP forwarding on all interfaces
	_ = exec.Command("powershell", "-Command",
		"Set-NetIPInterface -Forwarding Enabled -ErrorAction SilentlyContinue").Run()
	// Also via registry (persists)
	_ = exec.Command("reg", "add",
		"HKLM\\SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters",
		"/v", "IPEnableRouter", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	logW("SYS", "IP forwarding enabled")
}

// ── Routes ──────────────────────────────────────────────────────────────────

func addSubnetRoute(networkCIDR, deviceName string) error {
	logW("ROUTE", "Adding subnet %s → %s", networkCIDR, deviceName)

	// PowerShell — most reliable
	psCmd := fmt.Sprintf(
		"$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue; "+
			"if ($a) { "+
			"  $existing = Get-NetRoute -DestinationPrefix '%s' -InterfaceIndex $a.ifIndex -ErrorAction SilentlyContinue; "+
			"  if (-not $existing) { "+
			"    New-NetRoute -DestinationPrefix '%s' -InterfaceIndex $a.ifIndex -RouteMetric 10 -PolicyStore ActiveStore -ErrorAction SilentlyContinue "+
			"  } "+
			"}",
		deviceName, networkCIDR, networkCIDR)

	out, err := exec.Command("powershell", "-Command", psCmd).CombinedOutput()
	if err != nil {
		// Fallback netsh
		out2, _ := exec.Command("netsh", "interface", "ipv4", "add", "route",
			networkCIDR, deviceName, "metric=10", "store=active").CombinedOutput()
		outStr := strings.TrimSpace(string(out2))
		if outStr != "" && !strings.Contains(outStr, "exists") && !strings.Contains(outStr, "already") {
			logW("ROUTE", "subnet fallback: %s", outStr)
		}
		_ = out
	}
	return nil
}

func addHostRoute(peerIP, deviceName string) error {
	logW("ROUTE", "Adding host %s/32 → %s", peerIP, deviceName)

	psCmd := fmt.Sprintf(
		"$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue; "+
			"if ($a) { "+
			"  New-NetRoute -DestinationPrefix '%s/32' -InterfaceIndex $a.ifIndex -RouteMetric 10 -PolicyStore ActiveStore -ErrorAction SilentlyContinue "+
			"}",
		deviceName, peerIP)

	_, _ = exec.Command("powershell", "-Command", psCmd).CombinedOutput()
	return nil
}

func removeHostRoute(peerIP, deviceName string) error {
	psCmd := fmt.Sprintf(
		"$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue; "+
			"if ($a) { "+
			"  Remove-NetRoute -DestinationPrefix '%s/32' -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue "+
			"}",
		deviceName, peerIP)

	_ = exec.Command("powershell", "-Command", psCmd).Run()
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func maskBitsFromCIDR(cidr string) (int, error) {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		if bits, err := strconv.Atoi(parts[1]); err == nil {
			return bits, nil
		}
	}
	if bits, err := strconv.Atoi(cidr); err == nil {
		return bits, nil
	}
	return 24, nil
}
