package models

import "time"

// RelayServer describes a relay node available for fallback transport.
type RelayServer struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Region      string    `json:"region" db:"region"`
	Hostname    string    `json:"hostname" db:"hostname"`
	IP          string    `json:"ip" db:"ip"`
	Port        int       `json:"port" db:"port"`
	IsHealthy   bool      `json:"is_healthy" db:"is_healthy"`
	CurrentLoad int       `json:"current_load" db:"current_load"`
	MaxLoad     int       `json:"max_load" db:"max_load"`
	Latitude    float64   `json:"latitude" db:"latitude"`
	Longitude   float64   `json:"longitude" db:"longitude"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}
