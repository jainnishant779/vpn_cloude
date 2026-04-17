package queries

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound standardizes missing-row behavior for API handlers.
var ErrNotFound = errors.New("record not found")

func mapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
