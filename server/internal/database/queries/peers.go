package queries

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"quicktunnel/server/internal/database"
	"quicktunnel/server/internal/models"
)

// PeerStatusUpdate captures mutable runtime state from heartbeat updates.
type PeerStatusUpdate struct {
	PublicEndpoint string
	LocalEndpoints []string
	VNCAvailable   bool
	RXBytes        int64
	TXBytes        int64
	RelayID        string
}

// PeerStore handles persistence for peer records.
type PeerStore struct {
	db *database.DB
}

func NewPeerStore(db *database.DB) *PeerStore {
	return &PeerStore{db: db}
}

// peerColumns lists all columns for peer SELECT queries, including the new
// status and member_token fields added in migration 007.
const peerColumns = `id, network_id, name, machine_id, public_key, virtual_ip, public_endpoint,
       COALESCE(local_endpoints, '{}'::text[]), os, version, is_online, last_seen, last_handshake,
       rx_bytes, tx_bytes, vnc_port, vnc_available, relay_id, status, COALESCE(member_token, ''), created_at`

func (s *PeerStore) RegisterPeer(ctx context.Context, peer *models.Peer) (*models.Peer, error) {
	if peer == nil {
		return nil, fmt.Errorf("register peer: peer is nil")
	}
	if strings.TrimSpace(peer.MemberToken) == "" {
		token, err := generateMemberToken()
		if err != nil {
			return nil, fmt.Errorf("register peer: generate token: %w", err)
		}
		peer.MemberToken = token
	}

	query := `
INSERT INTO peers (
    network_id, name, machine_id, public_key, virtual_ip, public_endpoint,
    local_endpoints, os, version, is_online, vnc_port, vnc_available, relay_id, status, member_token
)
	VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, '{}'::text[]), $8, $9, true, $10, $11, NULLIF($12, ''), $13, NULLIF($14, ''))
ON CONFLICT (machine_id)
DO UPDATE SET
    network_id = EXCLUDED.network_id,
    name = EXCLUDED.name,
    public_key = EXCLUDED.public_key,
    virtual_ip = EXCLUDED.virtual_ip,
    public_endpoint = EXCLUDED.public_endpoint,
		local_endpoints = COALESCE(EXCLUDED.local_endpoints, '{}'::text[]),
    os = EXCLUDED.os,
    version = EXCLUDED.version,
    is_online = true,
    last_seen = NOW(),
    vnc_port = EXCLUDED.vnc_port,
    vnc_available = EXCLUDED.vnc_available,
    relay_id = EXCLUDED.relay_id
RETURNING ` + peerColumns + `;`

	row := s.db.Pool.QueryRow(
		ctx,
		query,
		peer.NetworkID,
		peer.Name,
		peer.MachineID,
		peer.PublicKey,
		peer.VirtualIP,
		peer.PublicEndpoint,
		peer.LocalEndpoints,
		peer.OS,
		peer.Version,
		peer.VNCPort,
		peer.VNCAvailable,
		peer.RelayID,
		peer.Status,
		peer.MemberToken,
	)

	out, err := scanPeer(row)
	if err != nil {
		return nil, fmt.Errorf("register peer: %w", err)
	}

	return out, nil
}

// RegisterPendingPeer creates a peer with status 'pending' and no virtual IP.
// Used by the unauthenticated join endpoint.
func (s *PeerStore) RegisterPendingPeer(ctx context.Context, peer *models.Peer) (*models.Peer, error) {
	if peer == nil {
		return nil, fmt.Errorf("register pending peer: peer is nil")
	}

	if peer.MemberToken == "" {
		token, err := generateMemberToken()
		if err != nil {
			return nil, fmt.Errorf("register pending peer: generate token: %w", err)
		}
		peer.MemberToken = token
	}

	query := `
INSERT INTO peers (
    network_id, name, machine_id, public_key, virtual_ip, public_endpoint,
    local_endpoints, os, version, is_online, vnc_port, vnc_available, status, member_token
)
VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, '')::inet, '0.0.0.0'::inet), $6, COALESCE($7, '{}'::text[]),
        $8, $9, false, $10, false, $11, $12)
ON CONFLICT (machine_id)
DO UPDATE SET
    name = EXCLUDED.name,
    public_key = EXCLUDED.public_key,
    os = EXCLUDED.os,
    version = EXCLUDED.version,
    last_seen = NOW()
RETURNING ` + peerColumns + `;`

	row := s.db.Pool.QueryRow(
		ctx, query,
		peer.NetworkID,
		peer.Name,
		peer.MachineID,
		peer.PublicKey,
		peer.VirtualIP,
		peer.PublicEndpoint,
		peer.LocalEndpoints,
		peer.OS,
		peer.Version,
		peer.VNCPort,
		peer.Status,
		peer.MemberToken,
	)

	out, err := scanPeer(row)
	if err != nil {
		return nil, fmt.Errorf("register pending peer: %w", err)
	}
	return out, nil
}

