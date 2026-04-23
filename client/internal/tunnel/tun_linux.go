//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// LinuxWGDevice uses the kernel WireGuard module directly via ip/wg commands.
// This gives full TCP/UDP support — exactly like ZeroTier.
type LinuxWGDevice struct {
	name string
}

func (d *LinuxWGDevice) Create(name string, mtu int) error {
	// Always delete first to avoid "device busy" on restart
	_ = exec.Command("ip", "link", "delete", name).Run()

	// Create a REAL WireGuard interface (not raw TUN)
	// This requires: sudo modprobe wireguard  (or kernel 5.6+)
	out, err := exec.Command("ip", "link", "add", name, "type", "wireguard").CombinedOutput()
	if err != nil {
		return fmt.Errorf("linux tun create: create interface: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if mtu > 0 {
		_ = exec.Command("ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu)).Run()
	}

	d.name = name
	return nil
}

func (d *LinuxWGDevice) Configure(ip string, cidr string) error {
	if d.name == "" {
		return fmt.Errorf("linux wg configure: interface not created")
	}

	maskBits, err := maskBitsFromCIDR(cidr)
	if err != nil {
		return fmt.Errorf("linux wg configure: %w", err)
	}

	addr := fmt.Sprintf("%s/%d", ip, maskBits)
	out, err := exec.Command("ip", "addr", "replace", addr, "dev", d.name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("linux wg configure: assign ip: %w: %s", err, strings.TrimSpace(string(out)))
	}

	out, err = exec.Command("ip", "link", "set", "dev", d.name, "up").CombinedOutput()
	if err != nil {
		return fmt.Errorf("linux wg configure: bring up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Enable IP forwarding so packets route through tunnel
	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()

	return nil
}

// Read/Write are no-ops for kernel WireGuard — kernel handles all packet I/O
func (d *LinuxWGDevice) Read(buf []byte) (int, error)  { return 0, nil }
func (d *LinuxWGDevice) Write(buf []byte) (int, error) { return len(buf), nil }

func (d *LinuxWGDevice) Close() error {
	if d.name == "" {
		return nil
	}
	_ = exec.Command("ip", "link", "delete", d.name).Run()
	return nil
}

func (d *LinuxWGDevice) Name() string { return d.name }

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	device := &LinuxWGDevice{}
	if err := device.Create(name, mtu); err != nil {
		return nil, fmt.Errorf("create tun: %w", err)
	}
	return device, nil
}

// ConfigureTUN kept for compatibility
func ConfigureTUN(name string, ip string, cidr string) error {
	d := &LinuxWGDevice{name: name}
	return d.Configure(ip, cidr)
}

func SetMTU(name string, mtu int) error {
	if mtu <= 0 {
		return fmt.Errorf("set mtu: mtu must be positive")
	}
	out, err := exec.Command("ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("set mtu: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func DestroyTUN(name string) error {
	if name == "" {
		return fmt.Errorf("destroy tun: name is required")
	}
	out, err := exec.Command("ip", "link", "delete", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("destroy tun: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runCommand(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run command %s %v: %w (output: %s)", command, args, err, string(output))
	}
	return nil
}