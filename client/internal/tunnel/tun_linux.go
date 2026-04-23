//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type LinuxWGDevice struct {
	name        string
	wgGoProc    *os.Process // wireguard-go process (userspace fallback)
	isUserspace bool
}

// hasWireGuardKernelModule checks if the kernel WireGuard module is available.
func hasWireGuardKernelModule() bool {
	// Check if already loaded
	if data, err := os.ReadFile("/proc/modules"); err == nil {
		if strings.Contains(string(data), "wireguard") {
			return true
		}
	}
	// Try to load it
	if err := exec.Command("modprobe", "wireguard").Run(); err == nil {
		return true
	}
	return false
}

// resolveWireGuardGo finds the wireguard-go binary by checking multiple locations.
func resolveWireGuardGo() string {
	// Check explicit known paths first (most reliable under sudo)
	candidates := []string{
		"/usr/bin/wireguard-go",
		"/usr/local/bin/wireguard-go",
		"/usr/sbin/wireguard-go",
		"/snap/bin/wireguard-go",
		"/usr/bin/boringtun",
		"/usr/local/bin/boringtun",
		"/usr/bin/boringtun-cli",
		"/usr/local/bin/boringtun-cli",
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	// Try PATH-based lookup as fallback
	for _, name := range []string{"wireguard-go", "boringtun", "boringtun-cli"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// tryInstallWireGuardGo attempts to install wireguard-go via package manager.
func tryInstallWireGuardGo() string {
	// Try apt
	if _, err := exec.LookPath("apt-get"); err == nil {
		_ = exec.Command("apt-get", "update", "-qq").Run()
		_ = exec.Command("apt-get", "install", "-y", "-qq", "wireguard-go").Run()
		// Check the known path immediately
		if info, err := os.Stat("/usr/bin/wireguard-go"); err == nil && !info.IsDir() {
			return "/usr/bin/wireguard-go"
		}
	}
	// Try go install if go is available
	for _, goPath := range []string{"/usr/local/go/bin/go", "/usr/bin/go"} {
		if _, err := os.Stat(goPath); err == nil {
			cmd := exec.Command(goPath, "install", "golang.zx2c4.com/wireguard-go@latest")
			cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin", "GOPATH=/tmp/gopath")
			if err := cmd.Run(); err == nil {
				if info, err := os.Stat("/usr/local/bin/wireguard-go"); err == nil && !info.IsDir() {
					return "/usr/local/bin/wireguard-go"
				}
			}
		}
	}
	return ""
}

func (d *LinuxWGDevice) Create(name string, mtu int) error {
	// Clean up any existing interface
	_ = exec.Command("ip", "link", "delete", name).Run()

	// Try kernel module first (fastest)
	if hasWireGuardKernelModule() {
		out, err := exec.Command("ip", "link", "add", name, "type", "wireguard").CombinedOutput()
		if err == nil {
			if mtu > 0 {
				_ = exec.Command("ip", "link", "set", name, "mtu", strconv.Itoa(mtu)).Run()
			}
			d.name = name
			d.isUserspace = false
			return nil
		}
		_ = out // kernel module loaded but ip link failed — continue to userspace
	}

	// Fallback: use wireguard-go (userspace WireGuard)
	wgGoPath := resolveWireGuardGo()
	if wgGoPath == "" {
		wgGoPath = tryInstallWireGuardGo()
	}
	if wgGoPath == "" {
		return fmt.Errorf("create wireguard device: WireGuard kernel module unavailable and wireguard-go not found.\n" +
			"  Fix: sudo apt install wireguard-go\n" +
			"  Or:  download Go 1.22+ and run: sudo GOBIN=/usr/local/bin go install golang.zx2c4.com/wireguard-go@latest")
	}

	// Start wireguard-go — it creates its own TUN interface
	cmd := exec.Command(wgGoPath, name)
	cmd.Env = append(os.Environ(), "LOG_LEVEL=info")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("create wireguard device: start wireguard-go (%s): %w", wgGoPath, err)
	}

	d.wgGoProc = cmd.Process
	d.name = name
	d.isUserspace = true

	// Wait for the interface to appear (wireguard-go creates it)
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if out, err := exec.Command("ip", "link", "show", name).CombinedOutput(); err == nil {
			_ = out
			break
		}
	}

	if mtu > 0 {
		_ = exec.Command("ip", "link", "set", name, "mtu", strconv.Itoa(mtu)).Run()
	}

	return nil
}

func (d *LinuxWGDevice) Configure(ip string, cidr string) error {
	maskBits, err := maskBitsFromCIDR(cidr)
	if err != nil {
		return fmt.Errorf("configure: %w", err)
	}
	addr := fmt.Sprintf("%s/%d", ip, maskBits)
	out, err := exec.Command("ip", "addr", "add", addr, "dev", d.name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") {
		return fmt.Errorf("configure: assign ip: %w: %s", err, string(out))
	}
	out2, err2 := exec.Command("ip", "link", "set", d.name, "up").CombinedOutput()
	if err2 != nil {
		return fmt.Errorf("configure: bring up: %w: %s", err2, string(out2))
	}
	return nil
}

func (d *LinuxWGDevice) Read(buf []byte) (int, error)  { return 0, nil }
func (d *LinuxWGDevice) Write(buf []byte) (int, error) { return len(buf), nil }
func (d *LinuxWGDevice) Name() string                  { return d.name }
func (d *LinuxWGDevice) Close() error {
	_ = exec.Command("ip", "link", "delete", d.name).Run()
	if d.wgGoProc != nil {
		_ = d.wgGoProc.Kill()
		_, _ = d.wgGoProc.Wait()
	}
	return nil
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	dev := &LinuxWGDevice{}
	if err := dev.Create(name, mtu); err != nil {
		return nil, fmt.Errorf("linux tun create: %w", err)
	}
	return dev, nil
}

func ConfigureTUN(name string, ip string, cidr string) error {
	return (&LinuxWGDevice{name: name}).Configure(ip, cidr)
}

func SetMTU(name string, mtu int) error {
	return exec.Command("ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu)).Run()
}

func DestroyTUN(name string) error {
	return exec.Command("ip", "link", "delete", name).Run()
}
