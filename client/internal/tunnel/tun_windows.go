//go:build windows
// +build windows

package tunnel

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
)

// WindowsTUNDevice — production-grade in-process WireGuard for Windows.
// Handles: wintun adapter, WireGuard crypto, firewall, routes, IP config.
type WindowsTUNDevice struct {
	mu     sync.Mutex
	tunDev tun.Device
	wgDev  *device.Device
	uapiLn net.Listener
	name   string
	ip     string
	cidr   string
	port   int
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	if mtu <= 0 {
		mtu = 1420
	}

	logW("TUN", "Creating wintun adapter '%s' MTU=%d", name, mtu)

	// Kill any stale adapter first
	killStaleAdapter(name)

	tunDev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("wintun create: %w", err)
	}

	realName, err := tunDev.Name()
	if err != nil {
		_ = tunDev.Close()
		return nil, fmt.Errorf("wintun name: %w", err)
	}

	logW("TUN", "Adapter created: '%s'", realName)
	return &WindowsTUNDevice{tunDev: tunDev, name: realName}, nil
}

func (d *WindowsTUNDevice) Name() string { return d.name }

func (d *WindowsTUNDevice) Configure(ip, cidr string) error {
	d.ip = ip
	d.cidr = cidr
	logW("CFG", "Configuring %s → %s/%s", d.name, ip, cidr)

	// 1. Wait for adapter in Windows network stack
	if err := d.waitForAdapter(); err != nil {
		return err
	}

	// 2. Enable adapter
	d.enableAdapter()

	// 3. Set IP address (try multiple methods)
	if err := d.setIPAddress(ip, cidr); err != nil {
		return err
	}

	// 4. Set MTU
	d.setMTU(1420)

	// 5. Set interface metric low (priority)
	d.setMetric(10)

	// 6. Verify
	d.verifyConfig()

	return nil
}

func (d *WindowsTUNDevice) SetupWireGuard(privateKeyB64 string, listenPort int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tunDev == nil {
		return fmt.Errorf("wg setup: tun nil")
	}

	d.port = listenPort
	privHex, err := b64ToHex(privateKeyB64)
	if err != nil {
		return fmt.Errorf("wg setup: key: %w", err)
	}

	logW("WG", "Starting WireGuard on %s port %d", d.name, listenPort)

	// Use LogLevelError in production, LogLevelVerbose for debug
	logger := device.NewLogger(device.LogLevelError, fmt.Sprintf("[WG/%s] ", d.name))
	wgDev := device.NewDevice(d.tunDev, conn.NewDefaultBind(), logger)

	ipcCfg := fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privHex, listenPort)
	if err := wgDev.IpcSet(ipcCfg); err != nil {
		wgDev.Close()
		return fmt.Errorf("wg setup: ipc: %w", err)
	}
	if err := wgDev.Up(); err != nil {
		wgDev.Close()
		return fmt.Errorf("wg setup: up: %w", err)
	}

	d.wgDev = wgDev
	logW("WG", "Device UP — in-process WireGuard active")

	// Open firewall
	d.openFirewall(listenPort)

	// UAPI listener
	d.startUAPI()

	// Start handshake monitor
	go d.monitorHandshakes()

	return nil
}

func (d *WindowsTUNDevice) AddWGPeer(publicKeyB64, endpoint, allowedIP string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDev == nil {
		return fmt.Errorf("add peer: wg nil")
	}

	pubHex, err := b64ToHex(publicKeyB64)
	if err != nil {
		return fmt.Errorf("add peer: key: %w", err)
	}
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}

	logW("PEER", "Adding: %s.. → %s [%s]",
		publicKeyB64[:8], endpoint, allowedIP)

	// Replace existing peer (remove first to avoid stale state)
	_ = d.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
	time.Sleep(50 * time.Millisecond)

	ipcCfg := fmt.Sprintf(
		"public_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=15\n",
		pubHex, endpoint, allowedIP)

	if err := d.wgDev.IpcSet(ipcCfg); err != nil {
		return fmt.Errorf("add peer: ipc: %w", err)
	}

	logW("PEER", "Added successfully")
	return nil
}

