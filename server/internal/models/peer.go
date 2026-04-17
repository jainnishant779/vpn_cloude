package models

import "time"

// Peer represents one machine participating in a virtual network.
type Peer struct {
	ID             string    `json:"id" db:"id"`
	NetworkID      string    `json:"network_id" db:"network_id"`
	Name           string    `json:"name" db:"name"`
	MachineID      string    `json:"machine_id" db:"machine_id"`
	PublicKey      string    `json:"public_key" db:"public_key"`
	VirtualIP      string    `json:"virtual_ip" db:"virtual_ip"`
	PublicEndpoint string    `json:"public_endpoint" db:"public_endpoint"`
	LocalEndpoints []string  `json:"local_endpoints" db:"local_endpoints"`
	OS             string    `json:"os" db:"os"`
	Version        string    `json:"version" db:"version"`
	IsOnline       bool      `json:"is_online" db:"is_online"`
	LastSeen       time.Time `json:"last_seen" db:"last_seen"`
	LastHandshake  time.Time `json:"last_handshake" db:"last_handshake"`
	RXBytes        int64     `json:"rx_bytes" db:"rx_bytes"`
	TXBytes        int64     `json:"tx_bytes" db:"tx_bytes"`
	VNCPort        int       `json:"vnc_port" db:"vnc_port"`
	VNCAvailable   bool      `json:"vnc_available" db:"vnc_available"`
	RelayID        string    `json:"relay_id" db:"relay_id"`
	Status         string    `json:"status" db:"status"`
	MemberToken    string    `json:"member_token,omitempty" db:"member_token"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}