func (s *PeerStore) GetPeer(ctx context.Context, peerID string) (*models.Peer, error) {
	query := `SELECT ` + peerColumns + ` FROM peers WHERE id = $1;`

	peer, err := scanPeer(s.db.Pool.QueryRow(ctx, query, peerID))
	if err != nil {
		return nil, fmt.Errorf("get peer: %w", mapNotFound(err))
	}
	return peer, nil
}

// GetPeerByMemberToken looks up a peer by its device member_token.
func (s *PeerStore) GetPeerByMemberToken(ctx context.Context, token string) (*models.Peer, error) {
	query := `SELECT ` + peerColumns + ` FROM peers WHERE member_token = $1;`

	peer, err := scanPeer(s.db.Pool.QueryRow(ctx, query, token))
	if err != nil {
		return nil, fmt.Errorf("get peer by member token: %w", mapNotFound(err))
	}
	return peer, nil
}

// GetPeerByPublicKey finds a peer by WireGuard public key within a network.
func (s *PeerStore) GetPeerByPublicKey(ctx context.Context, networkID, publicKey string) (*models.Peer, error) {
	query := `SELECT ` + peerColumns + ` FROM peers WHERE network_id = $1 AND public_key = $2;`

	peer, err := scanPeer(s.db.Pool.QueryRow(ctx, query, networkID, publicKey))
	if err != nil {
		return nil, fmt.Errorf("get peer by public key: %w", mapNotFound(err))
	}
	return peer, nil
}

func (s *PeerStore) ListNetworkPeers(ctx context.Context, networkID string) ([]models.Peer, error) {
	query := `
SELECT ` + peerColumns + `
FROM peers
WHERE network_id IN (SELECT id FROM networks WHERE is_active = true AND (id::text = $1 OR network_id = $1))
ORDER BY created_at ASC;`

	rows, err := s.db.Pool.Query(ctx, query, networkID)
	if err != nil {
		return nil, fmt.Errorf("list network peers: %w", err)
	}
	defer rows.Close()

	peers := make([]models.Peer, 0)
	for rows.Next() {
		peer, err := scanPeer(rows)
		if err != nil {
			return nil, fmt.Errorf("list network peers: scan row: %w", err)
		}
		peers = append(peers, *peer)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list network peers: rows: %w", rows.Err())
	}
	return peers, nil
}

