package tunnel

import (
	"fmt"
	"os/exec"
	"strings"
)

type linuxTUN struct {
    name string
}

func (t *linuxTUN) Create(name string, mtu int) error {
    // Delete if exists
    _ = exec.Command("ip", "link", "delete", name).Run()

    // Create real WireGuard interface (not raw TUN)
    out, err := exec.Command("ip", "link", "add", name, "type", "wireguard").CombinedOutput()
    if err != nil {
        return fmt.Errorf("linux tun create: create interface: %w: %s", err, strings.TrimSpace(string(out)))
    }

    // Set MTU
    _ = exec.Command("ip", "link", "set", name, "mtu", fmt.Sprintf("%d", mtu)).Run()

    t.name = name
    return nil
}

func (t *linuxTUN) Name() string { return t.name }

func (t *linuxTUN) Configure(virtualIP, cidr string) error {
	// Parse prefix length from cidr
	_, ipNet, err := parseIPNet(cidr)
	if err != nil {
		return fmt.Errorf("configure tun: parse cidr: %w", err)
	}
	ones, _ := ipNet.Mask.Size()

	// Assign IP
	addr := fmt.Sprintf("%s/%d", virtualIP, ones)
	out, err := exec.Command("ip", "addr", "add", addr, "dev", t.name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "exists") {
		return fmt.Errorf("configure tun: assign ip: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Bring up
	if out, err := exec.Command("ip", "link", "set", t.name, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("configure tun: bring up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

func (t *linuxTUN) Read(buf []byte) (int, error)  { return 0, nil }
func (t *linuxTUN) Write(buf []byte) (int, error) { return 0, nil }

func (t *linuxTUN) Close() error {
	_ = exec.Command("ip", "link", "delete", t.name).Run()
	return nil
}
