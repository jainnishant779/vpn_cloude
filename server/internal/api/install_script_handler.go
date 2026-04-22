package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// InstallScriptHandler serves:

// ServeJoinPS1 handles GET /join/{network_id}/ps1
// This serves a PowerShell script for Windows one-liner install.
func (h *InstallScriptHandler) ServeJoinPS1(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	serverURL := h.deriveServerURL(r)
	script := buildInstallPS1(serverURL, networkID)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=install.ps1")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

// buildInstallPS1 generates a PowerShell script for Windows quicktunnel install and join
func buildInstallPS1(serverURL, networkID string) string {
	binaryURL := serverURL + "/api/v1/downloads/client/windows/amd64"
	wintunURL := "https://www.wintun.net/builds/wintun-0.14.1.zip"
	ps := `$ErrorActionPreference = 'Stop'
function Download($url, $dest) {
	Invoke-WebRequest -Uri $url -OutFile $dest -UseBasicParsing
}

$bin = "C:\\quicktunnel.exe"
$wintunZip = "C:\\wintun.zip"
$wintunDir = "C:\\wintun"
$wintunDLL = "C:\\wintun.dll"

Write-Host "[1/3] Downloading quicktunnel.exe..."
Download "` + binaryURL + `" $bin

Write-Host "[2/3] Downloading WinTun..."
Download "` + wintunURL + `" $wintunZip
Expand-Archive -Path $wintunZip -DestinationPath $wintunDir -Force
Copy-Item "$wintunDir\wintun\bin\amd64\wintun.dll" $wintunDLL -Force

Write-Host "[3/3] Joining network..."
& $bin join ` + strings.TrimPrefix(serverURL, "http://") + ` ` + networkID + `
`
	return ps
}
//   GET /install.sh            — generic installer
//   GET /join/{network_id}     — ZeroTier-style one-liner
//
// Usage:
//   curl http://54.89.232.16:3000/join/5agrlxob7exh | sudo bash
type InstallScriptHandler struct {
	serverURL string
}

func NewInstallScriptHandler(serverURL string) *InstallScriptHandler {
	return &InstallScriptHandler{
		serverURL: strings.TrimRight(strings.TrimSpace(serverURL), "/"),
	}
}

func (h *InstallScriptHandler) deriveServerURL(r *http.Request) string {
	if h.serverURL != "" {
		return h.serverURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

// ServeScript handles GET /install.sh
func (h *InstallScriptHandler) ServeScript(w http.ResponseWriter, r *http.Request) {
	script := buildInstallScript(h.deriveServerURL(r), "")
	writeScript(w, script)
}

// ServeJoin handles GET /join/{network_id}
// This is the ZeroTier-style one-liner.
// curl http://<server>/join/<network_id> | sudo bash
func (h *InstallScriptHandler) ServeJoin(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	script := buildInstallScript(h.deriveServerURL(r), networkID)
	writeScript(w, script)
}

func writeScript(w http.ResponseWriter, script string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=install.sh")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}

func buildInstallScript(serverURL, networkID string) string {
	networkBlock := ""
	if networkID != "" {
		networkBlock = fmt.Sprintf(`NETWORK_ID="%s"`, networkID)
	} else {
		networkBlock = fmt.Sprintf(`NETWORK_ID="${1:-}"
if [ -z "$NETWORK_ID" ]; then
  echo "Usage:  curl %s/install.sh | sudo bash -s -- <network_id>"
  echo "Or:     curl %s/join/<network_id> | sudo bash"
  exit 1
fi`, serverURL, serverURL)
	}

	return fmt.Sprintf(`#!/usr/bin/env bash
# QuickTunnel — zero-touch installer
# One-liner: curl %s/join/<network_id> | sudo bash
set -euo pipefail

SERVER_URL="%s"
%s

# ── Detect platform ────────────────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7*|armv6*) ARCH="armv7" ;;
  *) echo "Unsupported arch: $ARCH_RAW"; exit 1 ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac
echo "[1/3] Platform: $OS/$ARCH"

# ── Download binary ────────────────────────────────────────────────────────────
BINARY="/usr/local/bin/quicktunnel"
DOWNLOAD_URL="$SERVER_URL/api/v1/downloads/client/$OS/$ARCH"
echo "[2/3] Downloading from $DOWNLOAD_URL ..."
if command -v curl &>/dev/null; then
  curl -fsSL "$DOWNLOAD_URL" -o "$BINARY"
elif command -v wget &>/dev/null; then
  wget -qO "$BINARY" "$DOWNLOAD_URL"
else
  echo "Error: curl or wget required"; exit 1
fi
chmod +x "$BINARY"
echo "      Saved to $BINARY"

# ── Join network ───────────────────────────────────────────────────────────────
echo "[3/3] Joining network: $NETWORK_ID"
exec "$BINARY" join "$SERVER_URL" "$NETWORK_ID"
`, serverURL, serverURL, networkBlock)
}