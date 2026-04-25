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

type WindowsTUNDevice struct {
	mu     sync.Mutex
	tunDev tun.Device
	wgDev  *device.Device
	uapiLn net.Listener
	name   string
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	if mtu <= 0 {
		mtu = 1420
	}

	fmt.Printf("[WIN-TUN] Creating wintun adapter '%s' mtu=%d\n", name, mtu)

	tunDev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("wintun create: %w", err)
	}

	resolvedName, err := tunDev.Name()
	if err != nil {
		_ = tunDev.Close()
		return nil, fmt.Errorf("wintun name: %w", err)
	}

	fmt.Printf("[WIN-TUN] Adapter created: '%s'\n", resolvedName)
	return &WindowsTUNDevice{tunDev: tunDev, name: resolvedName}, nil
}

func (d *WindowsTUNDevice) Name() string { return d.name }

func (d *WindowsTUNDevice) Configure(ip, cidr string) error {
	fmt.Printf("[WIN-TUN] Configuring %s with IP %s/%s\n", d.name, ip, cidr)

	// Wait for adapter to appear in Windows networking stack
	for i := 0; i < 15; i++ {
		out, _ := exec.Command("netsh", "interface", "show", "interface", d.name).CombinedOutput()
		if strings.Contains(string(out), d.name) || strings.Contains(string(out), "Connected") {
			fmt.Printf("[WIN-TUN] Adapter visible after %d attempts\n", i+1)
			break
		}
		if i == 14 {
			fmt.Printf("[WIN-TUN] WARNING: Adapter may not be visible yet\n")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Ensure adapter is enabled
	_ = exec.Command("netsh", "interface", "set", "interface", d.name, "admin=enabled").Run()
	time.Sleep(300 * time.Millisecond)

	// Set IP address using netsh
	mask := cidrBitsToNetmask(cidr)
	fmt.Printf("[WIN-TUN] Setting IP: netsh interface ip set address name=%s static %s %s\n", d.name, ip, mask)

	// Try multiple times — Windows is slow
	var lastErr error
	for i := 0; i < 10; i++ {
		out, err := exec.Command("netsh", "interface", "ip", "set", "address",
			fmt.Sprintf("name=%s", d.name), "source=static",
			fmt.Sprintf("addr=%s", ip), fmt.Sprintf("mask=%s", mask)).CombinedOutput()
		if err == nil {
			fmt.Printf("[WIN-TUN] IP set successfully\n")
			lastErr = nil
			break
		}
		lastErr = fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
		fmt.Printf("[WIN-TUN] IP set attempt %d failed: %s\n", i+1, strings.TrimSpace(string(out)))
		time.Sleep(1 * time.Second)
	}
	if lastErr != nil {
		// Fallback: try PowerShell
		fmt.Printf("[WIN-TUN] Trying PowerShell fallback...\n")
		bits, _ := strconv.Atoi(strings.TrimPrefix(cidr, "/"))
		if bits == 0 {
			bits = 24
		}
		psCmd := fmt.Sprintf(
			"$idx = (Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue).ifIndex; "+
				"if ($idx) { "+
				"Remove-NetIPAddress -InterfaceIndex $idx -Confirm:$false -ErrorAction SilentlyContinue; "+
				"New-NetIPAddress -InterfaceIndex $idx -IPAddress '%s' -PrefixLength %d -ErrorAction Stop"+
				"}", d.name, ip, bits)
		out, err := exec.Command("powershell", "-Command", psCmd).CombinedOutput()
		if err != nil {
			return fmt.Errorf("configure IP failed (netsh+ps): %s: %w", strings.TrimSpace(string(out)), err)
		}
		fmt.Printf("[WIN-TUN] PowerShell IP set OK\n")
	}

	// Verify IP was set
	time.Sleep(500 * time.Millisecond)
	verifyCmd := fmt.Sprintf("Get-NetIPAddress -InterfaceAlias '%s' -AddressFamily IPv4 | Select-Object -ExpandProperty IPAddress", d.name)
	out, _ := exec.Command("powershell", "-Command", verifyCmd).CombinedOutput()
	fmt.Printf("[WIN-TUN] Current IP on %s: %s\n", d.name, strings.TrimSpace(string(out)))

	return nil
}

func (d *WindowsTUNDevice) SetupWireGuard(privateKeyB64 string, listenPort int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tunDev == nil {
		return fmt.Errorf("setup wg: tun not created")
	}

	privHex, err := b64ToHex(privateKeyB64)
	if err != nil {
		return fmt.Errorf("setup wg: key: %w", err)
	}

	fmt.Printf("[WIN-WG] Starting in-process WireGuard on %s port %d\n", d.name, listenPort)

	logger := device.NewLogger(device.LogLevelVerbose, fmt.Sprintf("[WG/%s] ", d.name))
	wgDev := device.NewDevice(d.tunDev, conn.NewDefaultBind(), logger)

	ipcCfg := fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privHex, listenPort)
	if err := wgDev.IpcSet(ipcCfg); err != nil {
		wgDev.Close()
		return fmt.Errorf("setup wg: ipc set: %w", err)
	}
	if err := wgDev.Up(); err != nil {
		wgDev.Close()
		return fmt.Errorf("setup wg: device up: %w", err)
	}

	d.wgDev = wgDev
	fmt.Printf("[WIN-WG] WireGuard device UP\n")

	// Open firewall for WireGuard UDP port
	openFirewall(listenPort)

	// UAPI listener so wg.exe show can work
	if uapiLn, err := ipc.UAPIListen(d.name); err == nil {
		d.uapiLn = uapiLn
		go func() {
			for {
				c, err := uapiLn.Accept()
				if err != nil {
					return
				}
				go wgDev.IpcHandle(c)
			}
		}()
		fmt.Printf("[WIN-WG] UAPI listener started\n")
	} else {
		fmt.Printf("[WIN-WG] UAPI listen failed (non-fatal): %v\n", err)
	}

	return nil
}

func (d *WindowsTUNDevice) AddWGPeer(publicKeyB64, endpoint, allowedIP string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDev == nil {
		return fmt.Errorf("add peer: wg not init")
	}

	pubHex, err := b64ToHex(publicKeyB64)
	if err != nil {
		return fmt.Errorf("add peer: key: %w", err)
	}
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}

	ipcCfg := fmt.Sprintf(
		"public_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=25\n",
		pubHex, endpoint, allowedIP)

	fmt.Printf("[WIN-WG] Adding peer: pub=%s..%s ep=%s allowed=%s\n",
		publicKeyB64[:8], publicKeyB64[len(publicKeyB64)-4:], endpoint, allowedIP)

	if err := d.wgDev.IpcSet(ipcCfg); err != nil {
		return fmt.Errorf("add peer: ipc: %w", err)
	}

	// Dump current state for debugging
	go func() {
		time.Sleep(2 * time.Second)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.wgDev != nil {
			state, _ := d.wgDev.IpcGet()
			lines := strings.Split(state, "\n")
			for _, l := range lines {
				if strings.Contains(l, "public_key") || strings.Contains(l, "endpoint") ||
					strings.Contains(l, "last_handshake") || strings.Contains(l, "allowed_ip") {
					fmt.Printf("[WIN-WG] State: %s\n", l)
				}
			}
		}
	}()

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
	return d.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
}

