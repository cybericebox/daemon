// Package userRepo is the repository for the User aggregate: it accepts and
// returns whole domain entities (userModel.User) and keeps all pgtype/sqlc
// mapping out of the business layer.
//
// Narrow queries deliberately NOT wrapped here:
//   - UpdateUserLastSeen — async hot path (protection.touchAsync); it is also
//     excluded from the UpdateUser column set so a full-row write cannot race
//     it (lost update); MarkUserInvitationSent likewise writes only
//     invitation_sent_at, the retention clock of unconfirmed accounts;
//   - reads for lists/stats (ListUsersCursor, Count*, RegistrationsByDay...) —
//     query-side shapes, not aggregate loads;
//   - DeleteUserSessions / DeleteUserProviders — set operations on other tables.
package userRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// Queries is the narrow slice of the sqlc Querier this repository needs. It is
// satisfied by both the pool-backed Querier and the transaction-scoped
// repository handed out by the unit of work, so flows can run Update inside a
// UoW: userRepo.New(txRepo).
type Queries interface {
	CreateUser(ctx context.Context, arg postgres.CreateUserParams) (postgres.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (postgres.User, error)
	GetUserByEmail(ctx context.Context, email string) (postgres.User, error)
	UpdateUser(ctx context.Context, arg postgres.UpdateUserParams) (int64, error)
	// provider links are part of the User aggregate's identity
	GetUserByProvider(ctx context.Context, arg postgres.GetUserByProviderParams) (postgres.User, error)
	CreateUserProvider(ctx context.Context, arg postgres.CreateUserProviderParams) (postgres.UserProvider, error)
	GetUserProviders(ctx context.Context, userID uuid.UUID) ([]postgres.UserProvider, error)
	DeleteUserProvider(ctx context.Context, arg postgres.DeleteUserProviderParams) (int64, error)
	CountUserLoginMethods(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteUserProviders(ctx context.Context, userID uuid.UUID) (int64, error)
	// list / stat query shapes + async last-seen touch
	ListUsersCursor(ctx context.Context, arg postgres.ListUsersCursorParams) ([]postgres.User, error)
	ListUsersPage(ctx context.Context, arg postgres.ListUsersPageParams) ([]postgres.User, error)
	CountUsers(ctx context.Context, arg postgres.CountUsersParams) (int64, error)
	CountUsersByRole(ctx context.Context, role string) (int64, error)
	CountUsersByRoleAll(ctx context.Context) ([]postgres.CountUsersByRoleAllRow, error)
	CountUsersByStatus(ctx context.Context, status string) (int64, error)
	CountUsersCreatedSince(ctx context.Context, createdAt time.Time) (int64, error)
	CountUsersActiveSince(ctx context.Context, lastSeen time.Time) (int64, error)
	AvgDailyActiveSince(ctx context.Context, createdAt time.Time) (float64, error)
	RegistrationsByDaySince(ctx context.Context, createdAt time.Time) ([]postgres.RegistrationsByDaySinceRow, error)
	UpdateUserLastSeen(ctx context.Context, id uuid.UUID) (int64, error)
	MarkUserInvitationSent(ctx context.Context, arg postgres.MarkUserInvitationSentParams) (int64, error)
}

// ListParams is the keyset-page + role-filter query for the admin user list.
type ListParams struct {
	Search          string
	Roles           []string
	Status          string
	CursorCreatedAt time.Time
	CursorID        uuid.UUID
	Limit           int32
}

type PageParams struct {
	Search  string
	Roles   []string
	Status  string
	SortBy  string
	SortDir string
	Limit   int32
	Offset  int32
}

// RoleCount / DayCount are domain-typed stat rows for the admin dashboard.
type (
	RoleCount struct {
		Role  rbac.Role
		Count int64
	}
	DayCount struct {
		Day   time.Time
		Count int64
	}
)

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ── list / stats (admin dashboard) ──

// ListCursor returns a keyset page of users as domain entities.
func (r *Repository) ListCursor(ctx context.Context, p ListParams) ([]userModel.User, error) {
	rows, err := r.q.ListUsersCursor(ctx, postgres.ListUsersCursorParams{
		Search: p.Search, Roles: p.Roles, Status: p.Status, CursorCreatedAt: p.CursorCreatedAt, CursorID: p.CursorID, LimitVal: p.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]userModel.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

func (r *Repository) ListPage(ctx context.Context, p PageParams) ([]userModel.User, error) {
	rows, err := r.q.ListUsersPage(ctx, postgres.ListUsersPageParams{
		Search: p.Search, Roles: p.Roles, Status: p.Status, SortBy: p.SortBy,
		SortDir: p.SortDir, LimitVal: p.Limit, OffsetVal: p.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]userModel.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

// Count returns the number of users matching the search/roles filter.
func (r *Repository) Count(ctx context.Context, search string, roles []string, status string) (int64, error) {
	return r.q.CountUsers(ctx, postgres.CountUsersParams{Search: search, Roles: roles, Status: status})
}

// CountByRole returns the number of users with a role.
func (r *Repository) CountByRole(ctx context.Context, role string) (int64, error) {
	return r.q.CountUsersByRole(ctx, role)
}

// CountByRoleAll returns per-role user counts.
func (r *Repository) CountByRoleAll(ctx context.Context) ([]RoleCount, error) {
	rows, err := r.q.CountUsersByRoleAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RoleCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, RoleCount{Role: rbac.Role(row.Role), Count: row.Count})
	}
	return out, nil
}

// CountByStatus returns the number of users with a status.
func (r *Repository) CountByStatus(ctx context.Context, status string) (int64, error) {
	return r.q.CountUsersByStatus(ctx, status)
}

// CountCreatedSince returns the number of users created since t.
func (r *Repository) CountCreatedSince(ctx context.Context, t time.Time) (int64, error) {
	return r.q.CountUsersCreatedSince(ctx, t)
}

// CountActiveSince returns the number of users last-seen since t.
func (r *Repository) CountActiveSince(ctx context.Context, t time.Time) (int64, error) {
	return r.q.CountUsersActiveSince(ctx, t)
}

// AvgDailyActiveSince returns the average daily active users since t.
func (r *Repository) AvgDailyActiveSince(ctx context.Context, t time.Time) (float64, error) {
	return r.q.AvgDailyActiveSince(ctx, t)
}

// RegistrationsByDaySince returns per-day registration counts since t.
func (r *Repository) RegistrationsByDaySince(ctx context.Context, t time.Time) ([]DayCount, error) {
	rows, err := r.q.RegistrationsByDaySince(ctx, t)
	if err != nil {
		return nil, err
	}
	out := make([]DayCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, DayCount{Day: row.Day, Count: row.Count})
	}
	return out, nil
}

// MarkInvitationSent records when an invitation email was queued for a still
// unconfirmed account (event or platform invitation); the 30-day retention
// clock of unconfirmed accounts restarts from it.
func (r *Repository) MarkInvitationSent(ctx context.Context, id uuid.UUID, at time.Time) (int64, error) {
	return r.q.MarkUserInvitationSent(ctx, postgres.MarkUserInvitationSentParams{ID: id, SentAt: at})
}

// TouchLastSeen bumps the user's last_seen (async hot path). Excluded from the
// whole-row UpdateUser column set so a full write cannot race it.
func (r *Repository) TouchLastSeen(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.UpdateUserLastSeen(ctx, id)
}

// ── provider links (part of the User aggregate identity) ──

// GetByProvider loads the user owning a provider identity.
func (r *Repository) GetByProvider(ctx context.Context, provider, providerUserID string) (userModel.User, error) {
	row, err := r.q.GetUserByProvider(ctx, postgres.GetUserByProviderParams{Provider: provider, ProviderUserID: providerUserID})
	if err != nil {
		return userModel.User{}, err
	}
	return ToDomain(row), nil
}

// LinkProvider adds a provider identity to a user. Returns the raw error so the
// caller can classify a unique-violation (identity already linked).
func (r *Repository) LinkProvider(ctx context.Context, id, userID uuid.UUID, provider, providerUserID string) error {
	_, err := r.q.CreateUserProvider(ctx, postgres.CreateUserProviderParams{
		ID: id, UserID: userID, Provider: provider, ProviderUserID: providerUserID,
	})
	return err
}

// UnlinkProvider removes a provider identity from a user.
func (r *Repository) UnlinkProvider(ctx context.Context, userID uuid.UUID, provider string) (int64, error) {
	return r.q.DeleteUserProvider(ctx, postgres.DeleteUserProviderParams{UserID: userID, Provider: provider})
}

// ListProviders returns the provider names linked to a user.
func (r *Repository) ListProviders(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := r.q.GetUserProviders(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, p := range rows {
		out = append(out, p.Provider)
	}
	return out, nil
}

// CountLoginMethods reports how many login methods (provider links) a user has.
func (r *Repository) CountLoginMethods(ctx context.Context, userID uuid.UUID) (int64, error) {
	return r.q.CountUserLoginMethods(ctx, userID)
}

// DeleteProviders removes every provider link of a user.
func (r *Repository) DeleteProviders(ctx context.Context, userID uuid.UUID) (int64, error) {
	return r.q.DeleteUserProviders(ctx, userID)
}

// Create persists a whole domain user; every column value including
// timestamps comes from the entity (domain-owned defaults).
func (r *Repository) Create(ctx context.Context, u userModel.User) (userModel.User, error) {
	row, err := r.q.CreateUser(ctx, postgres.CreateUserParams{
		ID:             u.ID,
		Email:          userModel.NormalizeEmail(u.Email),
		FirstName:      u.FirstName,
		LastName:       u.LastName,
		HashedPassword: textFromString(u.HashedPassword),
		Picture:        u.Picture,
		Role:           string(u.Role),
		Status:         string(u.Status),
		EmailConfirmed: u.EmailConfirmed,
		LastSeen:       u.LastSeen,
		UpdatedAt:      timestamptzFromTime(u.UpdatedAt),
		CreatedAt:      u.CreatedAt,
	})
	if err != nil {
		return userModel.User{}, err
	}
	return ToDomain(row), nil
}

// GetByID loads one (non-deleted) user. Not found propagates the raw repo
// error for the caller to classify (repositoryTools.IsObjectNotFoundError).
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (userModel.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if err != nil {
		return userModel.User{}, err
	}
	return ToDomain(row), nil
}

// GetByEmail loads one (non-deleted) user by address.
func (r *Repository) GetByEmail(ctx context.Context, email string) (userModel.User, error) {
	row, err := r.q.GetUserByEmail(ctx, userModel.NormalizeEmail(email))
	if err != nil {
		return userModel.User{}, err
	}
	return ToDomain(row), nil
}

// Update writes the whole aggregate in one statement (last_seen and
// created_at excluded — see the package comment), guarded by an optimistic
// lock: expectedUpdatedAt is the UpdatedAt the caller LOADED (before the
// domain mutation touched it). Zero rows affected means the row vanished or
// was modified concurrently — the caller re-reads to tell which.
func (r *Repository) Update(ctx context.Context, u userModel.User, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.UpdateUser(ctx, postgres.UpdateUserParams{
		ExpectedUpdatedAt: timestamptzFromTime(expectedUpdatedAt),
		ID:                u.ID,
		Email:             userModel.NormalizeEmail(u.Email),
		FirstName:         u.FirstName,
		LastName:          u.LastName,
		HashedPassword:    textFromString(u.HashedPassword),
		Picture:           u.Picture,
		Role:              string(u.Role),
		Status:            string(u.Status),
		EmailConfirmed:    u.EmailConfirmed,
		TosAcceptedAt:     timestamptzFromPtr(u.TosAcceptedAt),
		TosVersion:        int4FromInt32(u.TosVersion),
		UpdatedAt:         timestamptzFromTime(u.UpdatedAt),
		UpdatedBy:         u.UpdatedBy,
		DeletedAt:         timestamptzFromPtr(u.DeletedAt),
	})
}

// ToDomain maps a sqlc row to the domain entity. Exported so flows that still
// read rows via narrow queries can lift them into the domain.
func ToDomain(row postgres.User) userModel.User {
	return userModel.User{
		ID:             row.ID,
		Email:          row.Email,
		FirstName:      row.FirstName,
		LastName:       row.LastName,
		HashedPassword: row.HashedPassword.String,
		Picture:        row.Picture,
		Role:           rbac.Role(row.Role),
		Status:         userModel.UserStatus(row.Status),
		EmailConfirmed: row.EmailConfirmed,
		TosAcceptedAt:  ptrFromTimestamptz(row.TosAcceptedAt),
		TosVersion:     row.TosVersion.Int32,
		LastSeen:       row.LastSeen,
		UpdatedAt:      row.UpdatedAt.Time,
		UpdatedBy:      row.UpdatedBy,
		DeletedAt:      ptrFromTimestamptz(row.DeletedAt),
		CreatedAt:      row.CreatedAt,
	}
}

// ── pgtype mapping helpers (the only place they exist for users) ────────────

func textFromString(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func timestamptzFromTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func timestamptzFromPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func ptrFromTimestamptz(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	return new(ts.Time)
}

func int4FromInt32(v int32) pgtype.Int4 {
	if v == 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: v, Valid: true}
}
