package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// InstallScriptHandler serves:
//   GET /install.sh              — bash installer (Linux/Mac)
//   GET /join/{network_id}       — ZeroTier-style Linux one-liner
//   GET /join/{network_id}/ps1   — Windows PowerShell one-liner
//
// Linux:   curl http://<server>/join/<network_id> | sudo bash
// Windows: irm http://<server>/join/<network_id>/ps1 | iex
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

// ServeJoin handles GET /join/{network_id} — Linux/Mac one-liner
func (h *InstallScriptHandler) ServeJoin(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	script := buildInstallScript(h.deriveServerURL(r), networkID)
	writeScript(w, script)
}

// ServeJoinPS1 handles GET /join/{network_id}/ps1 — Windows PowerShell one-liner
func (h *InstallScriptHandler) ServeJoinPS1(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	serverURL := h.deriveServerURL(r)
	script := buildPS1Script(serverURL, networkID)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=install.ps1")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
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
# QuickTunnel — zero-touch installer (Linux/Mac)
# Usage: curl %s/join/<network_id> | sudo bash
set -euo pipefail

SERVER_URL="%s"
%s

# ── Detect platform ─────────────────────────────────────────────────────
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
echo "[1/4] Platform: $OS/$ARCH"

# ── Stop old instance + clean up ─────────────────────────────────────────
echo "[2/4] Cleaning up old instance..."
pkill -f quicktunnel 2>/dev/null || true
ip link delete qtun0 2>/dev/null || true
fuser -k 51820/udp 2>/dev/null || true
rm -f /usr/local/bin/quicktunnel
sleep 1

# ── WireGuard kernel module (Linux only) ─────────────────────────────────
if [ "$OS" = "linux" ]; then
  if ! lsmod | grep -q wireguard; then
    modprobe wireguard 2>/dev/null || true
  fi
fi

# ── Download binary ───────────────────────────────────────────────────────
BINARY="/usr/local/bin/quicktunnel"
DOWNLOAD_URL="$SERVER_URL/api/v1/downloads/client/$OS/$ARCH"
echo "[3/4] Downloading from $DOWNLOAD_URL ..."
if command -v curl &>/dev/null; then
  curl -fsSL "$DOWNLOAD_URL" -o "$BINARY"
elif command -v wget &>/dev/null; then
  wget -qO "$BINARY" "$DOWNLOAD_URL"
else
  echo "Error: curl or wget required"; exit 1
fi
chmod +x "$BINARY"
echo "      Saved to $BINARY"

# ── Join network ──────────────────────────────────────────────────────────
echo "[4/4] Joining network: $NETWORK_ID"
exec "$BINARY" join "$SERVER_URL" "$NETWORK_ID"
`, serverURL, serverURL, networkBlock)
}

func buildPS1Script(serverURL, networkID string) string {
	return fmt.Sprintf(`# QuickTunnel — Windows one-liner installer
# Run in PowerShell as Administrator:
# irm %s/join/%s/ps1 | iex

$ErrorActionPreference = "Stop"
$ServerURL  = "%s"
$NetworkID  = "%s"
$BinaryPath = "$env:ProgramFiles\QuickTunnel\quicktunnel.exe"
$WintunPath = "$env:ProgramFiles\QuickTunnel\wintun.dll"
$DownloadURL = "$ServerURL/api/v1/downloads/client/windows/amd64"

Write-Host "[1/4] Setting up QuickTunnel directory..."
New-Item -ItemType Directory -Force -Path "$env:ProgramFiles\QuickTunnel" | Out-Null

Write-Host "[2/4] Downloading WinTun driver..."
if (-not (Test-Path $WintunPath)) {
    $wintunZip = "$env:TEMP\wintun.zip"
    Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile $wintunZip -UseBasicParsing
    Expand-Archive -Path $wintunZip -DestinationPath "$env:TEMP\wintun" -Force
    Copy-Item "$env:TEMP\wintun\wintun\bin\amd64\wintun.dll" -Destination $WintunPath -Force
    Write-Host "      WinTun installed."
} else {
    Write-Host "      WinTun already present."
}

Write-Host "[3/4] Downloading QuickTunnel binary..."
# Stop old instance first
Stop-Process -Name "quicktunnel" -Force -ErrorAction SilentlyContinue
Invoke-WebRequest -Uri $DownloadURL -OutFile $BinaryPath -UseBasicParsing
Write-Host "      Saved to $BinaryPath"

# Add to PATH if not already there
$currentPath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
if ($currentPath -notlike "*QuickTunnel*") {
    [Environment]::SetEnvironmentVariable("PATH", "$currentPath;$env:ProgramFiles\QuickTunnel", "Machine")
    $env:PATH += ";$env:ProgramFiles\QuickTunnel"
}

Write-Host "[4/4] Joining network $NetworkID ..."
& $BinaryPath join $ServerURL $NetworkID
`, serverURL, networkID, serverURL, networkID)
}