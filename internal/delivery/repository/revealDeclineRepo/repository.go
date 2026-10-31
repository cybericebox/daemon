// Package revealDeclineRepo keeps the teams that were kept out of a task's cohort for lack of room.
package revealDeclineRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type Repository struct{ db postgres.DBTX }

func New(db postgres.DBTX) *Repository { return &Repository{db: db} }

// Decline keeps the team out of the cohort of the task: it is recorded, and removed from the cohort of the barrier at
// once, so the show does not wait for it.
func (r *Repository) Decline(ctx context.Context, eventID, setID, teamID uuid.UUID, reason string, now time.Time) error {
	if _, err := r.db.Exec(ctx, `INSERT INTO event_lab_reveal_declined (event_exercise_id, event_team_id, reason, declined_at)
VALUES ($1, $2, $3, $4) ON CONFLICT (event_exercise_id, event_team_id) DO UPDATE SET reason = EXCLUDED.reason, declined_at = EXCLUDED.declined_at`,
		setID, teamID, reason, now); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `UPDATE event_lab_reveal_barriers SET eligible_team_ids = array_remove(eligible_team_ids, $3)
WHERE event_id = $1 AND event_exercise_id = $2 AND opened_at IS NULL`, eventID, setID, teamID)
	return err
}
