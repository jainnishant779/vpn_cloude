#!/bin/bash
cd ~/vpn/web
npm run build
cd ~/vpn/server
/usr/local/go/bin/go build -o quicktunnel-server ./cmd/server
sudo pkill -f quicktunnel-server || true
cd ~/vpn
sudo nohup ./server/quicktunnel-server > server.log 2>&1 < /dev/null &
echo "Server restarted from $PWD"
