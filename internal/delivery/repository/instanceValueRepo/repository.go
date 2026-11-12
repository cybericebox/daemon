// Package instanceValueRepo keeps the variables of the infrastructure drawn once per instance (a team's stand of an event
// exercise), so a lab that is rebuilt or reset gets the same values.
package instanceValueRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type Repository struct{ db postgres.DBTX }

func New(db postgres.DBTX) *Repository { return &Repository{db: db} }

// PinStand stores the drawn values of a team's stand variables unless a value of that name is already stored (a concurrent
// build may have won) and returns the values in force: the stored ones win.
func (r *Repository) PinStand(ctx context.Context, teamID, eventExerciseID uuid.UUID, drawn map[string]string, now time.Time) (map[string]string, error) {
	for name, value := range drawn {
		if _, err := r.db.Exec(ctx, `INSERT INTO stand_variables (event_team_id, event_exercise_id, name, value, created_at)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, teamID, eventExerciseID, name, value, now); err != nil {
			return nil, err
		}
	}
	return r.read(ctx, `SELECT name, value FROM stand_variables WHERE event_team_id = $1 AND event_exercise_id = $2`, teamID, eventExerciseID)
}

// Stand reads the stored variables of a team's stand.
func (r *Repository) Stand(ctx context.Context, teamID, eventExerciseID uuid.UUID) (map[string]string, error) {
	return r.read(ctx, `SELECT name, value FROM stand_variables WHERE event_team_id = $1 AND event_exercise_id = $2`, teamID, eventExerciseID)
}

func (r *Repository) read(ctx context.Context, query string, args ...any) (map[string]string, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, value string
		if err = rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, rows.Err()
}
