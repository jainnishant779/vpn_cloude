package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

type InstallScriptHandler struct {
	serverURL string
}

// validID matches only safe alphanumeric / hyphen / underscore identifiers.
var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validServerURL matches http(s) URLs without shell-dangerous characters.
var validServerURL = regexp.MustCompile(`^https?://[a-zA-Z0-9._:/-]+$`)

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
	derived := fmt.Sprintf("%s://%s", scheme, r.Host)
	if !validServerURL.MatchString(derived) {
		// Fallback: refuse to produce a script with an untrusted host.
		return ""
	}
	return derived
}

func (h *InstallScriptHandler) ServeScript(w http.ResponseWriter, r *http.Request) {
	serverURL := h.deriveServerURL(r)
	if serverURL == "" {
		http.Error(w, "unable to determine server URL", http.StatusBadRequest)
		return
	}
	script := buildInstallScript(serverURL, "")
	writeScript(w, script)
}

func (h *InstallScriptHandler) ServeJoin(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	if !validID.MatchString(networkID) {
		http.Error(w, "network_id contains invalid characters", http.StatusBadRequest)
		return
	}
	serverURL := h.deriveServerURL(r)
	if serverURL == "" {
		http.Error(w, "unable to determine server URL", http.StatusBadRequest)
		return
	}
	script := buildInstallScript(serverURL, networkID)
	writeScript(w, script)
}

func (h *InstallScriptHandler) ServeJoinPS1(w http.ResponseWriter, r *http.Request) {
	networkID := strings.TrimSpace(chi.URLParam(r, "network_id"))
	if networkID == "" {
		http.Error(w, "network_id is required", http.StatusBadRequest)
		return
	}
	if !validID.MatchString(networkID) {
		http.Error(w, "network_id contains invalid characters", http.StatusBadRequest)
		return
	}
	serverURL := h.deriveServerURL(r)
	if serverURL == "" {
		http.Error(w, "unable to determine server URL", http.StatusBadRequest)
		return
	}
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
  echo "Usage: curl %s/install.sh | sudo bash -s -- <network_id>"
  exit 1
fi`, serverURL)
	}

	return fmt.Sprintf(`#!/usr/bin/env bash
# QuickTunnel — zero-touch installer + service setup
# Usage: curl %s/join/<network_id> | sudo bash
set -euo pipefail

SERVER_URL="%s"
BINARY="/usr/local/bin/quicktunnel"
SERVICE_NAME="quicktunnel"
%s

# ── 1. Platform detection ─────────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7*|armv6*) ARCH="armv7" ;;
  *) echo "Unsupported arch: $ARCH_RAW"; exit 1 ;;
esac
echo "[1/5] Platform: $OS/$ARCH"

# ── 2. Cleanup old instance ───────────────────────────────────────────────────
echo "[2/5] Cleaning up..."
systemctl stop "$SERVICE_NAME" 2>/dev/null || true
pkill -f quicktunnel 2>/dev/null || true
sleep 1
ip link delete qtun0 2>/dev/null || true
fuser -k 51820/udp 2>/dev/null || true
rm -f "$BINARY"

# ── 3. WireGuard kernel module ────────────────────────────────────────────────
if [ "$OS" = "linux" ]; then
  modprobe wireguard 2>/dev/null || true
  if ! command -v wg &>/dev/null; then
    apt-get install -y -qq wireguard-tools 2>/dev/null || \
    yum install -y wireguard-tools 2>/dev/null || true
  fi
fi

# ── 4. Download binary ────────────────────────────────────────────────────────
DOWNLOAD_URL="$SERVER_URL/api/v1/downloads/client/$OS/$ARCH"
echo "[3/5] Downloading from $DOWNLOAD_URL ..."
DOWNLOAD_SUCCESS=false
for i in 1 2 3; do
  if command -v curl &>/dev/null; then
    if curl -fsSL --connect-timeout 15 --max-time 120 "$DOWNLOAD_URL" -o "$BINARY"; then
      DOWNLOAD_SUCCESS=true
      break
    fi
  elif command -v wget &>/dev/null; then
    if wget -qO "$BINARY" --timeout=120 "$DOWNLOAD_URL"; then
      DOWNLOAD_SUCCESS=true
      break
    fi
  fi
  echo "  Retry $i/3 ..."
  sleep 3
