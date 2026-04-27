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

// WindowsTUNDevice runs wireguard-go in-process on top of wintun.
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
	logW("CFG", "Configuring %s with %s/%s", d.name, ip, cidr)

	if err := d.waitForAdapter(); err != nil {
		return err
	}
	d.enableAdapter()
	if err := d.setIPAddress(ip, cidr); err != nil {
		return err
	}
	d.setMTU(1420)
	d.setMetric(10)
	d.verifyConfig()
	return nil
}

func (d *WindowsTUNDevice) SetupWireGuard(privateKeyB64 string, listenPort int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tunDev == nil {
		return fmt.Errorf("wg setup: tun is nil")
	}

	d.port = listenPort
	privHex, err := b64ToHex(privateKeyB64)
	if err != nil {
		return fmt.Errorf("wg setup: invalid private key: %w", err)
	}

	logW("WG", "Starting WireGuard on %s port %d", d.name, listenPort)
	logger := device.NewLogger(device.LogLevelError, fmt.Sprintf("[WG/%s] ", d.name))
	wgDev := device.NewDevice(d.tunDev, conn.NewDefaultBind(), logger)

	ipcCfg := fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privHex, listenPort)
	if err := wgDev.IpcSet(ipcCfg); err != nil {
		wgDev.Close()
		return fmt.Errorf("wg setup: ipc set: %w", err)
	}
	if err := wgDev.Up(); err != nil {
		wgDev.Close()
		return fmt.Errorf("wg setup: device up: %w", err)
	}

	d.wgDev = wgDev
	logW("WG", "Device up (in-process WireGuard active)")

	d.openFirewall(listenPort)
	d.startUAPI()
	go d.monitorHandshakes()
	return nil
}

func (d *WindowsTUNDevice) AddWGPeer(publicKeyB64, endpoint, allowedIP string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDev == nil {
		return fmt.Errorf("add peer: wg is not initialized")
	}

	pubHex, err := b64ToHex(publicKeyB64)
	if err != nil {
		return fmt.Errorf("add peer: invalid public key: %w", err)
	}
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}

	logW("PEER", "Adding peer %s.. endpoint=%s allowed=%s", shortKey(publicKeyB64), endpoint, allowedIP)

	// Replace stale entry first.
	_ = d.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
	time.Sleep(50 * time.Millisecond)

	ipcCfg := fmt.Sprintf(
		"public_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=15\n",
		pubHex, endpoint, allowedIP,
	)
	if err := d.wgDev.IpcSet(ipcCfg); err != nil {
		return fmt.Errorf("add peer: ipc set: %w", err)
	}

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
		return fmt.Errorf("remove peer: invalid public key: %w", err)
	}
	return d.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
}

func (d *WindowsTUNDevice) Read(buf []byte) (int, error) {
	// If WireGuard is active, it owns the TUN I/O path.
	if d.wgDev != nil {
		select {}
	}
	if d.tunDev == nil {
		return 0, fmt.Errorf("read: tun device is nil")
	}
	pkts := [][]byte{buf}
	sizes := make([]int, 1)
	_, err := d.tunDev.Read(pkts, sizes, 0)
	return sizes[0], err
}

func (d *WindowsTUNDevice) Write(buf []byte) (int, error) {
	// If WireGuard is active, kernel/userspace path handles packet writes.
	if d.wgDev != nil {
		return len(buf), nil
	}
	if d.tunDev == nil {
		return 0, fmt.Errorf("write: tun device is nil")
	}
	_, err := d.tunDev.Write([][]byte{buf}, 0)
	return len(buf), err
}

func (d *WindowsTUNDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	logW("TUN", "Closing %s", d.name)

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

	d.cleanFirewall()
	return nil
}

