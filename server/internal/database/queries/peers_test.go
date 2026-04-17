package queries

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubScanSource struct {
	scan func(dest ...any) error
}

func (s stubScanSource) Scan(dest ...any) error {
	return s.scan(dest...)
}

func TestScanPeer_AllowsNullableLegacyFields(t *testing.T) {
	createdAt := time.Date(2026, 4, 10, 8, 30, 0, 0, time.UTC)

	peer, err := scanPeer(stubScanSource{scan: func(dest ...any) error {
		*dest[0].(*sql.NullString) = sql.NullString{String: "peer-1", Valid: true}
		*dest[1].(*sql.NullString) = sql.NullString{String: "net-1", Valid: true}
		*dest[2].(*sql.NullString) = sql.NullString{Valid: false} // name
		*dest[3].(*sql.NullString) = sql.NullString{String: "machine-1", Valid: true}
		*dest[4].(*sql.NullString) = sql.NullString{String: "pubkey", Valid: true}
		*dest[5].(*sql.NullString) = sql.NullString{String: "10.0.0.2", Valid: true}
		*dest[6].(*sql.NullString) = sql.NullString{Valid: false} // public_endpoint
		*dest[7].(*[]string) = nil
		*dest[8].(*sql.NullString) = sql.NullString{Valid: false} // os
		*dest[9].(*sql.NullString) = sql.NullString{Valid: false} // version
		*dest[10].(*sql.NullBool) = sql.NullBool{Bool: true, Valid: true}
		*dest[11].(*sql.NullTime) = sql.NullTime{Valid: false} // last_seen
		*dest[12].(*sql.NullTime) = sql.NullTime{Valid: false} // last_handshake
		*dest[13].(*sql.NullInt64) = sql.NullInt64{Int64: 10, Valid: true}
		*dest[14].(*sql.NullInt64) = sql.NullInt64{Int64: 20, Valid: true}
		*dest[15].(*sql.NullInt64) = sql.NullInt64{Valid: false} // vnc_port
		*dest[16].(*sql.NullBool) = sql.NullBool{Bool: true, Valid: true}
		*dest[17].(*sql.NullString) = sql.NullString{Valid: false} // relay_id
		*dest[18].(*sql.NullTime) = sql.NullTime{Time: createdAt, Valid: true}
		return nil
	}})
	if err != nil {
		t.Fatalf("scanPeer returned error: %v", err)
	}

	if peer.Name != "" {
		t.Fatalf("expected empty name, got %q", peer.Name)
	}
	if peer.PublicEndpoint != "" {
		t.Fatalf("expected empty public endpoint, got %q", peer.PublicEndpoint)
	}
	if peer.OS != "" || peer.Version != "" {
		t.Fatalf("expected empty os/version, got os=%q version=%q", peer.OS, peer.Version)
	}
	if peer.RelayID != "" {
		t.Fatalf("expected empty relay id, got %q", peer.RelayID)
	}
	if !peer.LastSeen.IsZero() || !peer.LastHandshake.IsZero() {
		t.Fatalf("expected zero timestamps for null values")
	}
	if peer.VNCPort != 5900 {
		t.Fatalf("expected default vnc port 5900, got %d", peer.VNCPort)
	}
	if peer.LocalEndpoints == nil {
		t.Fatal("expected local_endpoints to be normalized to empty slice")
	}
	if !peer.CreatedAt.Equal(createdAt) {
		t.Fatalf("expected created_at=%v, got %v", createdAt, peer.CreatedAt)
	}
}

