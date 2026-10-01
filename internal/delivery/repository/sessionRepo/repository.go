// Package sessionRepo is the repository for the Session aggregate: it accepts
// and returns whole domain entities (authModel.Session) and keeps all
// row-mapping concerns — including SessionMetadata JSON (de)serialization —
// out of the business layer.
//
// Narrow queries deliberately NOT wrapped here:
//   - TouchSession / UpdateUserLastSeen — async hot path (protection.touchAsync);
//     a full-row update there would race concurrent mutations (lost update);
//   - DeleteSession / DeleteUserSession(s)(Except) — set operations, not
//     aggregate mutations.
package sessionRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateSession(ctx context.Context, arg postgres.CreateSessionParams) (postgres.Session, error)
	GetSessionByID(ctx context.Context, id uuid.UUID) (postgres.Session, error)
	GetSessionsByUser(ctx context.Context, userID uuid.UUID) ([]postgres.Session, error)
	TouchSession(ctx context.Context, arg postgres.TouchSessionParams) (int64, error)
	DeleteSession(ctx context.Context, id uuid.UUID) (int64, error)
	DeleteUserSession(ctx context.Context, arg postgres.DeleteUserSessionParams) (int64, error)
	DeleteUserSessionsExcept(ctx context.Context, arg postgres.DeleteUserSessionsExceptParams) (int64, error)
	DeleteUserSessions(ctx context.Context, userID uuid.UUID) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// Touch extends a session's idle expiry (async hot path).
func (r *Repository) Touch(ctx context.Context, id uuid.UUID, expiresAt time.Time) (int64, error) {
	return r.q.TouchSession(ctx, postgres.TouchSessionParams{ID: id, ExpiresAt: expiresAt})
}

// Delete removes a session by id.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.DeleteSession(ctx, id)
}

// DeleteForUser revokes one of a user's sessions (ownership enforced in SQL).
func (r *Repository) DeleteForUser(ctx context.Context, sessionID, userID uuid.UUID) (int64, error) {
	return r.q.DeleteUserSession(ctx, postgres.DeleteUserSessionParams{ID: sessionID, UserID: userID})
}

// DeleteForUserExcept revokes all of a user's sessions except one.
func (r *Repository) DeleteForUserExcept(ctx context.Context, userID, exceptID uuid.UUID) (int64, error) {
	return r.q.DeleteUserSessionsExcept(ctx, postgres.DeleteUserSessionsExceptParams{UserID: userID, ID: exceptID})
}

// DeleteAllForUser revokes every session of a user (cascade on delete).
func (r *Repository) DeleteAllForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	return r.q.DeleteUserSessions(ctx, userID)
}

// Create persists a whole domain session. Every column value — id and all
// timestamps included — comes from the entity (domain-owned defaults).
func (r *Repository) Create(ctx context.Context, s authModel.Session) (authModel.Session, error) {
	metaBytes, err := json.Marshal(s.Metadata)
	if err != nil {
		return authModel.Session{}, err
	}
	row, err := r.q.CreateSession(ctx, postgres.CreateSessionParams{
		ID:        s.ID,
		UserID:    s.UserID,
		ExpiresAt: s.ExpiresAt,
		LastSeen:  s.LastSeen,
		CreatedAt: s.CreatedAt,
		Metadata:  metaBytes,
	})
	if err != nil {
		return authModel.Session{}, err
	}
	return toDomain(row), nil
}

// GetByID loads one session. Not found propagates the raw repo error for the
// caller to classify (repositoryTools.IsObjectNotFoundError).
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (authModel.Session, error) {
	row, err := r.q.GetSessionByID(ctx, id)
	if err != nil {
		return authModel.Session{}, err
	}
	return toDomain(row), nil
}

// ListByUser returns the user's sessions, most recently seen first.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]authModel.Session, error) {
	rows, err := r.q.GetSessionsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]authModel.Session, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, nil
}

// toDomain maps a sqlc row to the domain entity. Metadata that fails to parse
// is forgiven (zero value): validations evolve, historical rows must always
// load — never validate on the read side.
func toDomain(row postgres.Session) authModel.Session {
	var meta authModel.SessionMetadata
	_ = json.Unmarshal(row.Metadata, &meta)
	return authModel.Session{
		ID:        row.ID,
		UserID:    row.UserID,
		ExpiresAt: row.ExpiresAt,
		LastSeen:  row.LastSeen,
		CreatedAt: row.CreatedAt,
		Metadata:  meta,
	}
}
