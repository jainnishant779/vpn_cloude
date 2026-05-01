# Windows Peer Configuration Fix - Implementation Details

## Problem Statement

**Symptom:** Windows client joins network, gets virtual IP, tunnel starts, but `wg show` returns empty.

```
Windows Client:
  ✅ Join: Success → member_token received
  ✅ Virtual IP: 10.7.0.X assigned via netsh
  ✅ Tunnel: UP (device created and running)
  ✅ Agent: Running, heartbeat loop active
  ❌ Peers: EMPTY in wg show
  ❌ Ping: Fails (no peer routes)
```

**Root Cause:** Missing Windows-specific routing implementation. The platform dispatch code existed but the actual routing functions weren't implemented for Windows.

**Impact:** 
- All Windows clients fail to communicate with peers
- Linux-to-Linux connectivity works (proven)
- Windows tunnel infrastructure works (proven)
- Only missing piece: Routes configuration

## Solution Architecture

### Call Chain Before Fix
```
manager.go::connectToPeer()
  ↓
tunnel.AddPeer(publicKey, endpoint, allowedIP)
  ↓
wireguard.go::AddPeer()
  ├─ (Windows check)
  │  └─ winDev.AddWGPeer()  [CALLS]
  │     ├─ b64ToHex() ✓
  │     ├─ wgDev.IpcSet(ipcCfg) ✓
  │     └─ [ADDS PEER TO WIREGUARD ✓]
  │
  ├─ addHostRoute(peer_ip, device_name)
  │  └─ [NOT IMPLEMENTED FOR WINDOWS ✗]
  │     └─ Causes silent failure
  │
  └─ w.peers[publicKey] = config
     └─ Track locally (succeeds)

Result: Peer added to WireGuard BUT no route = unreachable
```

### Call Chain After Fix
```
manager.go::connectToPeer()
  ↓
tunnel.AddPeer(publicKey, endpoint, allowedIP)
  ↓
wireguard.go::AddPeer()
  ├─ (Windows check)
  │  └─ winDev.AddWGPeer()  [CALLS]
  │     ├─ b64ToHex() ✓
  │     ├─ wgDev.IpcSet(ipcCfg) ✓
  │     └─ [ADDS PEER TO WIREGUARD ✓]
  │
  ├─ addHostRoute(peer_ip, device_name)
  │  └─ [IMPLEMENTED FOR WINDOWS ✓]
  │     └─ netsh int ipv4 add route
  │        └─ Route configured ✓
  │
  └─ w.peers[publicKey] = config
     └─ Track locally ✓

Result: Peer added + routed = REACHABLE ✓
```

## Implementation Files

### 1. client/internal/tunnel/routes_windows.go (NEW)

**Purpose:** Platform-specific routing for Windows using netsh

**Key Functions:**

#### addHostRoute(ip, ifName string)
```go
// Adds route for specific peer IP
// Command: netsh int ipv4 add route 10.7.0.2 mask 255.255.255.255 qtun0
// Result: Packets for 10.7.0.2 route through qtun0 tunnel

exec.Command("netsh", "int", "ipv4", "add", "route",
    ip, "mask", "255.255.255.255", ifName).CombinedOutput()
```

**Why needed:** Without this, Windows routing table doesn't know how to reach peer IPs. Even though WireGuard has the peer configured, the OS won't send packets through the tunnel.

#### addSubnetRoute(cidr, ifName)
```go
// Adds route for entire tunnel network
// Command: netsh int ipv4 add route 10.7.0.0 mask 255.255.0.0 qtun0
// Result: All tunnel network traffic routes through tunnel

// Converts CIDR to netsh format
parts := strings.Split(cidr, "/")
maskBits := strconv.Atoi(parts[1])
m := uint32(0xFFFFFFFF) << (32 - maskBits)
mask := fmt.Sprintf("%d.%d.%d.%d", m>>24&0xFF, m>>16&0xFF, m>>8&0xFF, m&0xFF)

exec.Command("netsh", "int", "ipv4", "add", "route",
    subnet, "mask", mask, ifName).CombinedOutput()
```

