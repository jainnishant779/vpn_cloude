package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

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

func (h *InstallScriptHandler) ServeScript(w http.ResponseWriter, r *http.Request) {
	script := buildInstallScript(h.deriveServerURL(r), "")
	writeScript(w, script)
}

func (h *InstallScriptHandler) ServeJoin(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	script := buildInstallScript(h.deriveServerURL(r), networkID)
	writeScript(w, script)
}

func (h *InstallScriptHandler) ServeJoinPS1(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	script := buildPS1Script(h.deriveServerURL(r), networkID)
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
  echo "Usage: curl %s/install.sh | sudo bash -s -- <network_id>"
  exit 1
fi`, serverURL)
	}

	return fmt.Sprintf(`#!/usr/bin/env bash
# QuickTunnel — zero-touch installer
# Usage: curl %s/join/<network_id> | sudo bash
set -euo pipefail

SERVER_URL="%s"
%s

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7*|armv6*) ARCH="armv7" ;;
  *) echo "Unsupported arch: $ARCH_RAW"; exit 1 ;;
esac

echo "[1/4] Platform: $OS/$ARCH"

echo "[2/4] Stopping old instance..."
pkill -f quicktunnel 2>/dev/null || true
ip link delete qtun0 2>/dev/null || true
fuser -k 51820/udp 2>/dev/null || true
rm -f /usr/local/bin/quicktunnel
sleep 1

if [ "$OS" = "linux" ]; then
  modprobe wireguard 2>/dev/null || true
  if ! command -v wg &>/dev/null; then
    apt-get install -y -qq wireguard-tools 2>/dev/null || \
    yum install -y wireguard-tools 2>/dev/null || true
  fi
fi

BINARY="/usr/local/bin/quicktunnel"
DOWNLOAD_URL="$SERVER_URL/api/v1/downloads/client/$OS/$ARCH"
echo "[3/6] Downloading from $DOWNLOAD_URL ..."
for i in 1 2 3; do
  if command -v curl &>/dev/null; then
    curl -fsSL --connect-timeout 15 --max-time 120 "$DOWNLOAD_URL" -o "$BINARY" && break
  elif command -v wget &>/dev/null; then
    wget -qO "$BINARY" --timeout=120 "$DOWNLOAD_URL" && break
  fi
  echo "  Retry $i/3..."
  sleep 2
done
chmod +x "$BINARY"

echo "[4/6] Joining network: $NETWORK_ID"
"$BINARY" join "$SERVER_URL" "$NETWORK_ID" &
JOIN_PID=$!

# Wait for config to be written (max 60s)
for i in $(seq 1 60); do
  if [ -f /etc/quicktunnel/config.json ] || [ -f "$HOME/.quicktunnel/config.json" ]; then
    break
  fi
  sleep 1
done

echo "[5/6] Installing as system service..."
"$BINARY" install 2>/dev/null || true

echo "[6/6] Done!"
echo ""
echo "  QuickTunnel is running and will auto-start on boot."
echo "  Commands:"
echo "    quicktunnel status   — check connection"
echo "    quicktunnel peers    — list connected peers"
echo "    sudo systemctl status quicktunnel — service status"
echo ""

# Keep running in foreground
wait $JOIN_PID 2>/dev/null || true
`, serverURL, serverURL, networkBlock)
}

func buildPS1Script(serverURL, networkID string) string {
    return fmt.Sprintf(`# QuickTunnel — Windows installer
# Run in PowerShell as Administrator:
# irm %s/join/%s/ps1 | iex

$ErrorActionPreference = "Stop"
$ServerURL   = "%s"
$NetworkID   = "%s"
$QtDir       = "$env:ProgramFiles\QuickTunnel"
$BinaryPath  = "$QtDir\quicktunnel.exe"
$WintunPath  = "$QtDir\wintun.dll"
$WGPath      = "$env:ProgramFiles\WireGuard"
$DownloadURL = "$ServerURL/api/v1/downloads/client/windows/amd64"

Write-Host "[1/5] Setting up directories..."
New-Item -ItemType Directory -Force -Path $QtDir | Out-Null

