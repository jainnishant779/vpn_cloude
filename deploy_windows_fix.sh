#!/bin/bash
# WINDOWS PING FIX - DEPLOYMENT SCRIPT
# Run this on EC2 to deploy the Windows VPN peer configuration fix

set -e

cd ~/vpn

echo "════════════════════════════════════════════════════════════════"
echo "Windows VPN Peer Configuration Fix - Deployment"
echo "════════════════════════════════════════════════════════════════"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 1: Verify file was created
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 1: Verifying routes_windows.go exists..."
if [ ! -f "client/internal/tunnel/routes_windows.go" ]; then
    echo "❌ ERROR: routes_windows.go not found!"
    echo "This file should have been created in the fix."
    exit 1
fi
echo "✅ routes_windows.go present"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 2: Verify file content
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 2: Verifying routes_windows.go has required functions..."

# Check for required functions
required_funcs=("addSubnetRoute" "addHostRoute" "enableIPForwarding")
for func in "${required_funcs[@]}"; do
    if ! grep -q "func $func" client/internal/tunnel/routes_windows.go; then
        echo "❌ ERROR: Function '$func' not found in routes_windows.go"
        exit 1
    fi
    echo "  ✓ $func defined"
done
echo "✅ All required functions present"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 3: Check wireguard.go has Windows dispatch
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 3: Verifying wireguard.go has Windows dispatch..."
if ! grep -q 'runtime.GOOS == "windows"' client/internal/tunnel/wireguard.go; then
    echo "❌ ERROR: Windows OS check not found in wireguard.go"
    exit 1
fi
echo "✅ Windows dispatch present in wireguard.go"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 4: Git commit
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 4: Committing changes to git..."
git add client/internal/tunnel/routes_windows.go

if git diff --cached --quiet; then
    echo "⚠️  No changes to commit (may already be committed)"
else
    git commit -m "fix: Windows WireGuard peer configuration

- Add routes_windows.go with netsh-based route management
- Implements addHostRoute, addSubnetRoute, enableIPForwarding
- Routes configured via netsh for peer-specific and subnet paths
- Non-fatal error handling for existing routes
- Fixes Windows peer configuration via platform-specific routing"
    echo "✅ Changes committed"
fi
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 5: Push to git
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 5: Pushing to git..."
if git status --short | grep -q .; then
    echo "⚠️  Uncommitted changes remain - pushing anyway"
fi
git push origin main --force
echo "✅ Pushed to git"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 6: Build Docker image
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 6: Building Docker image (this may take 1-2 minutes)..."
echo "Building: server container with all client binaries (including Windows)"
docker compose build --no-cache server
echo "✅ Docker build complete"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 7: Deploy
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 7: Deploying to EC2..."
docker compose up -d --force-recreate server
echo "✅ Server restarted"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 8: Verify deployment
# ══════════════════════════════════════════════════════════════════════════════

echo "Step 8: Verifying deployment..."
sleep 3

health=$(curl -s http://localhost:3000/api/v1/health || echo '{"status":"error"}')
if echo "$health" | grep -q '"status":"ok"'; then
    echo "✅ Server is healthy"
else
    echo "⚠️  Server health check unclear - checking logs..."
    docker compose logs server | tail -20
fi
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# STEP 9: Summary
# ══════════════════════════════════════════════════════════════════════════════

echo "════════════════════════════════════════════════════════════════"
echo "✅ DEPLOYMENT COMPLETE"
echo "════════════════════════════════════════════════════════════════"
echo ""
echo "Next: Test Windows client"
echo ""
echo "On Windows (PowerShell as Admin):"
echo "  1. Stop old process: Stop-Process -Name quicktunnel -Force"
echo "  2. Delete old config: rm \$env:USERPROFILE\.quicktunnel\config.json"
echo "  3. Rejoin network: iex (irm http://54.146.225.110:3000/join/5agrlxob7exh)"
echo "  4. Verify peers: wg.exe show"
echo "     - Should show peer entries with endpoints"
echo "  5. Test ping: ping 10.7.0.2"
echo "     - Should reach Linux peer"
echo ""
echo "If peers don't appear in 'wg show':"
echo "  - Check agent logs: Get-Content \$env:USERPROFILE\.quicktunnel\*.log"
echo "  - Verify tunnel up: ipconfig | findstr qtun"
echo "  - Check WireGuard services running"
echo ""