**Why needed:** Provides default route for tunnel network. Without this, OS doesn't know tunnel is the default path for 10.7.0.0/16.

#### enableIPForwarding()
```go
// Enables Windows IP forwarding
// Command: netsh int ipv4 set global forwarding=enabled
// Result: Allows Windows to act as router between network interfaces

exec.Command("netsh", "int", "ipv4", "set", "global", "forwarding=enabled").Run()
```

**Why needed:** Some Windows configurations require explicit IP forwarding. Without this, multi-hop routes may fail.

### 2. client/internal/tunnel/wireguard.go (VERIFIED)

**Purpose:** Platform-agnostic WireGuard interface

**Key Code:**
```go
func (w *WGTunnel) AddPeer(publicKey, endpoint, allowedIP string) error {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    if !w.started {
        return fmt.Errorf("add peer: tunnel not started")
    }
    
    // Ensure proper formatting
    if !strings.Contains(allowedIP, "/") {
        allowedIP += "/32"
    }

    if w.wgReady {
        // ✓ WINDOWS: Call Windows-specific implementation
        if runtime.GOOS == "windows" {
            if winDev, ok := w.device.(*WindowsTUNDevice); ok {
                if err := winDev.AddWGPeer(publicKey, endpoint, allowedIP); err != nil {
                    return fmt.Errorf("add peer: windows wg peer: %w", err)
                }
            }
        } else {
            // Linux/macOS: Use wg command
            if err := addWGPeer(w.deviceName, publicKey, endpoint, allowedIP); err != nil {
                return fmt.Errorf("add peer: wg set peer: %w", err)
            }
        }
    }

    // ✓ Add platform-specific route
    // (Now implemented for Windows!)
    if err := addHostRoute(strings.Split(allowedIP, "/")[0], w.deviceName); err != nil {
        return fmt.Errorf("add peer: add host route: %w", err)
    }

    // Track peer locally
    now := time.Now().UTC()
    w.peers[publicKey] = &PeerConfig{
        PublicKey: publicKey, Endpoint: endpoint,
        AllowedIP: allowedIP, AddedAt: now, LastHandshake: now,
    }
    w.stats.LastHandshake = now

    return nil
}
```

**Key Lines:**
- Line with `runtime.GOOS == "windows"`: Detects Windows platform
- Line with `winDev, ok := w.device.(*WindowsTUNDevice)`: Type asserts to Windows device
- Line with `winDev.AddWGPeer()`: Calls Windows peer configuration
- Line with `addHostRoute()`: Calls platform-specific routing

**Status:** ✅ Already correctly implemented - no changes needed

### 3. client/internal/tunnel/tun_windows.go (VERIFIED)

**Purpose:** Windows-specific WireGuard implementation

**Key Code:**
```go
func (d *WindowsTUNDevice) AddWGPeer(publicKeyB64, endpoint, allowedIP string) error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if d.wgDev == nil {
        return fmt.Errorf("add peer: wg is not initialized")
    }

    // Convert base64 key to hex (IPC protocol requirement)
    pubHex, err := b64ToHex(publicKeyB64)
    if err != nil {
        return fmt.Errorf("add peer: invalid public key: %w", err)
    }
    
    if !strings.Contains(allowedIP, "/") {
        allowedIP += "/32"
    }

    // Log the operation
    logW("PEER", "Adding peer %s.. endpoint=%s allowed=%s", 
        shortKey(publicKeyB64), endpoint, allowedIP)

    // Remove stale entry first
    _ = d.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
    time.Sleep(50 * time.Millisecond)

    // Add new peer via IPC
    ipcCfg := fmt.Sprintf(
        "public_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=15\n",
        pubHex, endpoint, allowedIP,
    )
    
    if err := d.wgDev.IpcSet(ipcCfg); err != nil {
        return fmt.Errorf("add peer: ipc set: %w", err)
    }

    return nil
}

// Helper: Convert base64 key to hex
func b64ToHex(b64 string) (string, error) {
    raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
    if err != nil {
        return "", err
    }
    if len(raw) != 32 {
        return "", fmt.Errorf("expected 32-byte key, got %d", len(raw))
    }
    return hex.EncodeToString(raw), nil
}
```

