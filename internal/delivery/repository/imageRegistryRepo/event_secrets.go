package imageRegistryRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

// EventSecrets stores the organizer registry secrets of events. Every query is
// scoped by event, so a secret of another event matches no row. Tokens arrive
// and leave sealed. Writes are one statement; Update and Delete report whether
// a row matched (false: not found for this event).
type EventSecrets struct {
	q Queries
}

func NewEventSecrets(q Queries) *EventSecrets {
	return &EventSecrets{q: q}
}

func toSecret(r postgres.EventRegistrySecret) imageRegistryModel.EventSecret {
	check, _ := imageRegistryModel.ParseCheck(r.CredentialCheck) // a corrupt value reads as never checked
	return imageRegistryModel.EventSecret{
		ID: r.ID, EventID: r.EventID, Host: r.Host, RepositoryPrefix: r.RepositoryPrefix, Username: r.Username,
		TokenCiphertext: r.TokenCiphertext, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Check: check,
	}
}

// SecretToCheck is a secret of a live event with the event's labels for a notification.
type SecretToCheck struct {
	Secret    imageRegistryModel.EventSecret
	EventTag  string
	EventName string
}

// ListToCheck lists the secrets of events that are not finished and not archived at now.
func (r *EventSecrets) ListToCheck(ctx context.Context, now time.Time) ([]SecretToCheck, error) {
	rows, err := r.q.ListEventRegistrySecretsToCheck(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]SecretToCheck, 0, len(rows))
	for _, row := range rows {
		check, _ := imageRegistryModel.ParseCheck(row.CredentialCheck)
		out = append(out, SecretToCheck{
			Secret: imageRegistryModel.EventSecret{
				ID: row.ID, EventID: row.EventID, Host: row.Host, RepositoryPrefix: row.RepositoryPrefix, Username: row.Username,
				TokenCiphertext: row.TokenCiphertext, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Check: check,
			},
			EventTag: row.EventTag, EventName: row.EventName,
		})
	}
	return out, nil
}

// SaveCheck writes the check result of the secret as it was when checked (its UpdatedAt); false: the secret was
// edited or removed meanwhile and the result is dropped.
func (r *EventSecrets) SaveCheck(ctx context.Context, s imageRegistryModel.EventSecret) (bool, error) {
	raw, err := s.Check.Marshal()
	if err != nil {
		return false, err
	}
	n, err := r.q.UpdateEventRegistrySecretCheck(ctx, postgres.UpdateEventRegistrySecretCheckParams{ID: s.ID, UpdatedAt: s.UpdatedAt, CredentialCheck: raw})
	return n > 0, err
}

// EventLabel returns the tag and name of an event for a notification.
func (r *EventSecrets) EventLabel(ctx context.Context, eventID uuid.UUID) (tag, name string, err error) {
	e, err := r.q.GetEventByID(ctx, eventID)
	if err != nil {
		return "", "", err
	}
	return e.Tag, e.Name, nil
}

func (r *EventSecrets) List(ctx context.Context, eventID uuid.UUID) ([]imageRegistryModel.EventSecret, error) {
	rows, err := r.q.ListEventRegistrySecrets(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]imageRegistryModel.EventSecret, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSecret(row))
	}
	return out, nil
}

// Get returns the raw not-found error (pgx.ErrNoRows) when the event has no such secret.
func (r *EventSecrets) Get(ctx context.Context, eventID, id uuid.UUID) (imageRegistryModel.EventSecret, error) {
	row, err := r.q.GetEventRegistrySecret(ctx, postgres.GetEventRegistrySecretParams{ID: id, EventID: eventID})
	if err != nil {
		return imageRegistryModel.EventSecret{}, err
	}
	return toSecret(row), nil
}

func (r *EventSecrets) Create(ctx context.Context, s imageRegistryModel.EventSecret) error {
	return r.q.InsertEventRegistrySecret(ctx, postgres.InsertEventRegistrySecretParams{
		ID: s.ID, EventID: s.EventID, Host: s.Host, RepositoryPrefix: s.RepositoryPrefix, Username: s.Username,
		TokenCiphertext: s.TokenCiphertext, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	})
}

func (r *EventSecrets) Update(ctx context.Context, s imageRegistryModel.EventSecret) (bool, error) {
	n, err := r.q.UpdateEventRegistrySecret(ctx, postgres.UpdateEventRegistrySecretParams{
		ID: s.ID, EventID: s.EventID, Host: s.Host, RepositoryPrefix: s.RepositoryPrefix, Username: s.Username,
		TokenCiphertext: s.TokenCiphertext, UpdatedAt: s.UpdatedAt,
	})
	return n > 0, err
}

func (r *EventSecrets) Delete(ctx context.Context, eventID, id uuid.UUID) (bool, error) {
	n, err := r.q.DeleteEventRegistrySecret(ctx, postgres.DeleteEventRegistrySecretParams{ID: id, EventID: eventID})
	return n > 0, err
}
