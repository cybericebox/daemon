// Package eventActivityRepo appends rows to the event activity log. The log
// is not an aggregate: rows are written once and read only by analytics.
package eventActivityRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
)

type Queries interface {
	CreateEventActivity(context.Context, postgres.CreateEventActivityParams) error
	CreateEventActivityOnce(context.Context, postgres.CreateEventActivityOnceParams) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Append writes one row.
func (r *Repository) Append(ctx context.Context, a eventActivityModel.Activity) error {
	data, err := encodeData(a.Data)
	if err != nil {
		return err
	}
	return r.q.CreateEventActivity(ctx, postgres.CreateEventActivityParams{
		EventID: a.EventID, UserID: uuid.NullUUID{UUID: a.UserID, Valid: a.UserID != uuid.Nil},
		TeamID: nullable(a.TeamID), Kind: string(a.Kind), SubjectID: nullable(a.SubjectID), At: a.At, Data: data,
	})
}

// AppendOnce writes the row unless the same user logged the same kind for
// the same subject within window before it; it reports whether it wrote.
// The activity must have a subject.
func (r *Repository) AppendOnce(ctx context.Context, a eventActivityModel.Activity, window time.Duration) (bool, error) {
	data, err := encodeData(a.Data)
	if err != nil {
		return false, err
	}
	var subject uuid.UUID
	if a.SubjectID != nil {
		subject = *a.SubjectID
	}
	n, err := r.q.CreateEventActivityOnce(ctx, postgres.CreateEventActivityOnceParams{
		EventID: a.EventID, UserID: a.UserID, TeamID: nullable(a.TeamID), Kind: string(a.Kind),
		SubjectID: subject, At: a.At, Data: data, Since: a.At.Add(-window),
	})
	return n == 1, err
}

func encodeData(data map[string]any) ([]byte, error) {
	if len(data) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(data)
}

func nullable(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}
