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
	name       string
	wgGoProc   *os.Process // wireguard-go process (userspace fallback)
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

// findWireGuardGo looks for wireguard-go or boringtun in PATH and common locations.
func findWireGuardGo() string {
	for _, name := range []string{"wireguard-go", "boringtun", "boringtun-cli"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	// Check common install locations
	for _, path := range []string{
		"/usr/bin/wireguard-go",
		"/usr/local/bin/wireguard-go",
		"/usr/bin/boringtun",
		"/usr/local/bin/boringtun",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// installWireGuardGo attempts to download and install wireguard-go.
func installWireGuardGo() (string, error) {
	// Try apt first
	if _, err := exec.LookPath("apt-get"); err == nil {
		_ = exec.Command("apt-get", "update", "-qq").Run()
		if err := exec.Command("apt-get", "install", "-y", "-qq", "wireguard-go").Run(); err == nil {
			if path, err := exec.LookPath("wireguard-go"); err == nil {
				return path, nil
			}
		}
	}

	// Try go install if go is available
	if goPath, err := exec.LookPath("go"); err == nil {
		cmd := exec.Command(goPath, "install", "golang.zx2c4.com/wireguard-go@latest")
		cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
		if err := cmd.Run(); err == nil {
			return "/usr/local/bin/wireguard-go", nil
		}
	}

	return "", fmt.Errorf("wireguard-go not found and could not be installed")
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
		// Log but continue to userspace fallback
		_ = out
	}

	// Fallback: use wireguard-go (userspace WireGuard)
	wgGoPath := findWireGuardGo()
	if wgGoPath == "" {
		var err error
		wgGoPath, err = installWireGuardGo()
		if err != nil {
			return fmt.Errorf("create wireguard device: kernel module unavailable and %w", err)
		}
	}

	// Create TUN device first (wireguard-go needs it)
	out, err := exec.Command("ip", "tuntap", "add", "mode", "tun", name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") {
		// Some systems need a different approach
		_ = out
	}

	// Start wireguard-go with the interface name
	cmd := exec.Command(wgGoPath, name)
	cmd.Env = append(os.Environ(),
		"WG_TUN_FD=",  // Let wireguard-go create its own TUN
		"LOG_LEVEL=info",
	)
	// Delete the tuntap device — wireguard-go creates its own
	_ = exec.Command("ip", "link", "delete", name).Run()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("create wireguard device: start wireguard-go: %w", err)
	}

	d.wgGoProc = cmd.Process
	d.name = name
	d.isUserspace = true

	// Wait briefly for the interface to appear
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := exec.Command("ip", "link", "show", name).CombinedOutput(); err == nil {
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