func (d *WindowsTUNDevice) Read(buf []byte) (int, error) {
	// When wireguard-go is active, it owns the TUN device reads.
	// Direct reads will conflict. Return error to prevent relay from reading.
	if d.wgDev != nil {
		// WireGuard is handling packets — block here
		select {}
	}
	if d.tunDev == nil {
		return 0, fmt.Errorf("read: device nil")
	}
	pkts := [][]byte{buf}
	sizes := make([]int, 1)
	_, err := d.tunDev.Read(pkts, sizes, 0)
	if err != nil {
		return 0, err
	}
	return sizes[0], nil
}

func (d *WindowsTUNDevice) Write(buf []byte) (int, error) {
	// When wireguard-go is active, it owns the TUN device writes.
	if d.wgDev != nil {
		return len(buf), nil // silently discard — WG handles everything
	}
	if d.tunDev == nil {
		return 0, fmt.Errorf("write: device nil")
	}
	_, err := d.tunDev.Write([][]byte{buf}, 0)
	if err != nil {
		return 0, err
	}
	return len(buf), nil
}

func (d *WindowsTUNDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Printf("[WIN-TUN] Closing %s\n", d.name)

	if d.uapiLn != nil {
		_ = d.uapiLn.Close()
		d.uapiLn = nil
	}
	if d.wgDev != nil {
		d.wgDev.Close()
		d.wgDev = nil
	}
	if d.tunDev != nil {
		err := d.tunDev.Close()
		d.tunDev = nil
		return err
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func b64ToHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", err
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

func cidrBitsToNetmask(cidr string) string {
	bits, err := strconv.Atoi(strings.TrimPrefix(cidr, "/"))
	if err != nil || bits <= 0 || bits > 32 {
		bits = 24
	}
	m := uint32(0xFFFFFFFF) << (32 - bits)
	return fmt.Sprintf("%d.%d.%d.%d", m>>24&0xFF, m>>16&0xFF, m>>8&0xFF, m&0xFF)
}

func openFirewall(port int) {
	ruleName := fmt.Sprintf("QuickTunnel-WG-%d", port)
	// Remove old rule
	_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
		fmt.Sprintf("name=%s", ruleName)).Run()
	// Add UDP inbound
	out, err := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		fmt.Sprintf("name=%s", ruleName),
		"dir=in", "action=allow", "protocol=UDP",
		fmt.Sprintf("localport=%d", port),
		"enable=yes").CombinedOutput()
	if err != nil {
		fmt.Printf("[WIN-FW] Firewall rule add failed: %s\n", strings.TrimSpace(string(out)))
	} else {
		fmt.Printf("[WIN-FW] Firewall opened UDP port %d\n", port)
	}
	// Also allow ICMP (ping)
	_ = exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=QuickTunnel-ICMP",
		"dir=in", "action=allow", "protocol=ICMPv4",
		"enable=yes").Run()
	fmt.Printf("[WIN-FW] ICMP allowed\n")
}