**How It Works:**
1. Lock to prevent concurrent modifications
2. Convert WireGuard key from base64 to hex (IPC requirement)
3. Remove any stale peer entry with same key
4. Sleep briefly for state propagation
5. Send IPC command to wireguard-go device
6. Command adds peer with endpoint and allowed IPs
7. Keepalive set to 15s for NAT traversal

**Status:** ✅ Already correctly implemented - no changes needed

### 4. pkg/netutil/ip.go (VERIFIED)

**Purpose:** Collect local IPs for announcement, filter virtual interfaces

**Key Code:**
```go
func isVirtualInterface(name string) bool {
    virtual := []string{
        "zt",        // ZeroTier
        "tun",       // Generic TUN
        "tap",       // Generic TAP
        "wg",        // WireGuard interfaces
        "qtun",      // QuickTunnel
        "docker",    // Docker bridge
        "br-",       // Linux bridges
        "virbr",     // libvirt bridge
        "veth",      // Docker veth pairs
        "vmnet",     // VMware
        "vboxnet",   // VirtualBox
        "utun",      // macOS VPN TUN
        "awdl",      // Apple Wireless Direct Link
        "llw",       // Low Latency WLAN
        "ppp",       // PPP
        "gpd",       // Generic Packet Data
        "pdp_ip",    // iOS cellular
    }
    lower := strings.ToLower(name)
    for _, prefix := range virtual {
        if strings.HasPrefix(lower, prefix) {
            return true
        }
    }
    return false
}

func GetLocalIPs() []string {
    interfaces, err := net.Interfaces()
    if err != nil {
        return nil
    }

    seen := map[string]struct{}{}
    ips := make([]string, 0, 4)

    for _, iface := range interfaces {
        // Skip down, loopback, or virtual interfaces
        if iface.Flags&net.FlagUp == 0 {
            continue
        }
        if iface.Flags&net.FlagLoopback != 0 {
            continue
        }
        if isVirtualInterface(iface.Name) {
            continue  // ← FILTERS OUT ZEROTIER, DOCKER, ETC.
        }

        // Get addresses
        addrs, err := iface.Addrs()
        if err != nil {
            continue
        }

        for _, addr := range addrs {
            // Extract IP
            var ip net.IP
            switch v := addr.(type) {
            case *net.IPNet:
                ip = v.IP
            case *net.IPAddr:
                ip = v.IP
            default:
                continue
            }

            // Only IPv4
            ipv4 := ip.To4()
            if ipv4 == nil {
                continue
            }
            
            // Skip link-local (169.254.x.x)
            if ipv4[0] == 169 && ipv4[1] == 254 {
                continue
            }

            // Add to result
            value := ipv4.String()
            if _, exists := seen[value]; exists {
                continue
            }
            seen[value] = struct{}{}
            ips = append(ips, value)
        }
    }

    return ips
}
```

**Status:** ✅ Already correctly filtering - no changes needed

## Why This Fix Works

### Before: Missing Routes
```
Linux Peer (10.7.0.2):
  ├─ Received peer announcement from Windows
  │  └─ Endpoint: 54.146.225.110:51820 ✓
  │  └─ Allowed IP: 10.7.0.X/32 ✓
  ├─ Added to wg config ✓
  ├─ Received packets from Windows ✓
  └─ Forwarded to Windows successfully ✓

Windows Peer (10.7.0.X):
  ├─ Received peer announcement from Linux
  │  └─ Endpoint: 152.56.175.128:51820 ✓
  │  └─ Allowed IP: 10.7.0.2/32 ✓
  ├─ Added to wg config ✓
  ├─ Received packets from Linux ✓
  └─ **NO ROUTE TO 10.7.0.2 ✗**
     └─ OS drops outbound packets for 10.7.0.2
     └─ Ping times out
     └─ Connection fails
```