func (d *WindowsTUNDevice) RemoveWGPeer(publicKeyB64 string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDev == nil {
		return nil
	}
	pubHex, err := b64ToHex(publicKeyB64)
	if err != nil {
		return err
	}
	logW("PEER", "Removing: %s..", publicKeyB64[:8])
	return d.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
}

func (d *WindowsTUNDevice) Read(buf []byte) (int, error) {
	// WireGuard owns the TUN — block direct reads
	if d.wgDev != nil {
		select {} // block forever — WG handles packets
	}
	if d.tunDev == nil {
		return 0, fmt.Errorf("read: nil")
	}
	pkts := [][]byte{buf}
	sizes := make([]int, 1)
	_, err := d.tunDev.Read(pkts, sizes, 0)
	return sizes[0], err
}

func (d *WindowsTUNDevice) Write(buf []byte) (int, error) {
	if d.wgDev != nil {
		return len(buf), nil // WG handles — discard
	}
	if d.tunDev == nil {
		return 0, fmt.Errorf("write: nil")
	}
	_, err := d.tunDev.Write([][]byte{buf}, 0)
	return len(buf), err
}

func (d *WindowsTUNDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	logW("TUN", "Closing %s", d.name)

	// Close UAPI
	if d.uapiLn != nil {
		_ = d.uapiLn.Close()
		d.uapiLn = nil
	}

	// Close WG device
	if d.wgDev != nil {
		d.wgDev.Close()
		d.wgDev = nil
	}

	// Close TUN
	if d.tunDev != nil {
		err := d.tunDev.Close()
		d.tunDev = nil
		return err
	}

	// Cleanup firewall rules
	d.cleanFirewall()

	return nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Internal helpers
// ═══════════════════════════════════════════════════════════════════════════════

func (d *WindowsTUNDevice) waitForAdapter() error {
	logW("CFG", "Waiting for adapter '%s' in network stack...", d.name)
	for i := 0; i < 30; i++ {
		// Check via PowerShell — most reliable
		out, err := exec.Command("powershell", "-Command",
			fmt.Sprintf("(Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue).Status", d.name)).CombinedOutput()
		status := strings.TrimSpace(string(out))
		if err == nil && (status == "Up" || status == "Disconnected" || status == "Not Present") {
			logW("CFG", "Adapter found: status=%s (attempt %d)", status, i+1)
			return nil
		}
		if i%5 == 4 {
			logW("CFG", "Still waiting... (attempt %d, status=%s)", i+1, status)
		}
		time.Sleep(300 * time.Millisecond)
	}
	logW("CFG", "WARNING: Adapter wait timeout — continuing anyway")
	return nil
}

func (d *WindowsTUNDevice) enableAdapter() {
	_ = exec.Command("powershell", "-Command",
		fmt.Sprintf("Enable-NetAdapter -Name '%s' -Confirm:$false -ErrorAction SilentlyContinue", d.name)).Run()
	time.Sleep(200 * time.Millisecond)
}

func (d *WindowsTUNDevice) setIPAddress(ip, cidr string) error {
	bits := cidrToBits(cidr)

	// Method 1: PowerShell New-NetIPAddress (most reliable)
	logW("CFG", "Method 1: PowerShell Set IP %s/%d on %s", ip, bits, d.name)
	psCmd := fmt.Sprintf(
		"$a = Get-NetAdapter -Name '%s' -ErrorAction Stop; "+
			"Remove-NetIPAddress -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue; "+
			"Remove-NetRoute -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue; "+
			"New-NetIPAddress -InterfaceIndex $a.ifIndex -IPAddress '%s' -PrefixLength %d -PolicyStore ActiveStore -ErrorAction Stop",
		d.name, ip, bits)

	out, err := exec.Command("powershell", "-Command", psCmd).CombinedOutput()
	if err == nil {
		logW("CFG", "IP set via PowerShell OK")
		return nil
	}
	logW("CFG", "PowerShell failed: %s", strings.TrimSpace(string(out)))

	// Method 2: netsh
	logW("CFG", "Method 2: netsh")
	mask := bitsToNetmask(bits)
	for i := 0; i < 5; i++ {
		out, err := exec.Command("netsh", "interface", "ip", "set", "address",
			fmt.Sprintf("name=%s", d.name), "source=static",
			fmt.Sprintf("addr=%s", ip), fmt.Sprintf("mask=%s", mask)).CombinedOutput()
		if err == nil {
			logW("CFG", "IP set via netsh OK (attempt %d)", i+1)
			return nil
		}
		logW("CFG", "netsh attempt %d: %s", i+1, strings.TrimSpace(string(out)))
		time.Sleep(1 * time.Second)
	}

	// Method 3: netsh add (instead of set)
	logW("CFG", "Method 3: netsh add")
	out, err = exec.Command("netsh", "interface", "ip", "add", "address",
		fmt.Sprintf("name=%s", d.name),
		fmt.Sprintf("addr=%s", ip),
		fmt.Sprintf("mask=%s", mask)).CombinedOutput()
	if err == nil {
		logW("CFG", "IP set via netsh add OK")
		return nil
	}

	return fmt.Errorf("all IP methods failed for %s/%d on %s", ip, bits, d.name)
}

func (d *WindowsTUNDevice) setMTU(mtu int) {
	_ = exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
		d.name, fmt.Sprintf("mtu=%d", mtu), "store=active").Run()
}