// ListMembers returns all peers (including pending) for a network. Used by dashboard member management.
func (s *PeerStore) ListMembers(ctx context.Context, networkID string) ([]models.Peer, error) {
	query := `
SELECT ` + peerColumns + `
FROM peers
WHERE network_id = $1
ORDER BY created_at ASC;`

	rows, err := s.db.Pool.Query(ctx, query, networkID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	members := make([]models.Peer, 0)
	for rows.Next() {
		peer, err := scanPeer(rows)
		if err != nil {
			return nil, fmt.Errorf("list members: scan row: %w", err)
		}
		members = append(members, *peer)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list members: rows: %w", rows.Err())
	}
	return members, nil
}

// ApprovePeer approves a pending peer and assigns the given virtual IP.
func (s *PeerStore) ApprovePeer(ctx context.Context, peerID, virtualIP string) (*models.Peer, error) {
	query := `
UPDATE peers
SET status = 'approved', virtual_ip = $2, is_online = false
WHERE id = $1
RETURNING ` + peerColumns + `;`

	peer, err := scanPeer(s.db.Pool.QueryRow(ctx, query, peerID, virtualIP))
	if err != nil {
		return nil, fmt.Errorf("approve peer: %w", mapNotFound(err))
	}
	return peer, nil
}

// RejectPeer sets a peer's status to 'rejected'.
func (s *PeerStore) RejectPeer(ctx context.Context, peerID string) error {
	const query = `UPDATE peers SET status = 'rejected' WHERE id = $1;`

	result, err := s.db.Pool.Exec(ctx, query, peerID)
	if err != nil {
		return fmt.Errorf("reject peer: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("reject peer: %w", ErrNotFound)
	}
	return nil
}

func (s *PeerStore) UpdatePeerStatus(ctx context.Context, peerID string, status PeerStatusUpdate) error {
	const query = `
UPDATE peers
SET public_endpoint = $2,
		local_endpoints = COALESCE($3::text[], '{}'::text[]),
    vnc_available = $4,
    rx_bytes = $5,
    tx_bytes = $6,
    relay_id = NULLIF($7, ''),
    is_online = true,
    last_seen = NOW()
WHERE id = $1;`

	_, err := s.db.Pool.Exec(
		ctx,
		query,
		peerID,
		status.PublicEndpoint,
		status.LocalEndpoints,
		status.VNCAvailable,
		status.RXBytes,
		status.TXBytes,
		status.RelayID,
	)
	if err != nil {
		return fmt.Errorf("update peer status: %w", err)
	}
	return nil
}

func (s *PeerStore) DeletePeer(ctx context.Context, peerID string) error {
	const query = `DELETE FROM peers WHERE id = $1;`

	result, err := s.db.Pool.Exec(ctx, query, peerID)
	if err != nil {
		return fmt.Errorf("delete peer: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete peer: %w", ErrNotFound)
	}
	return nil
}

func (s *PeerStore) DeletePeerByMachineID(ctx context.Context, networkID, machineID string) error {
	const query = `
DELETE FROM peers
WHERE machine_id = $2
  AND network_id IN (
		SELECT id
		FROM networks
		WHERE is_active = true
		  AND (id::text = $1 OR network_id = $1)
  );`

	result, err := s.db.Pool.Exec(ctx, query, networkID, machineID)
	if err != nil {
		return fmt.Errorf("delete peer by machine id: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete peer by machine id: %w", ErrNotFound)
	}
	return nil
}

func (s *PeerStore) GetOnlinePeers(ctx context.Context, networkID string) ([]models.Peer, error) {
	query := `
SELECT ` + peerColumns + `
FROM peers
WHERE network_id IN (SELECT id FROM networks WHERE is_active = true AND (id::text = $1 OR network_id = $1))
  AND is_online = true
  AND status = 'approved'
ORDER BY last_seen DESC;`

	rows, err := s.db.Pool.Query(ctx, query, networkID)
	if err != nil {
		return nil, fmt.Errorf("get online peers: %w", err)
	}
	defer rows.Close()

	peers := make([]models.Peer, 0)
	for rows.Next() {
		peer, err := scanPeer(rows)
		if err != nil {
			return nil, fmt.Errorf("get online peers: scan row: %w", err)
		}
		peers = append(peers, *peer)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("get online peers: rows: %w", rows.Err())
	}
	return peers, nil
}

type scanSource interface {
	Scan(dest ...any) error
}

func normalizeHostIPv4(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if ip := net.ParseIP(trimmed); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			return ipv4.String()
		}
		return trimmed
	}

	ip, _, err := net.ParseCIDR(trimmed)
	if err != nil || ip == nil {
		return trimmed
	}

	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4.String()
	}
	return trimmed
}

