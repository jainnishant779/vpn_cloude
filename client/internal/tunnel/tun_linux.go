//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type LinuxWGDevice struct {
	name string
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
	return nil
}

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	_ = exec.Command("ip", "link", "delete", name).Run()
	out, err := exec.Command("ip", "link", "add", name, "type", "wireguard").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("linux tun create: create interface: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if mtu > 0 {
		_ = exec.Command("ip", "link", "set", name, "mtu", strconv.Itoa(mtu)).Run()
	}
	return &LinuxWGDevice{name: name}, nil
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

func runCommand(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run command %s %v: %w (output: %s)", command, args, err, string(output))
	}
	return nil
}
