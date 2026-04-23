//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"

	"golang.zx2c4.com/wireguard/tun"
)

// WindowsTUNDevice wraps wireguard-go tun device for Windows.
type WindowsTUNDevice struct {
	dev  tun.Device
	name string
}

func (d *WindowsTUNDevice) Create(name string, mtu int) error {
	if mtu <= 0 {
		mtu = 1420
	}

	dev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return fmt.Errorf("windows tun create: create device: %w", err)
	}

	resolvedName, err := dev.Name()
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("windows tun create: read name: %w", err)
	}

	d.dev = dev
	d.name = resolvedName
	return nil
}

func (d *WindowsTUNDevice) Configure(ip string, cidr string) error {
	if d.name == "" {
		return fmt.Errorf("windows tun configure: interface is not created")
	}
	return ConfigureTUN(d.name, ip, cidr)
}

func (d *WindowsTUNDevice) Read(buf []byte) (int, error) {
	if d.dev == nil {
		return 0, fmt.Errorf("windows tun read: device is not created")
	}

	packets := [][]byte{buf}
	sizes := make([]int, 1)
	_, err := d.dev.Read(packets, sizes, 0)
	if err != nil {
		return 0, fmt.Errorf("windows tun read: %w", err)
	}
	return sizes[0], nil
}

func (d *WindowsTUNDevice) Write(buf []byte) (int, error) {
	if d.dev == nil {
		return 0, fmt.Errorf("windows tun write: device is not created")
	}

	if _, err := d.dev.Write([][]byte{buf}, 0); err != nil {
		return 0, fmt.Errorf("windows tun write: %w", err)
	}
	return len(buf), nil
}

func (d *WindowsTUNDevice) Close() error {
	if d.dev == nil {
		return nil
	}
	if err := d.dev.Close(); err != nil {
		return fmt.Errorf("windows tun close: %w", err)
	}
	return nil
}

func (d *WindowsTUNDevice) Name() string {
	return d.name
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	device := &WindowsTUNDevice{}
	if err := device.Create(name, mtu); err != nil {
		return nil, fmt.Errorf("create tun: %w", err)
	}
	return device, nil
}

func ConfigureTUN(name string, ip string, cidr string) error {
	mask, err := maskStringFromCIDR(cidr)
	if err != nil {
		return fmt.Errorf("configure tun: derive mask: %w", err)
	}

	if err := runNetsh("interface", "ip", "set", "address", name, "static", ip, mask); err != nil {
		return fmt.Errorf("configure tun: set ipv4 address: %w", err)
	}
	return nil
}

func SetMTU(name string, mtu int) error {
	if mtu <= 0 {
		return fmt.Errorf("set mtu: mtu must be positive")
	}
	if err := runNetsh("interface", "ipv4", "set", "subinterface", name, "mtu="+strconv.Itoa(mtu), "store=active"); err != nil {
		return fmt.Errorf("set mtu: %w", err)
	}
	return nil
}

func DestroyTUN(name string) error {
	if name == "" {
		return fmt.Errorf("destroy tun: name is required")
	}
	if err := runNetsh("interface", "set", "interface", "name="+name, "admin=disabled"); err != nil {
		return fmt.Errorf("destroy tun: disable interface: %w", err)
	}
	return nil
}

func runNetsh(args ...string) error {
	cmd := exec.Command("netsh", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run netsh %v: %w (output: %s)", args, err, string(output))
	}
	return nil
}