// scanPeer scans a full peer row matching peerColumns into a model.
func scanPeer(source scanSource) (*models.Peer, error) {
	var peer models.Peer

	var (
		id             sql.NullString
		networkID      sql.NullString
		name           sql.NullString
		machineID      sql.NullString
		publicKey      sql.NullString
		virtualIP      sql.NullString
		publicEndpoint sql.NullString
		osName         sql.NullString
		version        sql.NullString
		relayID        sql.NullString
		status         sql.NullString
		memberToken    sql.NullString

		isOnline     sql.NullBool
		vncAvailable sql.NullBool

		lastSeen      sql.NullTime
		lastHandshake sql.NullTime
		createdAt     sql.NullTime

		rxBytes sql.NullInt64
		txBytes sql.NullInt64
		vncPort sql.NullInt64
	)

	err := source.Scan(
		&id,
		&networkID,
		&name,
		&machineID,
		&publicKey,
		&virtualIP,
		&publicEndpoint,
		&peer.LocalEndpoints,
		&osName,
		&version,
		&isOnline,
		&lastSeen,
		&lastHandshake,
		&rxBytes,
		&txBytes,
		&vncPort,
		&vncAvailable,
		&relayID,
		&status,
		&memberToken,
		&createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan peer: %w", err)
	}

	if id.Valid {
		peer.ID = id.String
	}
	if networkID.Valid {
		peer.NetworkID = networkID.String
	}
	if name.Valid {
		peer.Name = name.String
	}
	if machineID.Valid {
		peer.MachineID = machineID.String
	}
	if publicKey.Valid {
		peer.PublicKey = publicKey.String
	}
	if virtualIP.Valid {
		peer.VirtualIP = normalizeHostIPv4(virtualIP.String)
	}

	if publicEndpoint.Valid {
		peer.PublicEndpoint = publicEndpoint.String
	}
	if osName.Valid {
		peer.OS = osName.String
	}
	if version.Valid {
		peer.Version = version.String
	}
	if isOnline.Valid {
		peer.IsOnline = isOnline.Bool
	}
	if rxBytes.Valid {
		peer.RXBytes = rxBytes.Int64
	}
	if txBytes.Valid {
		peer.TXBytes = txBytes.Int64
	}
	if vncAvailable.Valid {
		peer.VNCAvailable = vncAvailable.Bool
	}
	if relayID.Valid {
		peer.RelayID = relayID.String
	}
	if status.Valid {
		peer.Status = status.String
	}
	if memberToken.Valid {
		peer.MemberToken = memberToken.String
	}
	if lastSeen.Valid {
		peer.LastSeen = lastSeen.Time
	}
	if lastHandshake.Valid {
		peer.LastHandshake = lastHandshake.Time
	}
	if createdAt.Valid {
		peer.CreatedAt = createdAt.Time
	}

	peer.VNCPort = 5900
	if vncPort.Valid && vncPort.Int64 >= 1 && vncPort.Int64 <= 65535 {
		peer.VNCPort = int(vncPort.Int64)
	}
	if peer.LocalEndpoints == nil {
		peer.LocalEndpoints = []string{}
	}
	if peer.Status == "" {
		peer.Status = "approved"
	}

	return &peer, nil
}

// MarkPeerOffline is used by cleanup flows to clear online state.
func (s *PeerStore) MarkPeerOffline(ctx context.Context, peerID string) error {
	const query = `UPDATE peers SET is_online = false WHERE id = $1;`

	result, err := s.db.Pool.Exec(ctx, query, peerID)
	if err != nil {
		return fmt.Errorf("mark peer offline: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("mark peer offline: %w", ErrNotFound)
	}
	return nil
}

// ExpireStalePeers marks peers offline if heartbeat is stale.
func (s *PeerStore) ExpireStalePeers(ctx context.Context, staleAfter time.Duration) error {
	const query = `
UPDATE peers
SET is_online = false
WHERE is_online = true
  AND last_seen < NOW() - ($1::interval);`

	interval := fmt.Sprintf("%d seconds", int(staleAfter.Seconds()))
	if _, err := s.db.Pool.Exec(ctx, query, interval); err != nil {
		return fmt.Errorf("expire stale peers: %w", err)
	}
	return nil
}

// generateMemberToken creates a cryptographically random token for device auth.
func generateMemberToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate member token: %w", err)
	}
	return "mt_" + hex.EncodeToString(buf), nil
}
