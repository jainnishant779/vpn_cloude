package vnc

import (
	"fmt"
	"net"
	"time"
)

var defaultVNCPorts = []int{5900, 5901, 5902, 5800}

// DiscoverVNCServer probes localhost for active VNC servers.
func DiscoverVNCServer() (port int, available bool) {
	for _, candidate := range defaultVNCPorts {
		target := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", candidate))
		conn, err := net.DialTimeout("tcp", target, 500*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return candidate, true
	}
	return 0, false
}