func (d *WindowsTUNDevice) setMetric(metric int) {
	_ = exec.Command("powershell", "-Command",
		fmt.Sprintf("Set-NetIPInterface -InterfaceAlias '%s' -InterfaceMetric %d -ErrorAction SilentlyContinue",
			d.name, metric)).Run()
}

func (d *WindowsTUNDevice) verifyConfig() {
	// Show IP
	out, _ := exec.Command("powershell", "-Command",
		fmt.Sprintf("Get-NetIPAddress -InterfaceAlias '%s' -AddressFamily IPv4 -ErrorAction SilentlyContinue | Format-Table IPAddress,PrefixLength -AutoSize", d.name)).CombinedOutput()
	logW("CFG", "IP Verify:\n%s", strings.TrimSpace(string(out)))

	// Show adapter status
	out2, _ := exec.Command("powershell", "-Command",
		fmt.Sprintf("Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue | Format-Table Name,Status,LinkSpeed -AutoSize", d.name)).CombinedOutput()
	logW("CFG", "Adapter:\n%s", strings.TrimSpace(string(out2)))
}

func (d *WindowsTUNDevice) openFirewall(port int) {
	rules := []struct {
		name, proto, dir string
		lport             int
	}{
		{fmt.Sprintf("QT-WG-UDP-IN-%d", port), "UDP", "in", port},
		{fmt.Sprintf("QT-WG-UDP-OUT-%d", port), "UDP", "out", port},
		{"QT-ICMP-IN", "ICMPv4", "in", 0},
		{"QT-ICMP-OUT", "ICMPv4", "out", 0},
		{"QT-VNC-IN-5900", "TCP", "in", 5900},
		{"QT-VNC-OUT-5900", "TCP", "out", 5900},
		{"QT-RDP-IN-3389", "TCP", "in", 3389},
		{"QT-RDP-OUT-3389", "TCP", "out", 3389},
	}

	for _, r := range rules {
		// Delete old
		_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
			fmt.Sprintf("name=%s", r.name)).Run()

		// Add new
		args := []string{"advfirewall", "firewall", "add", "rule",
			fmt.Sprintf("name=%s", r.name),
			fmt.Sprintf("dir=%s", r.dir),
			"action=allow",
			fmt.Sprintf("protocol=%s", r.proto),
			"enable=yes",
		}
		if r.lport > 0 {
			args = append(args, fmt.Sprintf("localport=%d", r.lport))
		}
		out, err := exec.Command("netsh", args...).CombinedOutput()
		if err != nil {
			logW("FW", "Rule %s failed: %s", r.name, strings.TrimSpace(string(out)))
		}
	}

	// Also allow ALL traffic on the wintun interface
	_ = exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=QT-Interface-Allow-All",
		"dir=in", "action=allow",
		fmt.Sprintf("interface=%s", d.name),
		"enable=yes").Run()

	logW("FW", "Firewall configured: UDP/%d + ICMP + VNC/5900 + RDP/3389", port)

	// Disable Windows Firewall on the tunnel profile (safest for VPN)
	_ = exec.Command("powershell", "-Command",
		fmt.Sprintf("Set-NetFirewallProfile -Name Public,Private,Domain -DisabledInterfaceAliases '%s' -ErrorAction SilentlyContinue", d.name)).Run()
	logW("FW", "Firewall disabled on interface %s", d.name)
}

