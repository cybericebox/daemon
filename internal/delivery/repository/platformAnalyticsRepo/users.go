package platformAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// UsersQueries are the sqlc statements of «Користувачі»: the existing admin
// user stats (roles, blocked), the shared new / active account counts of the
// overview, and the period-bound aggregates.
type UsersQueries interface {
	CountUsersByRoleAll(context.Context) ([]postgres.CountUsersByRoleAllRow, error)
	CountUsersByStatus(context.Context, string) (int64, error)
	GetPlatformOverviewUsers(context.Context, postgres.GetPlatformOverviewUsersParams) (postgres.GetPlatformOverviewUsersRow, error)
	GetPlatformActiveUsers(context.Context, postgres.GetPlatformActiveUsersParams) (postgres.GetPlatformActiveUsersRow, error)
	ListPlatformOverviewDailyUsers(context.Context, postgres.ListPlatformOverviewDailyUsersParams) ([]postgres.ListPlatformOverviewDailyUsersRow, error)
	ListPlatformUsersDailyActive(context.Context, postgres.ListPlatformUsersDailyActiveParams) ([]postgres.ListPlatformUsersDailyActiveRow, error)
	GetPlatformUsersMethods(context.Context, postgres.GetPlatformUsersMethodsParams) (postgres.GetPlatformUsersMethodsRow, error)
	GetPlatformUsersRetention(context.Context, time.Time) (postgres.GetPlatformUsersRetentionRow, error)
	ListPlatformUsersPeople(context.Context, int32) ([]postgres.ListPlatformUsersPeopleRow, error)
}

type (
	// UsersRoleCount is the accounts holding one role.
	UsersRoleCount struct {
		Role  string
		Count int64
	}

	// UsersActiveDay is the daily and weekly active accounts of one UTC day.
	UsersActiveDay struct {
		Day      time.Time
		DAU, WAU int64
	}

	// UsersMethods are exclusive sign-in method groups: all current accounts
	// and those registered in the window (*New).
	UsersMethods struct {
		PasswordOnly, ProviderOnly, Both, None             int64
		PasswordOnlyNew, ProviderOnlyNew, BothNew, NoneNew int64
	}

	// UsersRetention is how many accounts joined 1, 2, 3+ events, and none.
	UsersRetention struct{ One, Two, ThreePlus, Never int64 }

	// UsersPerson is one row of the most active accounts (per-user data).
	UsersPerson struct {
		ID           uuid.UUID
		FirstName    string
		LastName     string
		Email        string
		Role         string
		EventsJoined int64
		Solves       int64
		LastSeenAt   time.Time
	}
)

func (r *Repository) UsersByRole(ctx context.Context) ([]UsersRoleCount, error) {
	rows, err := r.q.CountUsersByRoleAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UsersRoleCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsersRoleCount{Role: row.Role, Count: row.Count})
	}
	return out, nil
}

// UsersBlocked counts the blocked accounts.
func (r *Repository) UsersBlocked(ctx context.Context, status string) (int64, error) {
	return r.q.CountUsersByStatus(ctx, status)
}

// UsersActiveDays lists DAU and WAU per UTC day over [from, to), zero-filled.
func (r *Repository) UsersActiveDays(ctx context.Context, from, to time.Time) ([]UsersActiveDay, error) {
	rows, err := r.q.ListPlatformUsersDailyActive(ctx, postgres.ListPlatformUsersDailyActiveParams{FromAt: from, ToAt: to})
	if err != nil {
		return nil, err
	}
	out := make([]UsersActiveDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsersActiveDay{Day: row.Day, DAU: row.Dau, WAU: row.Wau})
	}
	return out, nil
}

func (r *Repository) UsersMethods(ctx context.Context, from, to time.Time) (UsersMethods, error) {
	row, err := r.q.GetPlatformUsersMethods(ctx, postgres.GetPlatformUsersMethodsParams{FromAt: from, ToAt: to})
	return UsersMethods{
		PasswordOnly: row.PasswordOnly, ProviderOnly: row.ProviderOnly, Both: row.Both, None: row.None,
		PasswordOnlyNew: row.PasswordOnlyNew, ProviderOnlyNew: row.ProviderOnlyNew, BothNew: row.BothNew, NoneNew: row.NoneNew,
	}, err
}

func (r *Repository) UsersRetention(ctx context.Context, to time.Time) (UsersRetention, error) {
	row, err := r.q.GetPlatformUsersRetention(ctx, to)
	return UsersRetention{One: row.OneEvent, Two: row.TwoEvents, ThreePlus: row.ThreePlusEvents, Never: row.Never}, err
}

// UsersPeople reads the most active accounts, at most limit rows.
func (r *Repository) UsersPeople(ctx context.Context, limit int32) ([]UsersPerson, error) {
	rows, err := r.q.ListPlatformUsersPeople(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]UsersPerson, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsersPerson{
			ID: row.ID, FirstName: row.FirstName, LastName: row.LastName, Email: row.Email, Role: row.Role,
			EventsJoined: row.EventsJoined, Solves: row.Solves, LastSeenAt: row.LastSeen,
		})
	}
	return out, nil
}
