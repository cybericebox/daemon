// Package tablePrefsRepo stores the saved table views of a person.
package tablePrefsRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	tablePrefsModel "github.com/cybericebox/daemon/internal/model/tablePrefs"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	GetUserTablePrefs(
		ctx context.Context,
		arg postgres.GetUserTablePrefsParams,
	) (postgres.GetUserTablePrefsRow, error)
	UpsertUserTablePrefs(
		ctx context.Context,
		arg postgres.UpsertUserTablePrefsParams,
	) (int64, error)
	DeleteUserTablePrefs(
		ctx context.Context,
		arg postgres.DeleteUserTablePrefsParams,
	) (int64, error)
	DeleteAllUserTablePrefs(ctx context.Context, userID uuid.UUID) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Get returns the saved view, or nil when the person has none.
func (r *Repository) Get(
	ctx context.Context,
	userID uuid.UUID,
	key string,
) (*tablePrefsModel.Prefs, error) {
	row, err := r.q.GetUserTablePrefs(
		ctx,
		postgres.GetUserTablePrefsParams{UserID: userID, TableKey: key},
	)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, nil
		}
		return nil, err
	}
	var prefs tablePrefsModel.Prefs
	if err = json.Unmarshal(row.Prefs, &prefs); err != nil {
		return nil, nil // a view that no longer parses is as good as none
	}
	return &prefs, nil
}

// Save stores the view; false when it would be a new table over the per-person cap.
func (r *Repository) Save(
	ctx context.Context,
	userID uuid.UUID,
	key string,
	raw []byte,
	at time.Time,
	maxTables int,
) (bool, error) {
	n, err := r.q.UpsertUserTablePrefs(ctx, postgres.UpsertUserTablePrefsParams{
		UserID: userID, TableKey: key, Prefs: raw, UpdatedAt: at, MaxTables: int64(maxTables),
	})
	return n > 0, err
}

// Delete forgets the view of one table.
func (r *Repository) Delete(ctx context.Context, userID uuid.UUID, key string) error {
	_, err := r.q.DeleteUserTablePrefs(
		ctx,
		postgres.DeleteUserTablePrefsParams{UserID: userID, TableKey: key},
	)
	return err
}

// DeleteAll forgets every view of the person.
func (r *Repository) DeleteAll(ctx context.Context, userID uuid.UUID) error {
	_, err := r.q.DeleteAllUserTablePrefs(ctx, userID)
	return err
}
