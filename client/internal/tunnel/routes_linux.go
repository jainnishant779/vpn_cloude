//go:build linux
// +build linux

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func addSubnetRoute(cidr, ifName string) error {
	out, err := exec.Command("ip", "route", "add", cidr, "dev", ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		return fmt.Errorf("add subnet route: %s: %w", string(out), err)
	}
	return nil
}

func addHostRoute(ip, ifName string) error {
	dst := ip + "/32"
	out, err := exec.Command("ip", "route", "add", dst, "dev", ifName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		return fmt.Errorf("add host route: %s: %w", string(out), err)
	}
	return nil
}

func removeHostRoute(ip, ifName string) error {
	_ = exec.Command("ip", "route", "del", ip+"/32", "dev", ifName).Run()
	return nil
}

func maskBitsFromCIDR(cidr string) (int, error) {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		bits, err := strconv.Atoi(parts[1])
		if err == nil {
			return bits, nil
		}
	}
	bits, err := strconv.Atoi(cidr)
	if err != nil {
		return 24, nil
	}
	return bits, nil
}
