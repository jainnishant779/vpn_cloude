package nat

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/stun"
)

const stunTimeout = 5 * time.Second

// stunServers is a list of STUN servers to try in order.
var stunServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun2.l.google.com:19302",
	"stun.cloudflare.com:3478",
}

// DiscoverPublicEndpoint returns the mapped public IPv4 address observed by STUN.
// It forces IPv4 (udp4) to prevent getting IPv6 endpoints that peers can't route to.
func DiscoverPublicEndpoint(stunServer string) (ip string, port int, err error) {
	if stunServer == "" {
		stunServer = "stun.l.google.com:19302"
	}

	// Try the requested server first, then fallbacks
	servers := []string{stunServer}
	for _, s := range stunServers {
		if s != stunServer {
			servers = append(servers, s)
		}
	}

	var lastErr error
	for _, server := range servers {
		ip, port, err := discoverIPv4Endpoint(server)
		if err == nil {
			return ip, port, nil
		}
		lastErr = err
	}

	return "", 0, fmt.Errorf("discover public endpoint: all STUN servers failed: %w", lastErr)
}

// discoverIPv4Endpoint performs STUN discovery forcing IPv4 only.
func discoverIPv4Endpoint(stunServer string) (string, int, error) {
	// Resolve STUN server to IPv4 only
	host, port, err := net.SplitHostPort(stunServer)
	if err != nil {
		return "", 0, fmt.Errorf("discover endpoint: parse server: %w", err)
	}

	// Resolve to IPv4 addresses only
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", 0, fmt.Errorf("discover endpoint: resolve %s: %w", host, err)
	}

	var ipv4Addr string
	for _, ip := range ips {
		if ip.To4() != nil {
			ipv4Addr = net.JoinHostPort(ip.String(), port)
			break
		}
	}
	if ipv4Addr == "" {
		return "", 0, fmt.Errorf("discover endpoint: no IPv4 address for %s", host)
	}

	// Force udp4 to ensure IPv4 STUN response
	conn, err := net.DialTimeout("udp4", ipv4Addr, stunTimeout)
	if err != nil {
		return "", 0, fmt.Errorf("discover endpoint: dial stun: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(stunTimeout)); err != nil {
		return "", 0, fmt.Errorf("discover endpoint: set deadline: %w", err)
	}

	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if _, err := conn.Write(message.Raw); err != nil {
		return "", 0, fmt.Errorf("discover endpoint: send binding request: %w", err)
	}

	buffer := make([]byte, 1500)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", 0, fmt.Errorf("discover endpoint: read response: %w", err)
	}

	response := &stun.Message{Raw: buffer[:n]}
	if err := response.Decode(); err != nil {
		return "", 0, fmt.Errorf("discover endpoint: decode response: %w", err)
	}

	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(response); err == nil {
		if xorAddr.IP.To4() != nil {
			return xorAddr.IP.String(), xorAddr.Port, nil
		}
	}

	var mappedAddr stun.MappedAddress
	if err := mappedAddr.GetFrom(response); err == nil {
		if mappedAddr.IP.To4() != nil {
			return mappedAddr.IP.String(), mappedAddr.Port, nil
		}
	}

	return "", 0, fmt.Errorf("discover endpoint: no IPv4 mapped address in response")
}
