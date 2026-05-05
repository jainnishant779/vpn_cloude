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

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
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
# QuickTunnel — zero-touch installer + systemd service setup
# Usage: curl %s/join/<network_id> | sudo bash
set -euo pipefail

SERVER_URL="%s"
BINARY="/usr/local/bin/quicktunnel"
SERVICE_NAME="quicktunnel"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
CONFIG_DIR_SYS="/etc/quicktunnel"
CONFIG_DIR_HOME="/root/.quicktunnel"
JOIN_LOG="/var/log/quicktunnel-join.log"
%s

# Must be root (we write to /usr/local/bin and /etc/systemd/system)
if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: This installer must be run as root (use sudo)."
    exit 1
fi

# ── 1. Platform detection ─────────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    armv7*|armv6*) ARCH="armv7" ;;
    *) echo "Unsupported arch: $ARCH_RAW"; exit 1 ;;
esac
echo "[1/6] Platform: $OS/$ARCH"

# ── 2. Cleanup old instance ───────────────────────────────────────────────────
echo "[2/6] Cleaning up old instance..."
systemctl stop "$SERVICE_NAME" 2>/dev/null || true
systemctl disable "$SERVICE_NAME" 2>/dev/null || true
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
        yum install -y wireguard-tools 2>/dev/null || \
        dnf install -y wireguard-tools 2>/dev/null || \
        apk add --no-cache wireguard-tools 2>/dev/null || true
    fi
fi

# ── 4. Download binary ────────────────────────────────────────────────────────
DOWNLOAD_URL="$SERVER_URL/api/v1/downloads/client/$OS/$ARCH"
echo "[3/6] Downloading from $DOWNLOAD_URL ..."
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

# ── 5. Join the network (wait for approval + config) ─────────────────────────
echo "[4/6] Joining network $NETWORK_ID ..."
mkdir -p "$CONFIG_DIR_SYS"
mkdir -p "$CONFIG_DIR_HOME"

# Function to verify config has required fields
config_valid() {
    if [ ! -f "$1" ]; then return 1; fi
    # Check for member_token and virtual_ip in config
    grep -q '"member_token"' "$1" 2>/dev/null && \
    grep -q '"virtual_ip"' "$1" 2>/dev/null && \
    ! grep -q '"member_token":""' "$1" 2>/dev/null
}

config_found() {
    [ -f "$CONFIG_DIR_SYS/config.json" ] && config_valid "$CONFIG_DIR_SYS/config.json" && return 0
    [ -f "$CONFIG_DIR_HOME/config.json" ] && config_valid "$CONFIG_DIR_HOME/config.json" && return 0
    [ -f "/root/.quicktunnel/config.json" ] && config_valid "/root/.quicktunnel/config.json" && return 0
    return 1
}

# Join with retry logic
JOIN_ATTEMPT=0
MAX_ATTEMPTS=3
while [ $JOIN_ATTEMPT -lt $MAX_ATTEMPTS ]; do
    JOIN_ATTEMPT=$((JOIN_ATTEMPT + 1))
    echo "      Join attempt $JOIN_ATTEMPT of $MAX_ATTEMPTS..."
    
    HOME=/root "$BINARY" join "$SERVER_URL" "$NETWORK_ID" >"$JOIN_LOG" 2>&1 &
    JOIN_PID=$!

    echo -n "      Waiting for approval..."
    JOIN_WAIT=0
    MAX_WAIT=180  # Increased from 300 to 180 seconds (3 minutes)
    while [ $JOIN_WAIT -lt $MAX_WAIT ]; do
        if config_found; then
            echo ""
            echo "      ✓ Join successful (config created)"
            kill -TERM "$JOIN_PID" 2>/dev/null || true
            sleep 1
            kill -KILL "$JOIN_PID" 2>/dev/null || true
            break 2  # Break both loops
        fi
        if ! kill -0 "$JOIN_PID" 2>/dev/null; then
            echo ""
            break  # Process exited, try again
        fi
        sleep 2
        JOIN_WAIT=$((JOIN_WAIT + 2))
        echo -n "."
    done

    if ! kill -0 "$JOIN_PID" 2>/dev/null; then
        wait "$JOIN_PID" 2>/dev/null
    else
        kill -TERM "$JOIN_PID" 2>/dev/null || true
        sleep 1
        kill -KILL "$JOIN_PID" 2>/dev/null || true
    fi
    
    if config_found; then
        break
    elif [ $JOIN_ATTEMPT -lt $MAX_ATTEMPTS ]; then
        echo "      Retrying in 5 seconds..."
        sleep 5
    fi
