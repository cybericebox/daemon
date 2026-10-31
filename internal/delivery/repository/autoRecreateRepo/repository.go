// Package autoRecreateRepo keeps the count of automatic recreations of failed team Labs.
package autoRecreateRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// MaxAttempts is how many times a failed Lab is recreated by itself before the organizers are told.
const MaxAttempts = 3

type Repository struct{ db postgres.DBTX }

func New(db postgres.DBTX) *Repository { return &Repository{db: db} }

// Candidate is a failed Lab (one the agent accepted) that may be recreated: one row per Lab.
type Candidate struct {
	BindingID  uuid.UUID
	TeamID     uuid.UUID
	ExerciseID uuid.UUID
	FailedAt   time.Time
	Reason     string
	// Attempts is how many times it was already recreated.
	Attempts int
}

// Candidates lists the failed, accepted Labs of an event that still run (not closed, not solved, the event not
// finished) and were recreated fewer than MaxAttempts times.
func (r *Repository) Candidates(ctx context.Context, eventID uuid.UUID, now time.Time) ([]Candidate, error) {
	rows, err := r.db.Query(ctx, `SELECT DISTINCT ON (lb.event_team_id, lb.lab_name)
       lb.id, lb.event_team_id, ec.event_exercise_id, COALESCE(lb.failed_at, lb.created_at), COALESCE(lb.failure_reason, ''), COALESCE(ar.attempts, 0)
FROM lab_bindings lb
JOIN event_challenges ec ON ec.id = lb.event_challenge_id
JOIN event_team_labs lab ON lab.id = lb.lab_id AND lab.event_team_id = lb.event_team_id
  AND lab.generation = lb.generation AND lab.lab_group_name = lb.lab_group_name AND lab.lab_name = lb.lab_name
  AND lab.desired_state = 'Running' AND lab.logical_closed_at IS NULL AND lab.close_reason IS DISTINCT FROM 'solved'
JOIN events ev ON ev.id = lb.event_id AND ev.manual_finished_at IS NULL AND (ev.finish_at IS NULL OR ev.finish_at > $2)
LEFT JOIN event_lab_auto_recreate ar ON ar.event_team_id = lb.event_team_id AND ar.event_exercise_id = ec.event_exercise_id
WHERE lb.event_id = $1 AND lb.readiness = 2 AND lb.deployed_at IS NOT NULL AND COALESCE(ar.attempts, 0) < $3
ORDER BY lb.event_team_id, lb.lab_name, lb.created_at, lb.id`, eventID, now, MaxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err = rows.Scan(&c.BindingID, &c.TeamID, &c.ExerciseID, &c.FailedAt, &c.Reason, &c.Attempts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Track starts counting a failed Lab (no attempt made yet); a Lab already tracked is left as it is. While a Lab is
// tracked and has attempts left, the stand counts it as being recreated, not as failed.
func (r *Repository) Track(ctx context.Context, teamID, exerciseID uuid.UUID, reason string, now time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO event_lab_auto_recreate (event_team_id, event_exercise_id, attempts, last_reason, updated_at)
VALUES ($1, $2, 0, $3, $4) ON CONFLICT (event_team_id, event_exercise_id) DO NOTHING`, teamID, exerciseID, reason, now)
	return err
}

// Bump counts one more automatic recreation of the Lab of the team's exercise.
func (r *Repository) Bump(ctx context.Context, teamID, exerciseID uuid.UUID, reason string, now time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO event_lab_auto_recreate (event_team_id, event_exercise_id, attempts, last_reason, updated_at)
VALUES ($1, $2, 1, $3, $4)
ON CONFLICT (event_team_id, event_exercise_id) DO UPDATE SET attempts = event_lab_auto_recreate.attempts + 1, last_reason = EXCLUDED.last_reason, updated_at = EXCLUDED.updated_at`,
		teamID, exerciseID, reason, now)
	return err
}

// Reset forgets the count of one exercise of the team (a Lab that became ready).
func (r *Repository) Reset(ctx context.Context, teamID, exerciseID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM event_lab_auto_recreate WHERE event_team_id = $1 AND event_exercise_id = $2`, teamID, exerciseID)
	return err
}

// ResetTeam forgets the counts of every exercise of the team (the organizer recreated its stand).
func (r *Repository) ResetTeam(ctx context.Context, teamID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM event_lab_auto_recreate WHERE event_team_id = $1`, teamID)
	return err
}
