//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type LinuxTUNDevice struct {
	name string
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	_ = exec.Command("ip", "link", "delete", name).Run()

	// Best-effort kernel module load for devices where it is not auto-loaded.
	_ = exec.Command("modprobe", "wireguard").Run()

	if out, err := exec.Command("ip", "link", "add", name, "type", "wireguard").CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "Operation not supported") || strings.Contains(msg, "Unknown device type") {
			return nil, fmt.Errorf("ip link add: %s: wireguard kernel support missing; install wireguard and load module (`sudo modprobe wireguard`)", msg)
		}
		return nil, fmt.Errorf("ip link add: %s: %w", msg, err)
	}
	if err := exec.Command("ip", "link", "set", name, "mtu", strconv.Itoa(mtu), "up").Run(); err != nil {
		return nil, fmt.Errorf("ip link set: %w", err)
	}
	return &LinuxTUNDevice{name: name}, nil
}

func (d *LinuxTUNDevice) Name() string { return d.name }

func (d *LinuxTUNDevice) Configure(ip, cidr string) error {
	// cidr can be "16", "/16", or "10.7.0.0/16"
	// We need just the prefix length bits
	prefixLen := "24" // default
	if strings.Contains(cidr, "/") {
		parts := strings.Split(cidr, "/")
		prefixLen = parts[len(parts)-1]
	} else {
		// cidr is just the number like "16"
		prefixLen = cidr
	}

	addr := ip + "/" + prefixLen
	fmt.Printf("[TUN] ip addr add %s dev %s\n", addr, d.name)

	out, err := exec.Command("ip", "addr", "add", addr, "dev", d.name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip addr add %s: %s: %w", addr, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (d *LinuxTUNDevice) Read(buf []byte) (int, error) {
	return 0, fmt.Errorf("direct read not supported on WG kernel device")
}

func (d *LinuxTUNDevice) Write(buf []byte) (int, error) {
	return 0, fmt.Errorf("direct write not supported on WG kernel device")
}

func (d *LinuxTUNDevice) Close() error {
	return exec.Command("ip", "link", "delete", d.name).Run()
}