done

if ! config_found; then
    echo ""
    echo "ERROR: Join failed after $MAX_ATTEMPTS attempts."
    echo ""
    echo "Troubleshooting:"
    echo "  1. Check dashboard - approve member if pending"
    echo "  2. Verify server is reachable: ping $(echo "$SERVER_URL" | sed 's|.*://||; s|:.*||')"
    echo "  3. Check firewall rules"
    echo ""
    echo "Last 30 lines of join log:"
    tail -n 30 "$JOIN_LOG" 2>/dev/null || true
    exit 1
fi

# Show config details
if [ -f "$CONFIG_DIR_SYS/config.json" ]; then
    CONFIG_FOUND="$CONFIG_DIR_SYS/config.json"
elif [ -f "$CONFIG_DIR_HOME/config.json" ]; then
    CONFIG_FOUND="$CONFIG_DIR_HOME/config.json"
else
    CONFIG_FOUND="/root/.quicktunnel/config.json"
fi
echo "✓ Config: $CONFIG_FOUND"

# ── 6. Write systemd unit file ───────────────────────────────────────────────
echo "[5/6] Installing systemd service..."
cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=QuickTunnel mesh VPN client
Documentation=$SERVER_URL
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=HOME=/root
ExecStart=$BINARY up
Restart=always
RestartSec=5
User=root
LimitNOFILE=65536
StandardOutput=journal
StandardError=journal
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF

chmod 644 "$SERVICE_FILE"

# ── 7. Reload, enable, and start ─────────────────────────────────────────────
echo "[6/6] Enabling and starting service..."
systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
sleep 2

# ── Verify ───────────────────────────────────────────────────────────────────
if systemctl is-active --quiet "$SERVICE_NAME"; then
    STATUS="active ✓"
else
    STATUS="NOT running ✗ — check: journalctl -u $SERVICE_NAME -n 50"
fi

echo ""
echo "═══════════════════════════════════════════════"
echo "✓ QuickTunnel installed!"
echo "  Virtual network : $NETWORK_ID"
echo "  Service status  : $STATUS"
echo "  Boot persistence: enabled (systemctl is-enabled $SERVICE_NAME)"
echo "  Manage service  : systemctl {status|restart|stop} $SERVICE_NAME"
echo "  Live logs       : journalctl -u $SERVICE_NAME -f"
echo "  Peers           : quicktunnel peers"
echo "═══════════════════════════════════════════════"
echo ""
echo "Terminal band karne pe bhi service chalti rahegi (systemd manage kar raha hai)."
`, serverURL, serverURL, networkBlock)
}

func buildPS1Script(serverURL, networkID string) string {
	return fmt.Sprintf(`# QuickTunnel — Windows installer + Windows Service
# Run in PowerShell as Administrator:
# irm %[1]s/join/%[2]s/ps1 | iex

$ErrorActionPreference = "Stop"
$ServerURL   = "%[1]s"
$NetworkID   = "%[2]s"
$QtDir       = "$env:ProgramFiles\QuickTunnel"
$BinaryPath  = "$QtDir\quicktunnel.exe"
$ConfigDir   = "$env:ProgramData\QuickTunnel"
$ConfigFile  = "$ConfigDir\config.json"
$WintunPath  = "$QtDir\wintun.dll"
$WGPath      = "$env:ProgramFiles\WireGuard"
$DownloadURL = "$ServerURL/api/v1/downloads/client/windows/amd64"
$ServiceName = "QuickTunnel"
$NssmPath    = "$QtDir\nssm.exe"
$LogFile     = "$QtDir\service.log"

Write-Host ""
Write-Host "══════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  QuickTunnel Windows Installer"
Write-Host "══════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# ── 1. Setup directories ─────────────────────────────────────────────────────
Write-Host "[1/7] Setting up directories..."
New-Item -ItemType Directory -Force -Path $QtDir | Out-Null
New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null

# ── 2. Stop existing ─────────────────────────────────────────────────────────
Write-Host "[2/7] Stopping existing instance..."
if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
    if (Test-Path $NssmPath) {
        & $NssmPath remove $ServiceName confirm 2>$null
    } else {
        sc.exe delete $ServiceName 2>$null
    }
    Start-Sleep -Seconds 1
}
Stop-Process -Name "quicktunnel" -Force -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName $ServiceName -Confirm:$false -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

