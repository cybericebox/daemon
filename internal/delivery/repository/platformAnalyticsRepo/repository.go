// Package platformAnalyticsRepo holds the query-side statements of
// platform-level analytics: read-only aggregates across the whole platform,
// returned as plain Go shapes (no pgtype outside the repo). Nothing here is
// an aggregate.
package platformAnalyticsRepo

// Queries is the union of the per-section sqlc query sets (one file each).
type Queries interface {
	OverviewQueries
	UsersQueries
	EventsQueries
	TasksQueries
	InfrastructureQueries
	InfrastructureKindQueries
	MailQueries
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }
