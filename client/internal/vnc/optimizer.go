package vnc

import (
	"fmt"
	"net"
	"time"
)

// VNCSettings defines viewer tuning knobs for network conditions.
type VNCSettings struct {
	Compression int    `json:"compression"`
	ColorDepth  int    `json:"color_depth"`
	Encoding    string `json:"encoding"`
	Quality     int    `json:"quality"`
}

// MeasureLatency estimates network RTT for a peer by repeated TCP connects.
func MeasureLatency(peerIP string) time.Duration {
	target := net.JoinHostPort(peerIP, "5900")
	samples := []time.Duration{}

	for i := 0; i < 3; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", target, 1200*time.Millisecond)
		if err != nil {
			samples = append(samples, 1200*time.Millisecond)
			continue
		}
		_ = conn.Close()
		samples = append(samples, time.Since(start))
	}

	var total time.Duration
	for _, sample := range samples {
		total += sample
	}
	if len(samples) == 0 {
		return 1200 * time.Millisecond
	}
	return total / time.Duration(len(samples))
}

// MeasureBandwidth approximates throughput from VNC server handshake read speed.
func MeasureBandwidth(peerIP string) int64 {
	target := net.JoinHostPort(peerIP, "5900")
	conn, err := net.DialTimeout("tcp", target, 2*time.Second)
	if err != nil {
		return 0
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
		return 0
	}

	start := time.Now()
	buffer := make([]byte, 256)
	n, err := conn.Read(buffer)
	if err != nil || n == 0 {
		return 0
	}
	elapsed := time.Since(start)
	if elapsed <= 0 {
		elapsed = 1 * time.Millisecond
	}

	bytesPerSecond := int64(float64(n) / elapsed.Seconds())
	if bytesPerSecond < 0 {
		return 0
	}
	return bytesPerSecond
}

// SuggestVNCSettings picks sane defaults from observed network quality.
func SuggestVNCSettings(latency time.Duration, bandwidth int64) VNCSettings {
	const mbps = 1024 * 1024

	switch {
	case latency > 200*time.Millisecond || bandwidth < int64(2*mbps):
		return VNCSettings{
			Compression: 9,
			ColorDepth:  16,
			Encoding:    "tight",
			Quality:     4,
		}
	case latency > 100*time.Millisecond || bandwidth < int64(8*mbps):
		return VNCSettings{
			Compression: 6,
			ColorDepth:  16,
			Encoding:    "zrle",
			Quality:     6,
		}
	default:
		return VNCSettings{
			Compression: 3,
			ColorDepth:  24,
			Encoding:    "hextile",
			Quality:     8,
		}
	}
}

func targetAddress(peerVirtualIP string, vncPort int) string {
	return net.JoinHostPort(peerVirtualIP, fmt.Sprintf("%d", vncPort))
}