# ── 3. Install WireGuard ─────────────────────────────────────────────────────
Write-Host "[3/7] Installing WireGuard..."
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
    try {
        [Environment]::SetEnvironmentVariable("PATH",
            [Environment]::GetEnvironmentVariable("PATH","Machine") + ";$WGPath", "Machine")
    } catch {}
}

# ── 4. Install WinTun ────────────────────────────────────────────────────────
Write-Host "[4/7] Installing WinTun driver..."
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

# ── 5. Download QuickTunnel + NSSM ───────────────────────────────────────────
Write-Host "[5/7] Downloading QuickTunnel binary..."
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
    try {
        [Environment]::SetEnvironmentVariable("PATH",
            [Environment]::GetEnvironmentVariable("PATH","Machine") + ";$QtDir", "Machine")
    } catch {}
}

# Download NSSM
Write-Host "      Downloading NSSM service manager..."
if (-not (Test-Path $NssmPath)) {
    $nssmZip = "$env:TEMP\nssm.zip"
    try {
        Invoke-WebRequest -Uri "https://nssm.cc/release/nssm-2.24.zip" -OutFile $nssmZip -UseBasicParsing -TimeoutSec 120
        Expand-Archive -Path $nssmZip -DestinationPath "$env:TEMP\nssm_extract" -Force
        Copy-Item "$env:TEMP\nssm_extract\nssm-2.24\win64\nssm.exe" -Destination $NssmPath -Force
        Write-Host "      NSSM installed."
    } catch {
        Write-Host "      [WARN] NSSM download failed, will use fallback." -ForegroundColor Yellow
    }
}

# ── 6. Join network ──────────────────────────────────────────────────────────
Write-Host "[6/7] Joining network $NetworkID ..."

# Function to verify config has required fields
function Test-ConfigValid {
    param([string]$Path)
    if (-not (Test-Path $Path)) { return $false }
    try {
        $cfg = Get-Content $Path -Raw | ConvertFrom-Json
        # Check for member_token (authentication) and virtual_ip
        return ($null -ne $cfg.member_token -and $null -ne $cfg.virtual_ip)
    } catch {
        return $false
    }
}

# Retry loop for join
$joinSuccess = $false
$joinAttempt = 0
$maxAttempts = 3

while ($joinAttempt -lt $maxAttempts -and -not $joinSuccess) {
    $joinAttempt++
    Write-Host "      Join attempt $joinAttempt of $maxAttempts..."
    
    $joinProc = Start-Process -FilePath $BinaryPath -ArgumentList "join", $ServerURL, $NetworkID -PassThru -NoNewWindow -RedirectStandardOutput "$QtDir\join_stdout.log" -RedirectStandardError "$QtDir\join_stderr.log"

    $waited = 0
    $maxWait = 180  # Increased from 60 to 180 seconds (3 minutes for approval)
    Write-Host "      Waiting for approval..." -NoNewline
    
    while ($waited -lt $maxWait) {
        Start-Sleep -Seconds 2
        $waited += 2
        Write-Host "." -NoNewline
        
        if (Test-ConfigValid $ConfigFile) {
            Write-Host ""
            Write-Host "      ✓ Config created with valid credentials" -ForegroundColor Green
            $joinSuccess = $true
            break
        }
        
        if ($joinProc.HasExited) {
            Write-Host ""
            Write-Host "      [!] Join process exited unexpectedly" -ForegroundColor Yellow
            break
        }
    }
    
    if (-not $joinProc.HasExited) {
        Stop-Process -Id $joinProc.Id -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 1
    }
    
    if ($joinSuccess) {
        Write-Host "      ✓ Join successful"
        break
    } elseif ($joinAttempt -lt $maxAttempts) {
        Write-Host "      Retrying in 5 seconds..." -ForegroundColor Yellow
        Start-Sleep -Seconds 5
    }
}

# Final verification
if (-not $joinSuccess) {
    Write-Host ""
    Write-Host "      [!] Failed to join network after $maxAttempts attempts" -ForegroundColor Red
    Write-Host ""
    Write-Host "      Troubleshooting:" -ForegroundColor Yellow
    Write-Host "        1. Check dashboard - approve member if pending"
    Write-Host "        2. Verify network is reachable: Test-NetConnection $ServerURL"
    Write-Host "        3. Check firewall rules"
    Write-Host ""
    Write-Host "      Join stdout:" -ForegroundColor Cyan
    if (Test-Path "$QtDir\join_stdout.log") { Get-Content "$QtDir\join_stdout.log" | Select-Object -Last 30 }
    Write-Host ""
    Write-Host "      Join stderr:" -ForegroundColor Cyan
    if (Test-Path "$QtDir\join_stderr.log") { Get-Content "$QtDir\join_stderr.log" | Select-Object -Last 30 }
    Write-Error "Join failed. Rerun installer after resolving issue."
    exit 1
}

