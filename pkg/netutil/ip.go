package netutil

import (
	"net"
	"sort"
)

// AllocateIP selects the next available host IP in a CIDR for virtual peer assignment.
func AllocateIP(cidr string, existing []string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}

	networkIP := ipnet.IP.To4()
	if networkIP == nil {
		return ""
	}

	mask := maskToUint32(ipnet.Mask)
	if mask == 0 {
		return ""
	}

	network := ipToUint32(networkIP.Mask(ipnet.Mask))
	broadcast := network | ^mask
	if broadcast <= network+2 {
		return ""
	}

	used := make(map[uint32]struct{}, len(existing))
	for _, raw := range existing {
		parsed := net.ParseIP(raw).To4()
		if parsed == nil {
			continue
		}
		used[ipToUint32(parsed)] = struct{}{}
	}

	for candidate := network + 2; candidate < broadcast; candidate++ {
		ip := uint32ToIP(candidate)

		// Skip x.x.x.255 to avoid common broadcast ambiguity in mixed subnet setups.
		if ip[3] == 255 {
			continue
		}
		if _, exists := used[candidate]; exists {
			continue
		}
		if !ipnet.Contains(ip) {
			continue
		}
		return ip.String()
	}

	return ""
}

// IsPrivateIP reports whether an address is from private or loopback ranges.
func IsPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return parsed.IsPrivate() || parsed.IsLoopback()
}

// GetLocalIPs returns active non-loopback IPv4 addresses in stable order.
func GetLocalIPs() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	seen := map[string]struct{}{}
	ips := make([]string, 0, len(interfaces))

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}

			ipv4 := ip.To4()
			if ipv4 == nil {
				continue
			}

			value := ipv4.String()
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			ips = append(ips, value)
		}
	}

	sort.Strings(ips)
	return ips
}

// GetOutboundIP returns the source IP chosen by the OS for outbound traffic.
func GetOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
			if ipv4 := addr.IP.To4(); ipv4 != nil {
				return ipv4.String()
			}
		}
	}

	local := GetLocalIPs()
	if len(local) == 0 {
		return ""
	}
	return local[0]
}

func ipToUint32(ip net.IP) uint32 {
	v := ip.To4()
	if v == nil {
		return 0
	}
	return uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
}

func uint32ToIP(v uint32) net.IP {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func maskToUint32(mask net.IPMask) uint32 {
	if len(mask) != net.IPv4len {
		return 0
	}
	return uint32(mask[0])<<24 | uint32(mask[1])<<16 | uint32(mask[2])<<8 | uint32(mask[3])
}