func (d *WindowsTUNDevice) cleanFirewall() {
	names := []string{"QT-WG-UDP-IN-", "QT-WG-UDP-OUT-", "QT-ICMP-IN", "QT-ICMP-OUT",
		"QT-VNC-IN-5900", "QT-VNC-OUT-5900", "QT-RDP-IN-3389", "QT-RDP-OUT-3389",
		"QT-Interface-Allow-All"}
	for _, n := range names {
		_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
			fmt.Sprintf("name=%s", n)).Run()
	}
}

func (d *WindowsTUNDevice) startUAPI() {
	uapiLn, err := ipc.UAPIListen(d.name)
	if err != nil {
		logW("UAPI", "Listen failed (non-fatal): %v", err)
		return
	}
	d.uapiLn = uapiLn
	go func() {
		for {
			c, err := uapiLn.Accept()
			if err != nil {
				return
			}
			go d.wgDev.IpcHandle(c)
		}
	}()
	logW("UAPI", "Listener started")
}

func (d *WindowsTUNDevice) monitorHandshakes() {
	time.Sleep(3 * time.Second) // Wait for initial handshake
	for i := 0; i < 10; i++ {
		d.mu.Lock()
		if d.wgDev == nil {
			d.mu.Unlock()
			return
		}
		state, err := d.wgDev.IpcGet()
		d.mu.Unlock()

		if err != nil {
			return
		}

		lines := strings.Split(state, "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "last_handshake_time_sec=") {
				val := strings.TrimPrefix(l, "last_handshake_time_sec=")
				if sec, _ := strconv.ParseInt(val, 10, 64); sec > 0 {
					t := time.Unix(sec, 0)
					logW("WG", "✓ Handshake OK at %s (%.1fs ago)",
						t.Format("15:04:05"), time.Since(t).Seconds())
					return
				}
			}
		}

		if i < 9 {
			logW("WG", "No handshake yet (check %d/10)...", i+1)
			time.Sleep(3 * time.Second)
		}
	}
	logW("WG", "⚠ No handshake after 30s — check server/network connectivity")
}

// ═══════════════════════════════════════════════════════════════════════════════
// Utility functions
// ═══════════════════════════════════════════════════════════════════════════════

func killStaleAdapter(name string) {
	_ = exec.Command("powershell", "-Command",
		fmt.Sprintf("Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue | Disable-NetAdapter -Confirm:$false", name)).Run()
	time.Sleep(200 * time.Millisecond)
}

func b64ToHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", err
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("key: expected 32B got %dB", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

func cidrToBits(cidr string) int {
	// Handle formats: "16", "/16", "10.7.0.0/16"
	if strings.Contains(cidr, "/") {
		parts := strings.Split(cidr, "/")
		cidr = parts[len(parts)-1]
	}
	cidr = strings.TrimPrefix(cidr, "/")
	bits, err := strconv.Atoi(cidr)
	if err != nil || bits <= 0 || bits > 32 {
		return 24
	}
	return bits
}

func bitsToNetmask(bits int) string {
	m := uint32(0xFFFFFFFF) << (32 - bits)
	return fmt.Sprintf("%d.%d.%d.%d", m>>24&0xFF, m>>16&0xFF, m>>8&0xFF, m&0xFF)
}

func logW(tag, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[WIN-%s] %s\n", tag, msg)
}