done
if [ "$DOWNLOAD_SUCCESS" != "true" ]; then
  echo "ERROR: Failed to download QuickTunnel binary after 3 attempts."
  exit 1
fi
chmod +x "$BINARY"
echo "      Saved: $BINARY"

# ── 5. Join + install systemd service ────────────────────────────────────────
echo "[4/5] Joining network $NETWORK_ID ..."

# Run join once to get config (foreground, short timeout)
if ! timeout 30s "$BINARY" join "$SERVER_URL" "$NETWORK_ID"; then
  echo "[WARN] Join command exited with an error or timed out."
fi

CONFIG_FILE="/root/.quicktunnel/config.json"
if [ -n "${HOME:-}" ] && [ -f "$HOME/.quicktunnel/config.json" ]; then
  CONFIG_FILE="$HOME/.quicktunnel/config.json"
fi

if [ ! -f "$CONFIG_FILE" ]; then
  echo "ERROR: Config file not found at $CONFIG_FILE. Join may have failed."
  exit 1
fi

echo "[5/5] Installing systemd service..."

# Create systemd service file
cat > /etc/systemd/system/${SERVICE_NAME}.service << EOF
[Unit]
Description=QuickTunnel VPN
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStartPre=/sbin/modprobe wireguard
ExecStart=${BINARY} up
Restart=always
RestartSec=10
User=root
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl start "$SERVICE_NAME"

echo ""
echo "═══════════════════════════════════════════════"
echo "✓ QuickTunnel installed and running!"
echo "  Virtual network: $NETWORK_ID"
echo "  Service: systemctl status $SERVICE_NAME"
echo "  Logs: journalctl -u $SERVICE_NAME -f"
echo "  Peers: quicktunnel peers"
echo "═══════════════════════════════════════════════"
`, serverURL, serverURL, networkBlock)
}

func buildPS1Script(serverURL, networkID string) string {
	return fmt.Sprintf(`# QuickTunnel — Windows installer + auto-start service
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
$TaskName    = "QuickTunnel"

Write-Host "[1/6] Setting up QuickTunnel directory..."
New-Item -ItemType Directory -Force -Path $QtDir | Out-Null

# ── Stop existing ─────────────────────────────────────────────────────────────
Write-Host "      Stopping existing instance..."
Stop-Process -Name "quicktunnel" -Force -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

# ── Install WireGuard ─────────────────────────────────────────────────────────
Write-Host "[2/6] Installing WireGuard..."
if (-not (Test-Path "$WGPath\wireguard.exe")) {
    $wgInstaller = "$env:TEMP\wireguard-installer.exe"
    Write-Host "      Downloading WireGuard installer..."
    try {
        Invoke-WebRequest -Uri "https://download.wireguard.com/windows-client/wireguard-installer.exe" -OutFile $wgInstaller -UseBasicParsing -TimeoutSec 300
        Write-Host "      Installing WireGuard (silent)..."
        Start-Process -FilePath $wgInstaller -ArgumentList "/S" -Wait
        Start-Sleep -Seconds 3
        Write-Host "      WireGuard installed."
    } catch {
        Write-Host "      [WARN] WireGuard download failed: $_" -ForegroundColor Yellow
    }
} else {
    Write-Host "      WireGuard already installed."
}
if ($env:PATH -notlike "*WireGuard*") {
    $env:PATH += ";$WGPath"
    try { [Environment]::SetEnvironmentVariable("PATH", [Environment]::GetEnvironmentVariable("PATH","Machine")+";$WGPath", "Machine") } catch {}
}

# ── Install WinTun ────────────────────────────────────────────────────────────
Write-Host "[3/6] Installing WinTun driver..."
if (-not (Test-Path $WintunPath)) {
    $wintunZip = "$env:TEMP\wintun.zip"
    try {
        Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile $wintunZip -UseBasicParsing -TimeoutSec 120
        Expand-Archive -Path $wintunZip -DestinationPath "$env:TEMP\wintun_extract" -Force
        Copy-Item "$env:TEMP\wintun_extract\wintun\bin\amd64\wintun.dll" -Destination $WintunPath -Force
        Write-Host "      WinTun installed."
    } catch {
        Write-Host "      [WARN] WinTun download failed: $_" -ForegroundColor Yellow
    }
} else {
    Write-Host "      WinTun already present."
}