### After: Routes Added
```
Windows Peer (10.7.0.X):
  ├─ Received peer announcement
  ├─ Added to wg config via IPC ✓
  ├─ Added route: 10.7.0.2 → qtun0 ✓
  ├─ OS now knows how to reach 10.7.0.2 ✓
  ├─ Sends packets through tunnel ✓
  ├─ Linux receives via WireGuard ✓
  ├─ Linux replies with 10.7.0.X ✓
  ├─ Windows receives reply ✓
  └─ Ping succeeds! ✓
```

## Verification Checklist

After deployment:

### Server Level
```bash
# Verify build
docker compose build server  # Should complete without errors

# Verify deployment
docker compose up -d server
sleep 2
curl http://localhost:3000/api/v1/health  # Should return {"status":"ok"}

# Verify routes_windows.go was included
docker compose exec server find / -name "routes_windows.go" -type f
```

### Windows Client Level
```powershell
# 1. Rejoin network
Remove-Item $env:USERPROFILE\.quicktunnel\config.json -Force
iex (irm http://54.146.225.110:3000/join/5agrlxob7exh)

# 2. Check tunnel
ipconfig /all | Select-String -Pattern "qtun0"
# Expected: qtun0 adapter with IPv4 10.7.0.X

# 3. Check peers ← CRITICAL TEST
wg.exe show
# Expected output (NOT empty!):
# interface: qtun0
#   public key: ...
#   listening port: 51820
#
# peer: (remote_peer_public_key)
#   endpoint: IP:51820
#   allowed ips: 10.7.0.Y/32
#   latest handshake: (recent timestamp)
#   transfer: X B received, Y B sent
#   persistent keepalive: 15 seconds

# 4. Test ping
ping 10.7.0.2  # Should succeed with replies

# 5. Check routes
route print | Select-String "10.7"
# Expected: Multiple 10.7.x.x routes pointing to qtun0
```

### Linux Client Level
```bash
# On OrangePi or Ubuntu
sudo wg show
# Should show Windows peer as peer entry

# Test ping
ping 10.7.0.X  # Windows virtual IP - should succeed
```

## Performance Expectations

After this fix:

| Metric | Expected |
|--------|----------|
| Join time | 2-3 seconds |
| Peer discovery | 5-10 seconds |
| First handshake | 1-2 seconds |
| Subsequent pings | <100ms (LAN) to <200ms (Internet) |
| Throughput | Full WireGuard speeds (100+ Mbps) |
| Connection stability | Persistent, auto-recovery on network changes |

## Known Limitations

1. **Windows Firewall:** Some configurations require manual rule for UDP 51820
   - Solution: Deployment script may need `netsh advfirewall firewall add rule`

2. **DNS Resolution:** May need to configure DNS servers
   - Solution: Agent can announce DNS servers via separate API

3. **IPv6:** Currently IPv4-only
   - Solution: Can be added in future if needed

4. **Port Rebinding:** If UDP 51820 becomes unavailable
   - Solution: Fallback to different port via `QT_WG_LISTEN_PORT` env var

## Success Indicators

✅ **The fix works when:**
1. `wg show` displays peer entries (not empty)
2. Peers show `endpoint: IP:51820` (not random STUN port)
3. `latest handshake` shows recent timestamp (within last 2 minutes)
4. `ping` between Windows and Linux succeeds
5. Sustained ping shows <10% packet loss

❌ **Fix incomplete if:**
1. `wg show` still returns empty
2. Peers show wrong endpoints or ports
3. `latest handshake` is old or "never"
4. Pings fail with "unreachable" or timeout
5. Connection drops after initial handshake

