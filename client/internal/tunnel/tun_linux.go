//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
)

type LinuxTUNDevice struct {
	name string
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	_ = exec.Command("ip", "link", "delete", name).Run()

	if err := exec.Command("ip", "link", "add", name, "type", "wireguard").Run(); err != nil {
		return nil, fmt.Errorf("ip link add: %w", err)
	}
	if err := exec.Command("ip", "link", "set", name, "mtu", strconv.Itoa(mtu), "up").Run(); err != nil {
		return nil, fmt.Errorf("ip link set: %w", err)
	}
	return &LinuxTUNDevice{name: name}, nil
}

func (d *LinuxTUNDevice) Name() string { return d.name }

func (d *LinuxTUNDevice) Configure(ip, cidr string) error {
	addr := ip + "/" + cidr
	out, err := exec.Command("ip", "addr", "add", addr, "dev", d.name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip addr add %s: %s: %w", addr, string(out), err)
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
