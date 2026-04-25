//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// WG functions are no-ops — real work done in WindowsTUNDevice methods
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

func enableIPForwarding() {
	_ = exec.Command("powershell", "-Command",
		"Set-NetIPInterface -Forwarding Enabled 2>$null").Run()
}

func addSubnetRoute(networkCIDR, deviceName string) error {
	fmt.Printf("[WIN-ROUTE] Adding subnet route %s via %s\n", networkCIDR, deviceName)
	// Use PowerShell for more reliable route add
	psCmd := fmt.Sprintf(
		"$idx = (Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue).ifIndex; "+
			"if ($idx) { New-NetRoute -DestinationPrefix '%s' -InterfaceIndex $idx -ErrorAction SilentlyContinue }",
		deviceName, networkCIDR)
	out, err := exec.Command("powershell", "-Command", psCmd).CombinedOutput()
	if err != nil {
		// Fallback to netsh
		out2, err2 := exec.Command("netsh", "interface", "ipv4", "add", "route",
			networkCIDR, deviceName, "store=active").CombinedOutput()
		if err2 != nil && !strings.Contains(string(out2), "exists") && !strings.Contains(string(out2), "already") {
			fmt.Printf("[WIN-ROUTE] subnet route failed: ps=%s netsh=%s\n",
				strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	return nil
}

func addHostRoute(peerIP, deviceName string) error {
	fmt.Printf("[WIN-ROUTE] Adding host route %s/32 via %s\n", peerIP, deviceName)
	psCmd := fmt.Sprintf(
		"$idx = (Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue).ifIndex; "+
			"if ($idx) { New-NetRoute -DestinationPrefix '%s/32' -InterfaceIndex $idx -ErrorAction SilentlyContinue }",
		deviceName, peerIP)
	out, _ := exec.Command("powershell", "-Command", psCmd).CombinedOutput()
	_ = out
	return nil
}

func removeHostRoute(peerIP, deviceName string) error {
	psCmd := fmt.Sprintf(
		"$idx = (Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue).ifIndex; "+
			"if ($idx) { Remove-NetRoute -DestinationPrefix '%s/32' -InterfaceIndex $idx -Confirm:$false -ErrorAction SilentlyContinue }",
		deviceName, peerIP)
	_ = exec.Command("powershell", "-Command", psCmd).Run()
	return nil
}

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