func (d *WindowsTUNDevice) waitForAdapter() error {
	logW("CFG", "Waiting for adapter '%s'...", d.name)
	for i := 0; i < 30; i++ {
		out, err := exec.Command("powershell", "-Command",
			fmt.Sprintf("(Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue).Status", d.name)).CombinedOutput()
		status := strings.TrimSpace(string(out))
		if err == nil && (status == "Up" || status == "Disconnected" || status == "Not Present") {
			logW("CFG", "Adapter ready: status=%s (attempt=%d)", status, i+1)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	logW("CFG", "Adapter wait timeout, continuing")
	return nil
}

func (d *WindowsTUNDevice) enableAdapter() {
	_ = exec.Command("powershell", "-Command",
		fmt.Sprintf("Enable-NetAdapter -Name '%s' -Confirm:$false -ErrorAction SilentlyContinue", d.name)).Run()
	time.Sleep(200 * time.Millisecond)
}

func (d *WindowsTUNDevice) setIPAddress(ip, cidr string) error {
	bits := cidrToBits(cidr)
	logW("CFG", "Setting IP %s/%d on %s", ip, bits, d.name)

	psCmd := fmt.Sprintf(
		"$a = Get-NetAdapter -Name '%s' -ErrorAction Stop; "+
			"Remove-NetIPAddress -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue; "+
			"Remove-NetRoute -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue; "+
			"New-NetIPAddress -InterfaceIndex $a.ifIndex -IPAddress '%s' -PrefixLength %d -PolicyStore ActiveStore -ErrorAction Stop",
		d.name, ip, bits,
	)
	out, err := exec.Command("powershell", "-Command", psCmd).CombinedOutput()
	if err == nil {
		return nil
	}
	logW("CFG", "PowerShell set IP failed: %s", strings.TrimSpace(string(out)))

	mask := bitsToNetmask(bits)
	for i := 0; i < 5; i++ {
		out, err := exec.Command("netsh", "interface", "ip", "set", "address",
			fmt.Sprintf("name=%s", d.name), "source=static",
			fmt.Sprintf("addr=%s", ip), fmt.Sprintf("mask=%s", mask)).CombinedOutput()
		if err == nil {
			return nil
		}
		logW("CFG", "netsh set IP attempt=%d failed: %s", i+1, strings.TrimSpace(string(out)))
		time.Sleep(1 * time.Second)
	}

	out, err = exec.Command("netsh", "interface", "ip", "add", "address",
		fmt.Sprintf("name=%s", d.name),
		fmt.Sprintf("addr=%s", ip),
		fmt.Sprintf("mask=%s", mask)).CombinedOutput()
	if err == nil {
		return nil
	}

	return fmt.Errorf("all IP methods failed: %s", strings.TrimSpace(string(out)))
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
	out, _ := exec.Command("powershell", "-Command",
		fmt.Sprintf("Get-NetIPAddress -InterfaceAlias '%s' -AddressFamily IPv4 -ErrorAction SilentlyContinue | Format-Table IPAddress,PrefixLength -AutoSize", d.name)).CombinedOutput()
	logW("CFG", "IP verify:\n%s", strings.TrimSpace(string(out)))
}

func (d *WindowsTUNDevice) openFirewall(port int) {
	rules := []struct {
		name, proto, dir string
		lport            int
	}{
		{fmt.Sprintf("QT-WG-UDP-IN-%d", port), "UDP", "in", port},
		{fmt.Sprintf("QT-WG-UDP-OUT-%d", port), "UDP", "out", port},
		{"QT-ICMP-IN", "ICMPv4", "in", 0},
		{"QT-ICMP-OUT", "ICMPv4", "out", 0},
	}

	for _, rule := range rules {
		_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
			fmt.Sprintf("name=%s", rule.name)).Run()

		args := []string{"advfirewall", "firewall", "add", "rule",
			fmt.Sprintf("name=%s", rule.name),
			fmt.Sprintf("dir=%s", rule.dir),
			"action=allow",
			fmt.Sprintf("protocol=%s", rule.proto),
			"enable=yes",
		}
		if rule.lport > 0 {
			args = append(args, fmt.Sprintf("localport=%d", rule.lport))
		}
		if out, err := exec.Command("netsh", args...).CombinedOutput(); err != nil {
			logW("FW", "Rule %s failed: %s", rule.name, strings.TrimSpace(string(out)))
		}
	}
}

func (d *WindowsTUNDevice) cleanFirewall() {
	names := []string{"QT-WG-UDP-IN-", "QT-WG-UDP-OUT-", "QT-ICMP-IN", "QT-ICMP-OUT"}
	for _, name := range names {
		_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
			fmt.Sprintf("name=%s", name)).Run()
	}
}

func (d *WindowsTUNDevice) startUAPI() {
	uapiLn, err := ipc.UAPIListen(d.name)
	if err != nil {
		logW("UAPI", "Listen failed: %v", err)
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
}

func (d *WindowsTUNDevice) monitorHandshakes() {
	time.Sleep(3 * time.Second)
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
		for _, line := range lines {
			if strings.HasPrefix(line, "last_handshake_time_sec=") {
				val := strings.TrimPrefix(line, "last_handshake_time_sec=")
				if sec, _ := strconv.ParseInt(val, 10, 64); sec > 0 {
					t := time.Unix(sec, 0)
					logW("WG", "Handshake OK at %s", t.Format(time.RFC3339))
					return
				}
			}
		}

		if i < 9 {
			time.Sleep(3 * time.Second)
		}
	}
	logW("WG", "No handshake after 30s")
}

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
		return "", fmt.Errorf("expected 32-byte key, got %d", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

func cidrToBits(cidr string) int {
	// Supports formats: "16", "/16", "10.7.0.0/16".
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
	mask := uint32(0xFFFFFFFF) << (32 - bits)
	return fmt.Sprintf("%d.%d.%d.%d", mask>>24&0xFF, mask>>16&0xFF, mask>>8&0xFF, mask&0xFF)
}

func shortKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8]
}

func logW(tag, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[WIN-%s] %s\n", tag, msg)
}
