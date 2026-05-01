# Windows Ping Fix - Production Ready

## Root Cause Analysis

Windows tunnel starts successfully but peers don't respond to ping. Root causes:

1. **AddWGPeer not being called** - Peer manager doesn't call AddWGPeer() on Windows
2. **Endpoint announcement issue** - publicEndpoint might still have wrong port
3. **Local endpoint filtering** - Windows might be announcing ZeroTier IPs instead of real IPs

## Critical Fixes Needed

### 1. Enable Peer Configuration on Windows
- wireguard.go needs to call AddWGPeer for each peer
- manager.go connectToPeer must not skip Windows

### 2. Verify WireGuard Port is 51820
- Check agent.go sends port 51820, not STUN random port

### 3. Fix Local IP Collection
- netutil.GetLocalIPs() must skip ZeroTier interfaces

## Implementation Steps

1. Fix wireguard.go to call AddWGPeer
2. Verify preferIPv4 logic
3. Test Windows endpoint setup
4. Rebuild and deploy
5. Windows rejoin + verify wg peers

