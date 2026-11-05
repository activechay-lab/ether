package capability

import (
	"database/sql"

	"errors"

	_ "github.com/braedonsaunders/ether/internal/handles/reservations"
)

func NewTickets(db *sql.DB) (*Tickets, error) {
	if db == nil {
		return nil, errors.New("capability: database required")
	}
	return &Tickets{db: db}, nil
}
