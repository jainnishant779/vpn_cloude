package queries

import (
	"context"
	"fmt"

	"quicktunnel/server/internal/database"
	"quicktunnel/server/internal/models"
)

// RelayStore handles persistence for relay inventory and load.
type RelayStore struct {
	db *database.DB
}

func NewRelayStore(db *database.DB) *RelayStore {
	return &RelayStore{db: db}
}

func (s *RelayStore) ListRelays(ctx context.Context) ([]models.RelayServer, error) {
	const query = `
SELECT id, name, region, hostname, ip::text, port, is_healthy, current_load, max_load,
       COALESCE(latitude, 0), COALESCE(longitude, 0), created_at
FROM relay_servers
ORDER BY region ASC, current_load ASC;`

	rows, err := s.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list relays: %w", err)
	}
	defer rows.Close()

	relays := make([]models.RelayServer, 0)
	for rows.Next() {
		var relay models.RelayServer
		if err := rows.Scan(
			&relay.ID,
			&relay.Name,
			&relay.Region,
			&relay.Hostname,
			&relay.IP,
			&relay.Port,
			&relay.IsHealthy,
			&relay.CurrentLoad,
			&relay.MaxLoad,
			&relay.Latitude,
			&relay.Longitude,
			&relay.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("list relays: scan row: %w", err)
		}
		relays = append(relays, relay)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list relays: rows: %w", rows.Err())
	}

	return relays, nil
}

func (s *RelayStore) GetNearestRelay(ctx context.Context, latitude, longitude float64) (*models.RelayServer, error) {
	const query = `
SELECT id, name, region, hostname, ip::text, port, is_healthy, current_load, max_load,
       COALESCE(latitude, 0), COALESCE(longitude, 0), created_at
FROM relay_servers
WHERE is_healthy = true
  AND current_load < max_load
ORDER BY
  ((COALESCE(latitude, 0) - $1) * (COALESCE(latitude, 0) - $1)) +
  ((COALESCE(longitude, 0) - $2) * (COALESCE(longitude, 0) - $2)) ASC,
  current_load ASC
LIMIT 1;`

	relay := &models.RelayServer{}
	err := s.db.Pool.QueryRow(ctx, query, latitude, longitude).Scan(
		&relay.ID,
		&relay.Name,
		&relay.Region,
		&relay.Hostname,
		&relay.IP,
		&relay.Port,
		&relay.IsHealthy,
		&relay.CurrentLoad,
		&relay.MaxLoad,
		&relay.Latitude,
		&relay.Longitude,
		&relay.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get nearest relay: %w", mapNotFound(err))
	}

	return relay, nil
}

func (s *RelayStore) UpdateRelayLoad(ctx context.Context, relayID string, currentLoad int) error {
	const query = `UPDATE relay_servers SET current_load = $2 WHERE id = $1;`

	result, err := s.db.Pool.Exec(ctx, query, relayID, currentLoad)
	if err != nil {
		return fmt.Errorf("update relay load: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("update relay load: %w", ErrNotFound)
	}

	return nil
}
