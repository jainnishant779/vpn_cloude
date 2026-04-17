package nat

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/stun"
)

const stunTimeout = 5 * time.Second

// DiscoverPublicEndpoint returns the mapped public address observed by STUN.
func DiscoverPublicEndpoint(stunServer string) (ip string, port int, err error) {
	if stunServer == "" {
		return "", 0, fmt.Errorf("discover public endpoint: stun server is required")
	}

	conn, err := net.DialTimeout("udp", stunServer, stunTimeout)
	if err != nil {
		return "", 0, fmt.Errorf("discover public endpoint: dial stun: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(stunTimeout)); err != nil {
		return "", 0, fmt.Errorf("discover public endpoint: set deadline: %w", err)
	}

	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if _, err := conn.Write(message.Raw); err != nil {
		return "", 0, fmt.Errorf("discover public endpoint: send binding request: %w", err)
	}

	buffer := make([]byte, 1500)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", 0, fmt.Errorf("discover public endpoint: read response: %w", err)
	}

	response := &stun.Message{Raw: buffer[:n]}
	if err := response.Decode(); err != nil {
		return "", 0, fmt.Errorf("discover public endpoint: decode response: %w", err)
	}

	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(response); err == nil {
		return xorAddr.IP.String(), xorAddr.Port, nil
	}

	var mappedAddr stun.MappedAddress
	if err := mappedAddr.GetFrom(response); err == nil {
		return mappedAddr.IP.String(), mappedAddr.Port, nil
	}

	return "", 0, fmt.Errorf("discover public endpoint: no mapped address attribute in response")
}