Write-Host "[2/5] Installing WireGuard (required)..."
if (-not (Test-Path "$WGPath\wireguard.exe")) {
    $wgInstaller = "$env:TEMP\wireguard-installer.exe"
    if (-not (Test-Path $wgInstaller)) {
        Write-Host "      Downloading WireGuard installer (may take 30-60s)..."
        Invoke-WebRequest -Uri "https://download.wireguard.com/windows-client/wireguard-installer.exe" -OutFile $wgInstaller -UseBasicParsing -TimeoutSec 300
    } else {
        Write-Host "      Using cached WireGuard installer."
    }
    Write-Host "      Installing WireGuard..."
    Start-Process -FilePath $wgInstaller -ArgumentList "/S" -Wait
    Write-Host "      ✓ WireGuard installed"
} else {
    Write-Host "      ✓ WireGuard already installed"
}

if ($env:PATH -notlike "*WireGuard*") {
    $env:PATH += ";$WGPath"
    [Environment]::SetEnvironmentVariable("PATH", "$env:PATH", "Machine")
}

Write-Host "[3/5] Installing WinTun driver..."
if (-not (Test-Path $WintunPath)) {
    $wintunZip = "$env:TEMP\wintun.zip"
    if (-not (Test-Path $wintunZip)) {
        Write-Host "      Downloading WinTun driver..."
        Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile $wintunZip -UseBasicParsing -TimeoutSec 120
    } else {
        Write-Host "      Using cached WinTun driver."
    }
    Expand-Archive -Path $wintunZip -DestinationPath "$env:TEMP\wintun" -Force
    Copy-Item "$env:TEMP\wintun\wintun\bin\amd64\wintun.dll" -Destination $WintunPath -Force
    Write-Host "      ✓ WinTun installed"
} else {
    Write-Host "      ✓ WinTun already present"
}

Write-Host "[4/5] Downloading QuickTunnel client..."
$localBinary = "$env:TEMP\quicktunnel.exe"
Write-Host "      Downloading client binary (may take 30-60s)..."
if (Test-Path "$env:TEMP\quicktunnel.exe") { Remove-Item "$env:TEMP\quicktunnel.exe" -Force }
Stop-Process -Name "quicktunnel" -Force -ErrorAction SilentlyContinue

$retries = 3
$downloaded = $false
for ($i = 1; $i -le $retries; $i++) {
    try {
        Invoke-WebRequest -Uri $DownloadURL -OutFile $localBinary -UseBasicParsing -TimeoutSec 300
        $downloaded = $true
        Write-Host "      ✓ Downloaded successfully"
        break
    } catch {
        Write-Host "      Download attempt $i/$retries failed"
        if ($i -lt $retries) {
            Write-Host "      Retrying in 5 seconds..."
            Start-Sleep -Seconds 5
        }
    }
}

if ($downloaded) {
    Copy-Item $localBinary -Destination $BinaryPath -Force
} else {
    Write-Host "ERROR: Failed to download QuickTunnel after $retries attempts" -ForegroundColor Red
    exit 1
}

if ($env:PATH -notlike "*QuickTunnel*") {
    try {
        $currentPath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
        if ($currentPath -notlike "*$QtDir*") {
            [Environment]::SetEnvironmentVariable("PATH", "$currentPath;$QtDir", "Machine")
        }
    } catch { }
    $env:PATH += ";$QtDir"
}

Write-Host "[5/5] Joining network $NetworkID and installing service..."
& $BinaryPath join $ServerURL $NetworkID

Write-Host ""
Write-Host "Installing as Windows Service for auto-start..."
& $BinaryPath install 2>&1 | Where-Object { -not ($_ -like "*error*") } | Out-Null

Write-Host ""
Write-Host "═══════════════════════════════════════════════"
Write-Host "✓ QuickTunnel is ready!"
Write-Host "═══════════════════════════════════════════════"
Write-Host ""
Write-Host "Commands:"
Write-Host "  quicktunnel status       - check connection"
Write-Host "  quicktunnel peers        - list connected peers"
Write-Host "  schtasks /query /tn QuickTunnel - task status"
Write-Host ""
Write-Host "Service will auto-start on reboot."
Write-Host ""
`, serverURL, networkID, serverURL, networkID)
}
