// Package temporalCodeRepo is the repository for single-use temporal codes
// (password reset / email change): it accepts and returns the domain Code entity
// and keeps sqlc row/param types inside. The JSON Data is stored/returned as
// opaque bytes; the use case owns marshalling the concrete payloads.
package temporalCodeRepo

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateTemporalCode(ctx context.Context, arg postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error)
	GetTemporalCodeByCode(ctx context.Context, code string) (postgres.TemporalCode, error)
	DeleteTemporalCode(ctx context.Context, id uuid.UUID) (int64, error)
	DeleteTemporalCodesForUser(ctx context.Context, arg postgres.DeleteTemporalCodesForUserParams) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ToDomain maps a stored row to the domain entity.
func ToDomain(row postgres.TemporalCode) temporalCodeModel.Code {
	return temporalCodeModel.Code{
		ID:        row.ID,
		Code:      row.Code,
		Type:      row.Type,
		Data:      row.Data,
		ExpiresAt: row.ExpiresAt,
	}
}

// Create persists a temporal code.
func (r *Repository) Create(ctx context.Context, c temporalCodeModel.Code) error {
	_, err := r.q.CreateTemporalCode(ctx, postgres.CreateTemporalCodeParams{
		ID:        c.ID,
		Code:      c.Code,
		Type:      c.Type,
		Data:      c.Data,
		ExpiresAt: c.ExpiresAt,
	})
	return err
}

// GetByCode loads a temporal code by its opaque value. Returns the raw not-found
// error for the caller to classify (anti-enumeration).
func (r *Repository) GetByCode(ctx context.Context, code string) (temporalCodeModel.Code, error) {
	row, err := r.q.GetTemporalCodeByCode(ctx, code)
	if err != nil {
		return temporalCodeModel.Code{}, err
	}
	return ToDomain(row), nil
}

// Delete removes a temporal code (single-use consumption).
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.DeleteTemporalCode(ctx, id)
}

// DeleteForUser removes every code of the type issued to the user.
func (r *Repository) DeleteForUser(ctx context.Context, codeType int32, userID uuid.UUID) (int64, error) {
	return r.q.DeleteTemporalCodesForUser(ctx, postgres.DeleteTemporalCodesForUserParams{Type: codeType, UserID: userID.String()})
}
