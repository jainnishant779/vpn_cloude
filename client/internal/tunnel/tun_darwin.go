//go:build darwin
// +build darwin

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"

	"golang.zx2c4.com/wireguard/tun"
)

// DarwinTUNDevice wraps wireguard-go tun device for macOS.
type DarwinTUNDevice struct {
	dev  tun.Device
	name string
}

func (d *DarwinTUNDevice) Create(name string, mtu int) error {
	if mtu <= 0 {
		mtu = 1420
	}

	dev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return fmt.Errorf("darwin tun create: create device: %w", err)
	}

	resolvedName, err := dev.Name()
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("darwin tun create: read name: %w", err)
	}

	d.dev = dev
	d.name = resolvedName
	return nil
}

func (d *DarwinTUNDevice) Configure(ip string, cidr string) error {
	if d.name == "" {
		return fmt.Errorf("darwin tun configure: interface is not created")
	}
	return ConfigureTUN(d.name, ip, cidr)
}

func (d *DarwinTUNDevice) Read(buf []byte) (int, error) {
	if d.dev == nil {
		return 0, fmt.Errorf("darwin tun read: device is not created")
	}

	packets := [][]byte{buf}
	sizes := make([]int, 1)
	_, err := d.dev.Read(packets, sizes, 0)
	if err != nil {
		return 0, fmt.Errorf("darwin tun read: %w", err)
	}
	return sizes[0], nil
}

func (d *DarwinTUNDevice) Write(buf []byte) (int, error) {
	if d.dev == nil {
		return 0, fmt.Errorf("darwin tun write: device is not created")
	}

	if _, err := d.dev.Write([][]byte{buf}, 0); err != nil {
		return 0, fmt.Errorf("darwin tun write: %w", err)
	}
	return len(buf), nil
}

func (d *DarwinTUNDevice) Close() error {
	if d.dev == nil {
		return nil
	}
	if err := d.dev.Close(); err != nil {
		return fmt.Errorf("darwin tun close: %w", err)
	}
	return nil
}

func (d *DarwinTUNDevice) Name() string {
	return d.name
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	device := &DarwinTUNDevice{}
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

	if err := runCommand("ifconfig", name, "inet", ip, ip, "netmask", mask, "up"); err != nil {
		return fmt.Errorf("configure tun: set address and up: %w", err)
	}
	return nil
}

func SetMTU(name string, mtu int) error {
	if mtu <= 0 {
		return fmt.Errorf("set mtu: mtu must be positive")
	}
	if err := runCommand("ifconfig", name, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("set mtu: %w", err)
	}
	return nil
}

func DestroyTUN(name string) error {
	if name == "" {
		return fmt.Errorf("destroy tun: name is required")
	}
	if err := runCommand("ifconfig", name, "down"); err != nil {
		return fmt.Errorf("destroy tun: %w", err)
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
