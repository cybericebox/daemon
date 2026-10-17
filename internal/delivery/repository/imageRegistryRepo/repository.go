// Package imageRegistryRepo is the repository of the platform image registry
// settings (platform_images.go: the record of each image pushed to the platform registry; blob_pushes.go: the state of
// the background image blob pushes; event_secrets.go: the organizer secrets of events; event_images.go: the read-side list of the images
// an event's exercises use): one row (id is always true) written whole in one statement. Tokens
// arrive and leave sealed; this package never sees a plaintext token.
package imageRegistryRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	GetImageRegistrySettings(ctx context.Context) (postgres.ImageRegistrySetting, error)
	UpsertImageRegistrySettings(ctx context.Context, arg postgres.UpsertImageRegistrySettingsParams) error
	DeleteImageRegistrySettings(ctx context.Context) error

	ListEventRegistrySecrets(ctx context.Context, eventID uuid.UUID) ([]postgres.EventRegistrySecret, error)
	GetEventRegistrySecret(ctx context.Context, arg postgres.GetEventRegistrySecretParams) (postgres.EventRegistrySecret, error)
	InsertEventRegistrySecret(ctx context.Context, arg postgres.InsertEventRegistrySecretParams) error
	UpdateEventRegistrySecret(ctx context.Context, arg postgres.UpdateEventRegistrySecretParams) (int64, error)
	DeleteEventRegistrySecret(ctx context.Context, arg postgres.DeleteEventRegistrySecretParams) (int64, error)

	UpsertPlatformImage(ctx context.Context, arg postgres.UpsertPlatformImageParams) error
	ListUnreferencedPlatformImages(ctx context.Context, before time.Time) ([]postgres.PlatformImage, error)
	DeletePlatformImage(ctx context.Context, arg postgres.DeletePlatformImageParams) (int64, error)
	IsDigestCollectable(ctx context.Context, arg postgres.IsDigestCollectableParams) (pgtype.Bool, error)
	DeleteStaleImageBlobPushes(ctx context.Context, updatedAt time.Time) (int64, error)
	UpsertImageBlobPush(ctx context.Context, arg postgres.UpsertImageBlobPushParams) error
	GetImageBlobPush(ctx context.Context, digest string) (postgres.ImageBlobPush, error)
	DeleteImageBlobPush(ctx context.Context, digest string) error

	ListEventExerciseImages(ctx context.Context, eventID uuid.UUID) ([]postgres.ListEventExerciseImagesRow, error)
	GetExerciseByID(ctx context.Context, id uuid.UUID) (postgres.Exercise, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// Get loads the settings. Not found propagates the raw repo error for the
// caller to classify (repositoryTools.IsObjectNotFoundError).
func (r *Repository) Get(ctx context.Context) (imageRegistryModel.Settings, error) {
	row, err := r.q.GetImageRegistrySettings(ctx)
	if err != nil {
		return imageRegistryModel.Settings{}, err
	}
	return imageRegistryModel.Settings{
		Provider:            imageRegistryModel.Provider(row.Provider),
		Account:             row.Account,
		Repository:          row.Repository,
		PushTokenCiphertext: row.PushTokenCiphertext,
		PullTokenCiphertext: row.PullTokenCiphertext,
		UpdatedAt:           row.UpdatedAt,
	}, nil
}

// Save writes the whole settings row (created or replaced).
func (r *Repository) Save(ctx context.Context, s imageRegistryModel.Settings) error {
	return r.q.UpsertImageRegistrySettings(ctx, postgres.UpsertImageRegistrySettingsParams{
		Provider:            string(s.Provider),
		Account:             s.Account,
		Repository:          s.Repository,
		PushTokenCiphertext: s.PushTokenCiphertext,
		PullTokenCiphertext: s.PullTokenCiphertext,
		UpdatedAt:           s.UpdatedAt,
	})
}

// Delete removes the settings; deleting absent settings is not an error.
func (r *Repository) Delete(ctx context.Context) error {
	return r.q.DeleteImageRegistrySettings(ctx)
}
