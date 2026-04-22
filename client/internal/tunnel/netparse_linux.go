package tunnel

import "net"

func parseIPNet(cidr string) (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(cidr)
}
