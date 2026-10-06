// Package sessionRepo is the repository for the Session aggregate: it accepts
// and returns whole domain entities (authModel.Session) and keeps all
// row-mapping concerns — including SessionMetadata JSON (de)serialization —
// out of the business layer.
//
// Narrow queries deliberately NOT wrapped here:
//   - TouchSession / UpdateUserLastSeen — the batched last_seen write (session.Seen);
//     a full-row update there would race concurrent mutations (lost update);
//   - Revoke* — set operations, not aggregate mutations: each deletes the session rows and writes their
//     revocation rows in one statement, so a session never ends without its revocation;
//   - the revocation list reads (the replicas' poll).
package sessionRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/session"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateSession(ctx context.Context, arg postgres.CreateSessionParams) (postgres.Session, error)
	GetSessionByID(ctx context.Context, id uuid.UUID) (postgres.Session, error)
	GetSessionsByUser(ctx context.Context, userID uuid.UUID) ([]postgres.Session, error)
	TouchSession(ctx context.Context, arg postgres.TouchSessionParams) (int64, error)
	RevokeSession(ctx context.Context, arg postgres.RevokeSessionParams) ([]postgres.RevokeSessionRow, error)
	RevokeUserSession(ctx context.Context, arg postgres.RevokeUserSessionParams) ([]postgres.RevokeUserSessionRow, error)
	RevokeUserSessionsExcept(ctx context.Context, arg postgres.RevokeUserSessionsExceptParams) ([]postgres.RevokeUserSessionsExceptRow, error)
	RevokeUserSessions(ctx context.Context, arg postgres.RevokeUserSessionsParams) ([]postgres.RevokeUserSessionsRow, error)
	GetDatabaseTime(ctx context.Context) (time.Time, error)
	ListActiveSessionRevocations(ctx context.Context) ([]postgres.SessionRevocation, error)
	ListSessionRevocationsSince(ctx context.Context, revokedAt time.Time) ([]postgres.SessionRevocation, error)
	DeleteExpiredSessionRevocations(ctx context.Context) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// Touch moves a session's last_seen and idle deadline forward (the batched write; never backward).
func (r *Repository) Touch(ctx context.Context, id uuid.UUID, seenAt, expiresAt time.Time) (int64, error) {
	return r.q.TouchSession(ctx, postgres.TouchSessionParams{ID: id, SeenAt: seenAt, ExpiresAt: expiresAt})
}

// Revoked is a session that was just ended and the time its cookies could still pass the expiry check.
type Revoked struct {
	SessionID uuid.UUID
	ExpiresAt time.Time
}

// seconds of a lifetime for the revocation expiry; no absolute limit counts as a century.
func seconds(l session.Lifetimes) (absolute, idle float64) {
	absolute = l.Absolute.Seconds()
	if l.Absolute <= 0 {
		absolute = (100 * 365 * 24 * time.Hour).Seconds()
	}
	return absolute, l.Idle.Seconds()
}

// Revoke ends a session by id: the row goes and the revocation row is written.
func (r *Repository) Revoke(ctx context.Context, id uuid.UUID, l session.Lifetimes) ([]Revoked, error) {
	absolute, idle := seconds(l)
	rows, err := r.q.RevokeSession(ctx, postgres.RevokeSessionParams{ID: id, AbsoluteSecs: absolute, IdleSecs: idle})
	out := make([]Revoked, 0, len(rows))
	for _, row := range rows {
		out = append(out, Revoked{row.SessionID, row.ExpiresAt})
	}
	return out, err
}

// RevokeForUser ends one of a user's sessions (ownership enforced in SQL).
func (r *Repository) RevokeForUser(ctx context.Context, sessionID, userID uuid.UUID, l session.Lifetimes) ([]Revoked, error) {
	absolute, idle := seconds(l)
	rows, err := r.q.RevokeUserSession(ctx, postgres.RevokeUserSessionParams{ID: sessionID, OwnerID: userID, AbsoluteSecs: absolute, IdleSecs: idle})
	out := make([]Revoked, 0, len(rows))
	for _, row := range rows {
		out = append(out, Revoked{row.SessionID, row.ExpiresAt})
	}
	return out, err
}

// RevokeForUserExcept ends all of a user's sessions except one.
func (r *Repository) RevokeForUserExcept(ctx context.Context, userID, exceptID uuid.UUID, l session.Lifetimes) ([]Revoked, error) {
	absolute, idle := seconds(l)
	rows, err := r.q.RevokeUserSessionsExcept(ctx, postgres.RevokeUserSessionsExceptParams{OwnerID: userID, ExceptID: exceptID, AbsoluteSecs: absolute, IdleSecs: idle})
	out := make([]Revoked, 0, len(rows))
	for _, row := range rows {
		out = append(out, Revoked{row.SessionID, row.ExpiresAt})
	}
	return out, err
}

// RevokeAllForUser ends every session of a user (sign out everywhere, password change, block, delete).
func (r *Repository) RevokeAllForUser(ctx context.Context, userID uuid.UUID, l session.Lifetimes) ([]Revoked, error) {
	absolute, idle := seconds(l)
	rows, err := r.q.RevokeUserSessions(ctx, postgres.RevokeUserSessionsParams{OwnerID: userID, AbsoluteSecs: absolute, IdleSecs: idle})
	out := make([]Revoked, 0, len(rows))
	for _, row := range rows {
		out = append(out, Revoked{row.SessionID, row.ExpiresAt})
	}
	return out, err
}

// ── the revocation list (session.RevocationSource) ──

func (r *Repository) DatabaseTime(ctx context.Context) (time.Time, error) {
	return r.q.GetDatabaseTime(ctx)
}

func (r *Repository) ActiveRevocations(ctx context.Context) ([]session.Revocation, error) {
	rows, err := r.q.ListActiveSessionRevocations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]session.Revocation, 0, len(rows))
	for _, row := range rows {
		out = append(out, session.Revocation{SessionID: row.SessionID, UserID: row.UserID, RevokedAt: row.RevokedAt, ExpiresAt: row.ExpiresAt})
	}
	return out, nil
}

func (r *Repository) RevocationsSince(ctx context.Context, since time.Time) ([]session.Revocation, error) {
	rows, err := r.q.ListSessionRevocationsSince(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]session.Revocation, 0, len(rows))
	for _, row := range rows {
		out = append(out, session.Revocation{SessionID: row.SessionID, UserID: row.UserID, RevokedAt: row.RevokedAt, ExpiresAt: row.ExpiresAt})
	}
	return out, nil
}

func (r *Repository) DeleteExpiredRevocations(ctx context.Context) (int64, error) {
	return r.q.DeleteExpiredSessionRevocations(ctx)
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
