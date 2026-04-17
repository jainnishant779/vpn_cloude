package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

type clientBinarySpec struct {
	DownloadOS   string
	DownloadArch string
	GOOS         string
	GOARCH       string
	GOARM        string
	Filename     string
}

var supportedClientBinaries = map[string]clientBinarySpec{
	"linux/amd64": {
		DownloadOS:   "linux",
		DownloadArch: "amd64",
		GOOS:         "linux",
		GOARCH:       "amd64",
		Filename:     "quicktunnel-linux-amd64",
	},
	"linux/arm64": {
		DownloadOS:   "linux",
		DownloadArch: "arm64",
		GOOS:         "linux",
		GOARCH:       "arm64",
		Filename:     "quicktunnel-linux-arm64",
	},
	"linux/armv7": {
		DownloadOS:   "linux",
		DownloadArch: "armv7",
		GOOS:         "linux",
		GOARCH:       "arm",
		GOARM:        "7",
		Filename:     "quicktunnel-linux-armv7",
	},
	"darwin/amd64": {
		DownloadOS:   "darwin",
		DownloadArch: "amd64",
		GOOS:         "darwin",
		GOARCH:       "amd64",
		Filename:     "quicktunnel-darwin-amd64",
	},
	"darwin/arm64": {
		DownloadOS:   "darwin",
		DownloadArch: "arm64",
		GOOS:         "darwin",
		GOARCH:       "arm64",
		Filename:     "quicktunnel-darwin-arm64",
	},
	"windows/amd64": {
		DownloadOS:   "windows",
		DownloadArch: "amd64",
		GOOS:         "windows",
		GOARCH:       "amd64",
		Filename:     "quicktunnel-windows-amd64.exe",
	},
}

// ClientDownloadHandler serves cached or on-demand-built client binaries.
type ClientDownloadHandler struct {
	projectRoot string
	buildBinary func(spec clientBinarySpec, targetPath string) error
	buildMu     sync.Mutex
}

func NewClientDownloadHandler() *ClientDownloadHandler {
	h := &ClientDownloadHandler{
		projectRoot: discoverProjectRoot(),
	}
	h.buildBinary = h.defaultBuildBinary
	return h
}

func (h *ClientDownloadHandler) Get(w http.ResponseWriter, r *http.Request) {
	spec, ok := lookupClientBinarySpec(chi.URLParam(r, "os"), chi.URLParam(r, "arch"))
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported client platform")
		return
	}

	targetPath, err := h.ensureBinary(spec)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "client artifact is unavailable")
		return
	}

	file, err := os.Open(targetPath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to open client artifact")
		return
	}
	defer file.Close()

	// Compute SHA256 checksum for integrity header
	checksum, err := computeFileSHA256(targetPath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to compute checksum")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", spec.Filename))
	w.Header().Set("X-Checksum-SHA256", checksum)
	http.ServeContent(w, r, spec.Filename, info.ModTime(), file)
}

// computeFileSHA256 returns the hex-encoded SHA256 checksum of the file at the given path.
func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (h *ClientDownloadHandler) ensureBinary(spec clientBinarySpec) (string, error) {
	if h.projectRoot == "" {
		return "", fmt.Errorf("client downloads are unavailable on this server host")
	}

	targetPath := filepath.Join(h.projectRoot, "client", "bin", spec.Filename)
	if fileExists(targetPath) {
		return targetPath, nil
	}

	h.buildMu.Lock()
	defer h.buildMu.Unlock()

	if fileExists(targetPath) {
		return targetPath, nil
	}

	if h.buildBinary == nil {
		return "", fmt.Errorf("client binary %s is unavailable", spec.Filename)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", fmt.Errorf("prepare client artifact directory: %w", err)
	}

	if err := h.buildBinary(spec, targetPath); err != nil {
		_ = os.Remove(targetPath)
		return "", err
	}

	if spec.GOOS != "windows" {
		_ = os.Chmod(targetPath, 0o755)
	}

	if !fileExists(targetPath) {
		return "", fmt.Errorf("client binary %s was not produced", spec.Filename)
	}

	return targetPath, nil
}

func (h *ClientDownloadHandler) defaultBuildBinary(spec clientBinarySpec, targetPath string) error {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("client binary %s is missing and Go is not installed on the host", spec.Filename)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, goBin, "build", "-trimpath", "-o", targetPath, "./cmd/quicktunnel")
	cmd.Dir = filepath.Join(h.projectRoot, "client")
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+spec.GOOS,
		"GOARCH="+spec.GOARCH,
	)
	if spec.GOARM != "" {
		cmd.Env = append(cmd.Env, "GOARM="+spec.GOARM)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return fmt.Errorf("build client binary %s: %w", spec.Filename, err)
		}
		return fmt.Errorf("build client binary %s: %w: %s", spec.Filename, err, trimmed)
	}

	return nil
}

func lookupClientBinarySpec(osName, arch string) (clientBinarySpec, bool) {
	key := strings.ToLower(strings.TrimSpace(osName)) + "/" + strings.ToLower(strings.TrimSpace(arch))
	spec, ok := supportedClientBinaries[key]
	return spec, ok
}

func discoverProjectRoot() string {
	candidates := []string{}

	if gowork := strings.TrimSpace(os.Getenv("GOWORK")); gowork != "" {
		if strings.HasSuffix(strings.ToLower(gowork), "go.work") {
			candidates = append(candidates, filepath.Dir(gowork))
		} else {
			candidates = append(candidates, gowork)
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd, filepath.Dir(cwd))
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates, exeDir, filepath.Dir(exeDir))
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		cleaned := filepath.Clean(candidate)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		if fileExists(filepath.Join(cleaned, "client", "go.mod")) &&
			fileExists(filepath.Join(cleaned, "server", "go.mod")) {
			return cleaned
		}
	}

	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
