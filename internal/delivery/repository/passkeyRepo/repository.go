// Package passkeyRepo is the repository for passkeys: it accepts and returns the domain Passkey entity and
// keeps the sqlc row types inside.
package passkeyRepo

import (
	"context"
	"math"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	passkeyModel "github.com/cybericebox/daemon/internal/model/passkey"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateUserPasskey(
		ctx context.Context,
		arg postgres.CreateUserPasskeyParams,
	) (postgres.UserPasskey, error)
	GetUserPasskeyByCredentialID(
		ctx context.Context,
		credentialID []byte,
	) (postgres.UserPasskey, error)
	ListUserPasskeys(ctx context.Context, userID uuid.UUID) ([]postgres.UserPasskey, error)
	CountUserPasskeys(ctx context.Context, userID uuid.UUID) (int64, error)
	UseUserPasskey(ctx context.Context, arg postgres.UseUserPasskeyParams) (int64, error)
	RenameUserPasskey(ctx context.Context, arg postgres.RenameUserPasskeyParams) (int64, error)
	DeleteUserPasskey(ctx context.Context, arg postgres.DeleteUserPasskeyParams) (int64, error)
	DeleteUserPasskeys(ctx context.Context, userID uuid.UUID) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ToDomain maps a stored row to the domain entity.
func ToDomain(row postgres.UserPasskey) passkeyModel.Passkey {
	p := passkeyModel.Passkey{
		ID:             row.ID,
		UserID:         row.UserID,
		CredentialID:   row.CredentialID,
		PublicKey:      row.PublicKey,
		SignCount:      clampCounter(row.SignCount),
		AAGUID:         row.Aaguid,
		Transports:     row.Transports,
		UserVerified:   row.UserVerified,
		BackupEligible: row.BackupEligible,
		BackupState:    row.BackupState,
		Name:           row.Name,
		CreatedAt:      row.CreatedAt,
	}
	if row.LastUsedAt.Valid {
		at := row.LastUsedAt.Time
		p.LastUsedAt = &at
	}
	return p
}

func clampCounter(v int64) uint32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// Create stores a passkey unless the user already holds limit of them; found is false when the cap refused it.
func (r *Repository) Create(
	ctx context.Context,
	p passkeyModel.Passkey,
	limit int,
) (passkeyModel.Passkey, bool, error) {
	transports := p.Transports
	if transports == nil {
		transports = []string{}
	}
	aaguid := p.AAGUID
	if aaguid == nil {
		aaguid = []byte{}
	}
	row, err := r.q.CreateUserPasskey(ctx, postgres.CreateUserPasskeyParams{
		ID:           p.ID,
		UserID:       p.UserID,
		CredentialID: p.CredentialID,
		PublicKey:    p.PublicKey,
		SignCount: int64(
			p.SignCount,
		),
		Aaguid:         aaguid,
		Transports:     transports,
		UserVerified:   p.UserVerified,
		BackupEligible: p.BackupEligible,
		BackupState:    p.BackupState,
		Name:           p.Name,
		CreatedAt:      p.CreatedAt,
		MaxPerUser:     int64(limit),
	})
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return passkeyModel.Passkey{}, false, nil
		}
		return passkeyModel.Passkey{}, false, err
	}
	return ToDomain(row), true, nil
}

// GetByCredentialID loads a passkey by its authenticator id. Returns the raw not-found error.
func (r *Repository) GetByCredentialID(
	ctx context.Context,
	credentialID []byte,
) (passkeyModel.Passkey, error) {
	row, err := r.q.GetUserPasskeyByCredentialID(ctx, credentialID)
	if err != nil {
		return passkeyModel.Passkey{}, err
	}
	return ToDomain(row), nil
}

// ListByUser returns the user's passkeys, oldest first.
func (r *Repository) ListByUser(
	ctx context.Context,
	userID uuid.UUID,
) ([]passkeyModel.Passkey, error) {
	rows, err := r.q.ListUserPasskeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]passkeyModel.Passkey, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

// CountByUser returns how many passkeys the user holds.
func (r *Repository) CountByUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	return r.q.CountUserPasskeys(ctx, userID)
}

// RecordUse stores the counter and backup state of a sign-in; false means the counter did not advance (a replayed
// or cloned assertion) and nothing was written.
func (r *Repository) RecordUse(
	ctx context.Context,
	id uuid.UUID,
	signCount uint32,
	backupState bool,
	at time.Time,
) (bool, error) {
	n, err := r.q.UseUserPasskey(ctx, postgres.UseUserPasskeyParams{
		ID:          id,
		SignCount:   int64(signCount),
		BackupState: backupState,
		UsedAt:      pgtype.Timestamptz{Time: at, Valid: true},
	})
	return n > 0, err
}

// Rename sets the name of the user's passkey; false when the user has no such passkey.
func (r *Repository) Rename(ctx context.Context, userID, id uuid.UUID, name string) (bool, error) {
	n, err := r.q.RenameUserPasskey(
		ctx,
		postgres.RenameUserPasskeyParams{ID: id, UserID: userID, Name: name},
	)
	return n > 0, err
}

// Delete removes the user's passkey; false when the user has no such passkey.
func (r *Repository) Delete(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	n, err := r.q.DeleteUserPasskey(ctx, postgres.DeleteUserPasskeyParams{ID: id, UserID: userID})
	return n > 0, err
}

// DeleteAll removes every passkey of the user.
func (r *Repository) DeleteAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	return r.q.DeleteUserPasskeys(ctx, userID)
}
