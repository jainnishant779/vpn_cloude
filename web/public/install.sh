#!/bin/bash
set -e

SERVER_URL="${QUICKTUNNEL_SERVER:-__SERVER_URL__}"
NETWORK_ID="$1"
API_KEY="$2"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="quicktunnel"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[QuickTunnel]${NC} $1"; }
warn() { echo -e "${YELLOW}[QuickTunnel]${NC} $1"; }
err()  { echo -e "${RED}[QuickTunnel]${NC} $1"; exit 1; }

# Detect OS and ARCH
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    armv7l)  ARCH="arm7" ;;
    arm64)   ARCH="arm64" ;;
    *) err "Unsupported architecture: $ARCH" ;;
esac

case "$OS" in
    linux|darwin) ;;
    *) err "Unsupported OS: $OS" ;;
esac

BINARY_NAME="quicktunnel-${OS}-${ARCH}"
DOWNLOAD_URL="${SERVER_URL}/api/downloads/${OS}/${ARCH}"

log "Detected: ${OS}/${ARCH}"
log "Downloading QuickTunnel client..."

curl -fsSL "$DOWNLOAD_URL" -o /tmp/quicktunnel || err "Download failed"
chmod +x /tmp/quicktunnel
mv /tmp/quicktunnel "$INSTALL_DIR/quicktunnel"

log "Installed to ${INSTALL_DIR}/quicktunnel"

# Join network if args provided
if [ -n "$NETWORK_ID" ] && [ -n "$API_KEY" ]; then
    log "Joining network: $NETWORK_ID"
    quicktunnel join --server "$SERVER_URL" --network "$NETWORK_ID" --api-key "$API_KEY"
fi

# Create systemd service
if [ -d /etc/systemd/system ] && [ -n "$NETWORK_ID" ]; then
    log "Creating systemd service..."
    cat > /etc/systemd/system/quicktunnel.service << SVCEOF
[Unit]
Description=QuickTunnel VPN Client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/quicktunnel up --server ${SERVER_URL}
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SVCEOF

    systemctl daemon-reload
    systemctl enable quicktunnel
    systemctl start quicktunnel
    log "Service created and started!"
fi

log "✅ QuickTunnel installed successfully!"
log "Usage: quicktunnel join --server ${SERVER_URL} --network <ID> --api-key <KEY>"
