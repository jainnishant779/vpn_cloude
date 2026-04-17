package queries

import (
	"context"
	"fmt"

	"quicktunnel/server/internal/database"
	"quicktunnel/server/internal/models"
)

// NetworkStore handles persistence for virtual network records.
type NetworkStore struct {
	db *database.DB
}

func NewNetworkStore(db *database.DB) *NetworkStore {
	return &NetworkStore{db: db}
}

func (s *NetworkStore) CreateNetwork(ctx context.Context, network *models.Network) (*models.Network, error) {
	if network == nil {
		return nil, fmt.Errorf("create network: network is nil")
	}

	const query = `
INSERT INTO networks (owner_id, name, network_id, cidr, description, max_peers, is_active, access_control)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, owner_id, name, network_id, cidr, description, max_peers, is_active, access_control, created_at;`

	out := &models.Network{}
	if err := s.db.Pool.QueryRow(
		ctx,
		query,
		network.OwnerID,
		network.Name,
		network.NetworkID,
		network.CIDR,
		network.Description,
		network.MaxPeers,
		network.IsActive,
		network.AccessControl,
	).Scan(
		&out.ID,
		&out.OwnerID,
		&out.Name,
		&out.NetworkID,
		&out.CIDR,
		&out.Description,
		&out.MaxPeers,
		&out.IsActive,
		&out.AccessControl,
		&out.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("create network: %w", err)
	}

	return out, nil
}

// scanNetwork scans a full network row into a model, used by all query methods.
func scanNetwork(scanner interface {
	Scan(dest ...any) error
}, out *models.Network) error {
	return scanner.Scan(
		&out.ID,
		&out.OwnerID,
		&out.Name,
		&out.NetworkID,
		&out.CIDR,
		&out.Description,
		&out.MaxPeers,
		&out.IsActive,
		&out.AccessControl,
		&out.CreatedAt,
	)
}

const networkColumns = `id, owner_id, name, network_id, cidr, description, max_peers, is_active, access_control, created_at`

func (s *NetworkStore) GetNetwork(ctx context.Context, networkID string) (*models.Network, error) {
	query := `
SELECT ` + networkColumns + `
FROM networks
WHERE is_active = true
  AND (id::text = $1 OR network_id = $1)
LIMIT 1;`

	out := &models.Network{}
	if err := scanNetwork(s.db.Pool.QueryRow(ctx, query, networkID), out); err != nil {
		return nil, fmt.Errorf("get network: %w", mapNotFound(err))
	}

	return out, nil
}

// GetNetworkByPublicID looks up a network by its short public network_id.
// This is used by the unauthenticated join endpoint.
func (s *NetworkStore) GetNetworkByPublicID(ctx context.Context, publicNetworkID string) (*models.Network, error) {
	query := `
SELECT ` + networkColumns + `
FROM networks
WHERE is_active = true AND network_id = $1
LIMIT 1;`

	out := &models.Network{}
	if err := scanNetwork(s.db.Pool.QueryRow(ctx, query, publicNetworkID), out); err != nil {
		return nil, fmt.Errorf("get network by public id: %w", mapNotFound(err))
	}

	return out, nil
}

func (s *NetworkStore) ListUserNetworks(ctx context.Context, userID string) ([]models.Network, error) {
	query := `
SELECT ` + networkColumns + `
FROM networks
WHERE owner_id = $1 AND is_active = true
ORDER BY created_at DESC;`

	rows, err := s.db.Pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list user networks: %w", err)
	}
	defer rows.Close()

	items := make([]models.Network, 0)
	for rows.Next() {
		var n models.Network
		if err := scanNetwork(rows, &n); err != nil {
			return nil, fmt.Errorf("list user networks: scan row: %w", err)
		}
		items = append(items, n)
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("list user networks: rows: %w", rows.Err())
	}

	return items, nil
}

func (s *NetworkStore) DeleteNetwork(ctx context.Context, networkID string) error {
	const query = `UPDATE networks SET is_active = false WHERE id = $1;`

	result, err := s.db.Pool.Exec(ctx, query, networkID)
	if err != nil {
		return fmt.Errorf("delete network: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete network: %w", ErrNotFound)
	}
	return nil
}

func (s *NetworkStore) UpdateNetwork(ctx context.Context, networkID, name, description string) (*models.Network, error) {
	query := `
UPDATE networks
SET name = $2, description = $3
WHERE id = $1 AND is_active = true
RETURNING ` + networkColumns + `;`

	out := &models.Network{}
	if err := scanNetwork(s.db.Pool.QueryRow(ctx, query, networkID, name, description), out); err != nil {
		return nil, fmt.Errorf("update network: %w", mapNotFound(err))
	}

	return out, nil
}

// UpdateNetworkAccessControl updates the access_control field.
func (s *NetworkStore) UpdateNetworkAccessControl(ctx context.Context, networkID, accessControl string) (*models.Network, error) {
	query := `
UPDATE networks
SET access_control = $2
WHERE id = $1 AND is_active = true
RETURNING ` + networkColumns + `;`

	out := &models.Network{}
	if err := scanNetwork(s.db.Pool.QueryRow(ctx, query, networkID, accessControl), out); err != nil {
		return nil, fmt.Errorf("update network access control: %w", mapNotFound(err))
	}

	return out, nil
}
