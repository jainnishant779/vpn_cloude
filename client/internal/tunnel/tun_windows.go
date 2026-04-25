//go:build windows
// +build windows

package tunnel

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
)

// WindowsTUNDevice wraps wireguard-go for Windows using wintun + in-process WG device.
// This gives full kernel-level WireGuard without needing wg.exe to work.
type WindowsTUNDevice struct {
	mu      sync.Mutex
	tunDev  tun.Device
	wgDev   *device.Device
	uapiLn  net.Listener
	name    string
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
		return fmt.Errorf("windows tun create: create device: %w", err)
	}

	realName, err := tunDev.Name()
	if err != nil {
		_ = tunDev.Close()
		return fmt.Errorf("windows tun create: read name: %w", err)
	}
	d.tunDev = tunDev
	d.name   = resolvedName
	return nil
}

func (d *WindowsTUNDevice) Configure(ip, cidr string) error {
	if d.name == "" {
		return fmt.Errorf("windows tun configure: interface not created")
	}
	return ConfigureTUN(d.name, ip, cidr)
}

// SetupWireGuard initialises the in-process WireGuard device.
// Called by wireguard.go after Start() to configure private key + listen port.
// This replaces the external wg.exe approach — everything runs in-process.
func (d *WindowsTUNDevice) SetupWireGuard(privateKeyB64 string, listenPort int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tunDev == nil {
		return fmt.Errorf("setup wireguard: tun device not created")
	}

	privKeyHex, err := b64ToHex(privateKeyB64)
	if err != nil {
		return fmt.Errorf("setup wireguard: parse private key: %w", err)
	}

	logger := device.NewLogger(device.LogLevelError, fmt.Sprintf("[%s] ", d.name))
	wgDev  := device.NewDevice(d.tunDev, conn.NewDefaultBind(), logger)

	ipcConf := fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privKeyHex, listenPort)
	if err := wgDev.IpcSet(ipcConf); err != nil {
		wgDev.Close()
		return fmt.Errorf("setup wireguard: ipc set: %w", err)
	}
	if err := wgDev.Up(); err != nil {
		wgDev.Close()
		return fmt.Errorf("setup wireguard: device up: %w", err)
	}

	d.wgDev = wgDev

	// Start UAPI listener so wg.exe can also read interface state
	uapiLn, err := ipc.UAPIListen(d.name)
	if err == nil {
		d.uapiLn = uapiLn
		go func() {
			for {
				conn, err := uapiLn.Accept()
				if err != nil {
					return
				}
				go wgDev.IpcHandle(conn)
			}
		}()
	}

	return nil
}

// AddWGPeer adds a WireGuard peer in-process (no wg.exe needed).
func (d *WindowsTUNDevice) AddWGPeer(publicKeyB64, endpoint, allowedIP string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDev == nil {
		return fmt.Errorf("add wg peer: wireguard not initialized")
	}

	pubKeyHex, err := b64ToHex(publicKeyB64)
	if err != nil {
		return fmt.Errorf("add wg peer: parse public key: %w", err)
	}
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}

	ipcConf := fmt.Sprintf(
		"public_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=25\n",
		pubKeyHex, endpoint, allowedIP,
	)
	if err := d.wgDev.IpcSet(ipcConf); err != nil {
		return fmt.Errorf("add wg peer: ipc set: %w", err)
	}
	return nil
}

// RemoveWGPeer removes a WireGuard peer in-process.
func (d *WindowsTUNDevice) RemoveWGPeer(publicKeyB64 string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDev == nil {
		return nil
	}
	pubKeyHex, err := b64ToHex(publicKeyB64)
	if err != nil {
		return fmt.Errorf("remove wg peer: parse public key: %w", err)
	}
	return d.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubKeyHex))
}

func (d *WindowsTUNDevice) Read(buf []byte) (int, error) {
	if d.tunDev == nil {
		return 0, fmt.Errorf("windows tun read: device not created")
	}
	packets := [][]byte{buf}
	sizes   := make([]int, 1)
	_, err  := d.tunDev.Read(packets, sizes, 0)
	if err != nil {
		return 0, fmt.Errorf("windows tun read: %w", err)
	}
	return sizes[0], nil
}

func (d *WindowsTUNDevice) Write(buf []byte) (int, error) {
	if d.tunDev == nil {
		return 0, fmt.Errorf("windows tun write: device not created")
	}
	if _, err := d.tunDev.Write([][]byte{buf}, 0); err != nil {
		return 0, fmt.Errorf("windows tun write: %w", err)
	}
	return len(buf), nil
}

func (d *WindowsTUNDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.uapiLn != nil {
		_ = d.uapiLn.Close()
	}

	// Close WG device
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

func (d *WindowsTUNDevice) Name() string { return d.name }

// ── TUNDevice interface ──────────────────────────────────────────────────────

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	dev := &WindowsTUNDevice{}
	if err := dev.Create(name, mtu); err != nil {
		return nil, fmt.Errorf("create tun: %w", err)
	}
	return dev, nil
}

// ── Windows-specific helpers ─────────────────────────────────────────────────

func ConfigureTUN(name, ip, cidr string) error {
	mask, err := maskStringFromCIDR(cidr)
	if err != nil {
		return fmt.Errorf("configure tun: derive mask: %w", err)
	}
	if err := runNetsh("interface", "ip", "set", "address",
		"name="+name, "source=static", "addr="+ip, "mask="+mask); err != nil {
		return fmt.Errorf("configure tun: set ip: %w", err)
	}
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

func runNetsh(args ...string) error {
	out, err := exec.Command("netsh", args...).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") && !strings.Contains(string(out), "already") {
		return fmt.Errorf("netsh %v: %w (out: %s)", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// b64ToHex converts a base64-encoded 32-byte WireGuard key to lowercase hex.
// WireGuard's IPC protocol (UAPI) requires hex-encoded keys.
func b64ToHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("b64ToHex: decode: %w", err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("b64ToHex: expected 32 bytes, got %d", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

func cidrToBits(cidr string) int {
	s := strings.TrimPrefix(cidr, "/")
	bits, err := strconv.Atoi(s)
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