func TestScanPeer_UsesValidVNCPortWhenPresent(t *testing.T) {
	peer, err := scanPeer(stubScanSource{scan: func(dest ...any) error {
		*dest[0].(*sql.NullString) = sql.NullString{String: "peer-2", Valid: true}
		*dest[1].(*sql.NullString) = sql.NullString{String: "net-2", Valid: true}
		*dest[2].(*sql.NullString) = sql.NullString{String: "node", Valid: true}
		*dest[3].(*sql.NullString) = sql.NullString{String: "machine-2", Valid: true}
		*dest[4].(*sql.NullString) = sql.NullString{String: "pubkey", Valid: true}
		*dest[5].(*sql.NullString) = sql.NullString{String: "10.0.0.3", Valid: true}
		*dest[6].(*sql.NullString) = sql.NullString{String: "198.51.100.1:51820", Valid: true}
		*dest[7].(*[]string) = []string{"192.168.1.10:51820"}
		*dest[8].(*sql.NullString) = sql.NullString{String: "windows", Valid: true}
		*dest[9].(*sql.NullString) = sql.NullString{String: "1.0.0", Valid: true}
		*dest[10].(*sql.NullBool) = sql.NullBool{Bool: true, Valid: true}
		*dest[11].(*sql.NullTime) = sql.NullTime{Time: time.Now().UTC(), Valid: true}
		*dest[12].(*sql.NullTime) = sql.NullTime{Time: time.Now().UTC(), Valid: true}
		*dest[13].(*sql.NullInt64) = sql.NullInt64{Int64: 100, Valid: true}
		*dest[14].(*sql.NullInt64) = sql.NullInt64{Int64: 200, Valid: true}
		*dest[15].(*sql.NullInt64) = sql.NullInt64{Int64: 5999, Valid: true}
		*dest[16].(*sql.NullBool) = sql.NullBool{Bool: true, Valid: true}
		*dest[17].(*sql.NullString) = sql.NullString{String: "relay-a", Valid: true}
		*dest[18].(*sql.NullTime) = sql.NullTime{Time: time.Now().UTC(), Valid: true}
		return nil
	}})
	if err != nil {
		t.Fatalf("scanPeer returned error: %v", err)
	}

	if peer.VNCPort != 5999 {
		t.Fatalf("expected vnc port 5999, got %d", peer.VNCPort)
	}
}

func TestScanPeer_WrapsScanError(t *testing.T) {
	_, err := scanPeer(stubScanSource{scan: func(dest ...any) error {
		return errors.New("boom")
	}})
	if err == nil {
		t.Fatal("expected scanPeer to return error")
	}
	if !strings.Contains(err.Error(), "scan peer: boom") {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestScanPeer_NormalizesCIDRVirtualIP(t *testing.T) {
	peer, err := scanPeer(stubScanSource{scan: func(dest ...any) error {
		*dest[0].(*sql.NullString) = sql.NullString{String: "peer-3", Valid: true}
		*dest[1].(*sql.NullString) = sql.NullString{String: "net-3", Valid: true}
		*dest[2].(*sql.NullString) = sql.NullString{String: "node-3", Valid: true}
		*dest[3].(*sql.NullString) = sql.NullString{String: "machine-3", Valid: true}
		*dest[4].(*sql.NullString) = sql.NullString{String: "pubkey", Valid: true}
		*dest[5].(*sql.NullString) = sql.NullString{String: "10.7.0.2/32", Valid: true}
		*dest[6].(*sql.NullString) = sql.NullString{Valid: false}
		*dest[7].(*[]string) = []string{}
		*dest[8].(*sql.NullString) = sql.NullString{String: "linux", Valid: true}
		*dest[9].(*sql.NullString) = sql.NullString{String: "1.0.0", Valid: true}
		*dest[10].(*sql.NullBool) = sql.NullBool{Bool: true, Valid: true}
		*dest[11].(*sql.NullTime) = sql.NullTime{Valid: false}
		*dest[12].(*sql.NullTime) = sql.NullTime{Valid: false}
		*dest[13].(*sql.NullInt64) = sql.NullInt64{Int64: 0, Valid: true}
		*dest[14].(*sql.NullInt64) = sql.NullInt64{Int64: 0, Valid: true}
		*dest[15].(*sql.NullInt64) = sql.NullInt64{Int64: 5900, Valid: true}
		*dest[16].(*sql.NullBool) = sql.NullBool{Bool: true, Valid: true}
		*dest[17].(*sql.NullString) = sql.NullString{Valid: false}
		*dest[18].(*sql.NullTime) = sql.NullTime{Valid: false}
		return nil
	}})
	if err != nil {
		t.Fatalf("scanPeer returned error: %v", err)
	}

	if peer.VirtualIP != "10.7.0.2" {
		t.Fatalf("expected normalized virtual ip 10.7.0.2, got %q", peer.VirtualIP)
	}
}
