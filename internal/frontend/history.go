package frontend

import "context"

// HistoryTopStore aggregates persisted viewing events into per-title
// counts. Adapters apply their own authorization before exposing them.
type HistoryTopStore interface {
	HistoryTop(context.Context, HistoryQuery, KeyKind) ([]HistoryCount, error)
	LifetimeUsers(context.Context, RatingKey, HistoryQuery) ([]HistoryCount, error)
}

// HistoryStore reads and deletes persisted viewing events.
type HistoryStore interface {
	QueryHistory(context.Context, HistoryQuery) ([]HistoryEntry, error)
	HistoryByID(context.Context, int64, int64) (HistoryEntry, error)
	DeleteHistory(context.Context, int64, int64) error
}
