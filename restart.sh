#!/bin/bash
set -e

echo "=== QuickTunnel Full Rebuild & Restart ==="

# Build server
cd ~/vpn/server
echo "Building server..."
/usr/local/go/bin/go build -o quicktunnel-server ./cmd/server

# Build client (for this platform)
cd ~/vpn
echo "Building client..."
/usr/local/go/bin/go build -trimpath -ldflags "-s -w" -o ./binaries/quicktunnel ./client/cmd/quicktunnel

# Stop existing server
sudo pkill -f quicktunnel-server || true
sleep 1

# Start server
cd ~/vpn
export PUBLIC_SERVER_URL="http://3.93.45.218:3000"
sudo -E nohup ./server/quicktunnel-server > server.log 2>&1 < /dev/null &
echo "Server restarted (PID: $!)"

# Restart client tunnel if installed as service
if systemctl is-active --quiet quicktunnel 2>/dev/null; then
  sudo systemctl restart quicktunnel
  echo "Client service restarted"
fi

echo "=== Done ==="
echo "Server logs: tail -f ~/vpn/server.log"
