// Package revealDeclineRepo keeps the teams that were kept out of a task's cohort for lack of room.
package revealDeclineRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// Queries are what the repository needs of the database.
type Queries interface {
	DeclineEventLabRevealTeam(ctx context.Context, arg postgres.DeclineEventLabRevealTeamParams) error
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Decline keeps the team out of the cohort of the task: it is recorded for the task's current revision and reveal mode
// (a decline of an older tuple no longer applies, and is dropped when the barrier is invalidated), and removed from
// the cohort of the barrier at once, so the show does not wait for it. On a unit of work's queries it is part of the
// unit that decided it.
func (r *Repository) Decline(ctx context.Context, eventID, setID, teamID uuid.UUID, reason string, now time.Time) error {
	return r.q.DeclineEventLabRevealTeam(ctx, postgres.DeclineEventLabRevealTeamParams{TeamID: teamID, Reason: reason, Now: now, EventID: eventID, EventExerciseID: setID})
}
