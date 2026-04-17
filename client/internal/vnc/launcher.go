package vnc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LaunchVNCViewer launches a local VNC viewer pointed at peer virtual address.
func LaunchVNCViewer(peerVirtualIP string, vncPort int) error {
	if strings.TrimSpace(peerVirtualIP) == "" {
		return fmt.Errorf("launch vnc viewer: peer virtual ip is required")
	}
	if vncPort <= 0 || vncPort > 65535 {
		return fmt.Errorf("launch vnc viewer: invalid vnc port")
	}

	viewer, viewerName, err := detectViewer()
	if err != nil {
		return fmt.Errorf("launch vnc viewer: %w", err)
	}

	target := targetAddress(peerVirtualIP, vncPort)
	settings := SuggestVNCSettings(MeasureLatency(peerVirtualIP), MeasureBandwidth(peerVirtualIP))
	args := viewerArgs(viewerName, target, settings)

	cmd := exec.Command(viewer, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch vnc viewer: start process: %w", err)
	}
	return nil
}

func detectViewer() (path string, viewerName string, err error) {
	if custom := strings.TrimSpace(os.Getenv("QUICKTUNNEL_VNC_VIEWER")); custom != "" {
		if fileExists(custom) {
			return custom, "custom", nil
		}
	}

	candidates := [][]string{}
	switch runtime.GOOS {
	case "windows":
		candidates = [][]string{
			{`C:\Program Files\TigerVNC\vncviewer.exe`, "tigervnc"},
			{`C:\Program Files\RealVNC\VNC Viewer\vncviewer.exe`, "realvnc"},
			{`C:\Program Files\TightVNC\tvnviewer.exe`, "tightvnc"},
		}
	case "darwin":
		candidates = [][]string{
			{"/Applications/TigerVNC Viewer.app/Contents/MacOS/TigerVNC Viewer", "tigervnc"},
			{"/Applications/VNC Viewer.app/Contents/MacOS/vncviewer", "realvnc"},
		}
	default:
		candidates = [][]string{
			{"/usr/bin/vncviewer", "tigervnc"},
			{"/usr/local/bin/vncviewer", "tigervnc"},
			{"/usr/bin/xtightvncviewer", "tightvnc"},
		}
	}

	for _, candidate := range candidates {
		if len(candidate) != 2 {
			continue
		}
		if fileExists(candidate[0]) {
			return candidate[0], candidate[1], nil
		}
	}

	// Fallback to PATH lookup for common binaries.
	for _, binary := range []string{"vncviewer", "tvnviewer", "xtightvncviewer"} {
		resolved, lookupErr := exec.LookPath(binary)
		if lookupErr == nil {
			return resolved, normalizeViewerName(binary), nil
		}
	}

	return "", "", fmt.Errorf("no supported VNC viewer detected (TigerVNC, RealVNC, TightVNC)")
}

func viewerArgs(viewerName, target string, settings VNCSettings) []string {
	switch viewerName {
	case "tigervnc":
		return []string{
			target,
			"-CompressLevel", fmt.Sprintf("%d", settings.Compression),
			"-QualityLevel", fmt.Sprintf("%d", settings.Quality),
		}
	case "tightvnc":
		host, port := splitHostAndPort(target)
		return []string{
			"-host=" + host,
			"-port=" + port,
		}
	case "realvnc":
		return []string{target}
	default:
		return []string{target}
	}
}

func splitHostAndPort(target string) (host, port string) {
	parts := strings.Split(target, ":")
	if len(parts) < 2 {
		return target, "5900"
	}
	return strings.Join(parts[:len(parts)-1], ":"), parts[len(parts)-1]
}

func normalizeViewerName(binary string) string {
	base := strings.ToLower(filepath.Base(binary))
	switch base {
	case "tvnviewer", "tvnviewer.exe", "xtightvncviewer":
		return "tightvnc"
	case "vncviewer", "vncviewer.exe", "tigervnc viewer":
		return "tigervnc"
	default:
		return "custom"
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