if (Test-Path $ConfigFile) {
    $cfg = Get-Content $ConfigFile -Raw | ConvertFrom-Json
    Write-Host "      Member ID  : $($cfg.member_id)"
    Write-Host "      Virtual IP : $($cfg.virtual_ip)"
} else {
    Write-Error "Config file missing after successful join."
    exit 1
}

# ── 7. Install Windows Service ────────────────────────────────────────────────
Write-Host "[7/7] Installing Windows service..."

if (Test-Path $NssmPath) {
    & $NssmPath install $ServiceName $BinaryPath "up" 2>$null
    & $NssmPath set $ServiceName AppDirectory $QtDir 2>$null
    & $NssmPath set $ServiceName DisplayName "QuickTunnel VPN" 2>$null
    & $NssmPath set $ServiceName Description "QuickTunnel mesh VPN network service" 2>$null
    & $NssmPath set $ServiceName Start SERVICE_AUTO_START 2>$null
    & $NssmPath set $ServiceName AppStdout $LogFile 2>$null
    & $NssmPath set $ServiceName AppStderr $LogFile 2>$null
    & $NssmPath set $ServiceName AppRotateFiles 1 2>$null
    & $NssmPath set $ServiceName AppRotateBytes 1048576 2>$null
    & $NssmPath set $ServiceName AppEnvironmentExtra "QUICKTUNNEL_CONFIG=$ConfigFile" 2>$null
    & $NssmPath set $ServiceName AppRestartDelay 10000 2>$null

    try {
        Start-Service -Name $ServiceName -ErrorAction Stop
        Write-Host "      Windows service installed and started (NSSM)" -ForegroundColor Green
    } catch {
        Write-Host "      [WARN] Service start failed: $_" -ForegroundColor Yellow
        & $NssmPath start $ServiceName 2>$null
    }
} else {
    Write-Host "      NSSM not available, using fallback method..."

    Start-Process -FilePath $BinaryPath -ArgumentList "up" -WindowStyle Hidden -WorkingDirectory $QtDir

    $action   = New-ScheduledTaskAction -Execute $BinaryPath -Argument "up" -WorkingDirectory $QtDir
    $triggers = @(
        $(New-ScheduledTaskTrigger -AtLogOn),
        $(New-ScheduledTaskTrigger -AtStartup)
    )
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
    $principal = New-ScheduledTaskPrincipal -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) -RunLevel Highest

    Register-ScheduledTask -TaskName $ServiceName -Action $action -Trigger $triggers -Settings $settings -Principal $principal -Force | Out-Null

    Write-Host "      Detached process started + scheduled task created" -ForegroundColor Green
}

# ── Verify ────────────────────────────────────────────────────────────────────
Start-Sleep -Seconds 3
$running = Get-Process -Name "quicktunnel" -ErrorAction SilentlyContinue

Write-Host ""
if ($running) {
    Write-Host "═══════════════════════════════════════════════════" -ForegroundColor Green
    Write-Host "  QuickTunnel installed and RUNNING!" -ForegroundColor Green
    Write-Host "═══════════════════════════════════════════════════" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Network  : $NetworkID"
    Write-Host "  Binary   : $BinaryPath"
    Write-Host "  Config   : $ConfigFile"
    Write-Host "  Logs     : $LogFile"
    Write-Host ""
    Write-Host "  PowerShell band karo - service chalti rahegi!" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  Commands:"
    Write-Host "    quicktunnel status          - connection info"
    Write-Host "    quicktunnel peers           - list peers"
    Write-Host "    Get-Service QuickTunnel     - service status"
    Write-Host "    Restart-Service QuickTunnel - restart service"
    Write-Host ""
} else {
    Write-Host "═══════════════════════════════════════════════════" -ForegroundColor Red
    Write-Host "  [!] QuickTunnel process not detected!" -ForegroundColor Red
    Write-Host "═══════════════════════════════════════════════════" -ForegroundColor Red
    Write-Host ""
    Write-Host "  Check logs: Get-Content '$LogFile'"
    Write-Host "  Try manually: & '$BinaryPath' up"
    Write-Host ""
}
`, serverURL, networkID)
}