# ── Download QuickTunnel binary ───────────────────────────────────────────────
Write-Host "[4/6] Downloading QuickTunnel binary..."
$downloaded = $false
for ($i = 1; $i -le 3; $i++) {
    try {
        Invoke-WebRequest -Uri $DownloadURL -OutFile $BinaryPath -UseBasicParsing -TimeoutSec 300
        $downloaded = $true
        Write-Host "      Saved to $BinaryPath"
        break
    } catch {
        Write-Host "      Attempt $i/3 failed. Retrying..."
        Start-Sleep -Seconds 5
    }
}
if (-not $downloaded) {
    Write-Error "Failed to download QuickTunnel binary after 3 attempts."
    exit 1
}

# Add to PATH
if ($env:PATH -notlike "*QuickTunnel*") {
    $env:PATH += ";$QtDir"
    try { [Environment]::SetEnvironmentVariable("PATH", [Environment]::GetEnvironmentVariable("PATH","Machine")+";$QtDir", "Machine") } catch {}
}

# ── Join network ──────────────────────────────────────────────────────────────
Write-Host "[5/6] Joining network $NetworkID ..."

$job = Start-Job -ScriptBlock {
    param($bin, $srv, $net)
    & $bin join $srv $net
} -ArgumentList $BinaryPath, $ServerURL, $NetworkID

# Wait up to 60s for config file
$configPath = "$env:USERPROFILE\.quicktunnel\config.json"
$waited = 0
Write-Host "      Waiting for config..." -NoNewline
while (-not (Test-Path $configPath) -and $waited -lt 60) {
    Start-Sleep -Seconds 2
    $waited += 2
    Write-Host "." -NoNewline
}
Write-Host ""

# Clean up the background job
Stop-Job -Job $job -ErrorAction SilentlyContinue
Remove-Job -Job $job -Force -ErrorAction SilentlyContinue

if (Test-Path $configPath) {
    Write-Host "      Config saved: $configPath"
} else {
    Write-Host "      Config not found, running join directly..."
    $joinResult = & $BinaryPath join $ServerURL $NetworkID 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Join failed: $joinResult"
        exit 1
    }
}

# ── Install as Windows Scheduled Task (runs on login + after reboot) ──────────
Write-Host "[6/6] Installing auto-start task..."

$action  = New-ScheduledTaskAction -Execute $BinaryPath -Argument "up" -WorkingDirectory $QtDir
$triggers = @(
    $(New-ScheduledTaskTrigger -AtLogOn),
    $(New-ScheduledTaskTrigger -AtStartup)
)
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1) `
    -ExecutionTimeLimit ([TimeSpan]::Zero)

$principal = New-ScheduledTaskPrincipal `
    -UserId "SYSTEM" `
    -LogonType ServiceAccount `
    -RunLevel Highest

try {
    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $triggers `
        -Settings $settings `
        -Principal $principal `
        -Force | Out-Null
    Write-Host "      Auto-start task installed (runs as SYSTEM on boot + login)"
} catch {
    Write-Host "      [WARN] Could not install as SYSTEM, trying current user..."
    $principal2 = New-ScheduledTaskPrincipal -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) -RunLevel Highest
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $triggers -Settings $settings -Principal $principal2 -Force | Out-Null
    Write-Host "      Auto-start task installed (current user)"
}

# Start the task now
Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "═══════════════════════════════════════════════"
Write-Host "✓ QuickTunnel installed and running!" -ForegroundColor Green
Write-Host "  Network : $NetworkID"
Write-Host "  Binary  : $BinaryPath"
Write-Host "═══════════════════════════════════════════════"
Write-Host ""
Write-Host "Commands:"
Write-Host "  quicktunnel status              - connection info"
Write-Host "  quicktunnel peers               - list peers"
Write-Host "  Get-ScheduledTask QuickTunnel   - task status"
Write-Host ""
Write-Host "Auto-starts on every reboot and login." -ForegroundColor Cyan
Write-Host ""
`, serverURL, networkID, serverURL, networkID)
}