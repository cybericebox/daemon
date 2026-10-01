// Package secretEnvelopeRepo maps persistent secret envelopes at the storage
// boundary. It deliberately exposes only opaque ciphertext fields.
package secretEnvelopeRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	secretModel "github.com/cybericebox/daemon/internal/model/secret"
)

type Queries interface {
	CreateSecretEnvelope(context.Context, postgres.CreateSecretEnvelopeParams) (postgres.SecretEnvelope, error)
	GetSecretEnvelope(context.Context, uuid.UUID) (postgres.SecretEnvelope, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, envelope secretModel.Envelope) error {
	_, err := r.q.CreateSecretEnvelope(ctx, postgres.CreateSecretEnvelopeParams{
		ID: envelope.ID, Purpose: string(envelope.Purpose), KeyVersion: envelope.KeyVersion,
		SignalType: envelope.SignalType, FieldPath: envelope.FieldPath, ScopeEventID: envelope.ScopeEventID,
		RecipientUserID: nullableUUID(envelope.RecipientUserID), Ciphertext: envelope.Ciphertext,
		WrappedDataKey: envelope.WrappedDataKey, ExpiresAt: nullableTime(envelope.ExpiresAt), CreatedAt: envelope.CreatedAt,
	})
	return err
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (secretModel.Envelope, error) {
	row, err := r.q.GetSecretEnvelope(ctx, id)
	if err != nil {
		return secretModel.Envelope{}, err
	}
	return secretModel.Envelope{
		ID: row.ID, Purpose: secretModel.KeyPurpose(row.Purpose), KeyVersion: row.KeyVersion,
		SignalType: row.SignalType, FieldPath: row.FieldPath, ScopeEventID: row.ScopeEventID,
		RecipientUserID: optionalUUID(row.RecipientUserID), Ciphertext: row.Ciphertext,
		WrappedDataKey: row.WrappedDataKey, ExpiresAt: optionalTime(row.ExpiresAt), CreatedAt: row.CreatedAt,
	}, nil
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func optionalUUID(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := value.UUID
	return &result
}

func nullableTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
