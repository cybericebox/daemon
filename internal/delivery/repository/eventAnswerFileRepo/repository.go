// Package eventAnswerFileRepo stores the files attached to participant and
// team answers. Create writes the row with the media reference that keeps
// the blob alive; Attach is a set operation over one owner's files.
package eventAnswerFileRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
)

type Queries interface {
	CreateEventAnswerFile(ctx context.Context, arg postgres.CreateEventAnswerFileParams) error
	GetEventAnswerFile(ctx context.Context, fileID uuid.UUID) (postgres.EventAnswerFile, error)
	ListEventAnswerFiles(ctx context.Context, fileIds []uuid.UUID) ([]postgres.EventAnswerFile, error)
	AttachEventAnswerFiles(ctx context.Context, arg postgres.AttachEventAnswerFilesParams) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, f eventFormModel.AnswerFile) error {
	return r.q.CreateEventAnswerFile(ctx, postgres.CreateEventAnswerFileParams{
		FileID: f.FileID, EventID: f.EventID, Scope: string(f.Scope), FieldKey: f.FieldKey, Name: f.Name,
		SizeBytes: f.SizeBytes, ContentType: f.ContentType, UploadedBy: f.UploadedBy, CreatedAt: f.CreatedAt,
	})
}

func (r *Repository) Get(ctx context.Context, fileID uuid.UUID) (eventFormModel.AnswerFile, error) {
	row, err := r.q.GetEventAnswerFile(ctx, fileID)
	if err != nil {
		return eventFormModel.AnswerFile{}, err
	}
	return toDomain(row), nil
}

// List returns the rows of the given files keyed by file ID; unknown IDs are
// simply absent.
func (r *Repository) List(ctx context.Context, fileIDs []uuid.UUID) (map[uuid.UUID]eventFormModel.AnswerFile, error) {
	out := make(map[uuid.UUID]eventFormModel.AnswerFile, len(fileIDs))
	if len(fileIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListEventAnswerFiles(ctx, fileIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.FileID] = toDomain(row)
	}
	return out, nil
}

// Attach gives fileIDs to the owner and releases the owner's other files of
// this event and scope.
func (r *Repository) Attach(ctx context.Context, eventID uuid.UUID, scope eventFormModel.AnswerScope, ownerID uuid.UUID, fileIDs []uuid.UUID, now time.Time) error {
	if fileIDs == nil {
		fileIDs = []uuid.UUID{}
	}
	_, err := r.q.AttachEventAnswerFiles(ctx, postgres.AttachEventAnswerFilesParams{
		OwnerID: uuid.NullUUID{UUID: ownerID, Valid: true}, AttachedAt: now, EventID: eventID, Scope: string(scope), FileIds: fileIDs,
	})
	return err
}

func toDomain(row postgres.EventAnswerFile) eventFormModel.AnswerFile {
	f := eventFormModel.AnswerFile{
		FileID: row.FileID, EventID: row.EventID, Scope: eventFormModel.AnswerScope(row.Scope), FieldKey: row.FieldKey,
		Name: row.Name, SizeBytes: row.SizeBytes, ContentType: row.ContentType, UploadedBy: row.UploadedBy,
		OwnerID: row.OwnerID, CreatedAt: row.CreatedAt,
	}
	if row.AttachedAt.Valid {
		attached := row.AttachedAt.Time
		f.AttachedAt = &attached
	}
	return f
}
