//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/songgao/water"
)

// LinuxTUNDevice wraps songgao/water for Linux TUN operations.
type LinuxTUNDevice struct {
	iface *water.Interface
	name  string
}

func (d *LinuxTUNDevice) Create(name string, mtu int) error {
	cfg := water.Config{DeviceType: water.TUN}
	if name != "" {
		cfg.PlatformSpecificParams.Name = name
	}

	iface, err := water.New(cfg)
	if err != nil {
		return fmt.Errorf("linux tun create: create interface: %w", err)
	}

	d.iface = iface
	d.name = iface.Name()

	if mtu > 0 {
		if err := SetMTU(d.name, mtu); err != nil {
			_ = iface.Close()
			return fmt.Errorf("linux tun create: set mtu: %w", err)
		}
	}
	return nil
}

func (d *LinuxTUNDevice) Configure(ip string, cidr string) error {
	if d.name == "" {
		return fmt.Errorf("linux tun configure: interface is not created")
	}
	return ConfigureTUN(d.name, ip, cidr)
}

func (d *LinuxTUNDevice) Read(buf []byte) (int, error) {
	if d.iface == nil {
		return 0, fmt.Errorf("linux tun read: interface is not created")
	}
	n, err := d.iface.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("linux tun read: %w", err)
	}
	return n, nil
}

func (d *LinuxTUNDevice) Write(buf []byte) (int, error) {
	if d.iface == nil {
		return 0, fmt.Errorf("linux tun write: interface is not created")
	}
	n, err := d.iface.Write(buf)
	if err != nil {
		return 0, fmt.Errorf("linux tun write: %w", err)
	}
	return n, nil
}

func (d *LinuxTUNDevice) Close() error {
	if d.iface == nil {
		return nil
	}
	if err := d.iface.Close(); err != nil {
		return fmt.Errorf("linux tun close: %w", err)
	}
	return nil
}

func (d *LinuxTUNDevice) Name() string {
	return d.name
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	device := &LinuxTUNDevice{}
	if err := device.Create(name, mtu); err != nil {
		return nil, fmt.Errorf("create tun: %w", err)
	}
	return device, nil
}

func ConfigureTUN(name string, ip string, cidr string) error {
	maskBits, err := maskBitsFromCIDR(cidr)
	if err != nil {
		return fmt.Errorf("configure tun: derive mask bits: %w", err)
	}

	if err := runCommand("ip", "addr", "replace", fmt.Sprintf("%s/%d", ip, maskBits), "dev", name); err != nil {
		return fmt.Errorf("configure tun: assign ip: %w", err)
	}
	if err := runCommand("ip", "link", "set", "dev", name, "up"); err != nil {
		return fmt.Errorf("configure tun: bring interface up: %w", err)
	}
	return nil
}

func SetMTU(name string, mtu int) error {
	if mtu <= 0 {
		return fmt.Errorf("set mtu: mtu must be positive")
	}
	if err := runCommand("ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("set mtu: %w", err)
	}
	return nil
}

func DestroyTUN(name string) error {
	if name == "" {
		return fmt.Errorf("destroy tun: name is required")
	}
	if err := runCommand("ip", "link", "delete", name); err != nil {
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
