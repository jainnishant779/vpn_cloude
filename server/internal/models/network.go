package models

import "time"

// Network groups peers that can connect through QuickTunnel.
type Network struct {
	ID          string    `json:"id" db:"id"`
	OwnerID     string    `json:"owner_id" db:"owner_id"`
	Name        string    `json:"name" db:"name"`
	NetworkID   string    `json:"network_id" db:"network_id"`
	CIDR        string    `json:"cidr" db:"cidr"`
	Description string    `json:"description" db:"description"`
	MaxPeers    int       `json:"max_peers" db:"max_peers"`
	IsActive      bool      `json:"is_active" db:"is_active"`
	AccessControl string    `json:"access_control" db:"access_control"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}
