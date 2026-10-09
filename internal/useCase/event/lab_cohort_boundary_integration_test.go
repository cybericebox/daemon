package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCohortFreezesAtPreparationAfterZeroTeamConfigOrSourceEdit(t *testing.T) {
	for _, edit := range []string{"same all_ready config", "new source"} {
		t.Run(edit, func(t *testing.T) {
			f := newStandFixture(t)
			set := f.attachSharedSet(t)
			ctx := context.Background()
			now := time.Now().UTC()
			_, err := f.db.Pool.Exec(ctx, `UPDATE event_teams SET admitted_manually=false,admission_locked=false,member_count=0,formed_at=NULL WHERE event_id=$1`, f.eventID)
			require.NoError(t, err)
			f.uc.SetLifecycleControls(true)
			if edit == "same all_ready config" {
				mode := eventConfigModel.RevealAllReady
				_, err = f.uc.UpdateEventConfig(ctx, f.eventID, event.UpdateConfigInput{TaskRevealMode: &mode, MinTeamSize: waveMinutes(2)}, f.ownerID)
				require.NoError(t, err)
			} else {
				next := publishLifecycleSource(t, f, set)
				_, err = f.uc.ReplaceEventExercise(ctx, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next}, f.ownerID)
				require.NoError(t, err)
			}
			var premature int
			require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_lab_reveal_barriers WHERE event_exercise_id=$1`, set.exerciseID).Scan(&premature))
			require.Zero(t, premature, "authorized edit must not freeze a pre-preparation empty/partial cohort")
			_, err = f.db.Pool.Exec(ctx, `UPDATE event_teams SET admitted_manually=true,formed_at=$2 WHERE id=ANY($1::uuid[])`, []uuid.UUID{f.blueID, f.redID}, now)
			require.NoError(t, err)
			r, err := calModel.NewEventReservation(calModel.EventInput{EventID: f.eventID, Window: calModel.Window{Start: now.Add(-time.Hour), End: now.Add(6 * time.Hour)}, Teams: 20, PerTeam: calModel.Amount{CPUMillicores: 10000, MemoryBytes: 10 << 30}}, f.ownerID, now)
			require.NoError(t, err)
			r.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 20}}, 0, now)
			require.NoError(t, resourceCalendarRepo.New(f.db.Queries).CreateReservation(ctx, r))
			f.uc.SetAllocationAccounting(true)
			f.pass(t)
			repo := eventLabRevealRepo.New(f.db.Queries)
			barrier, err := repo.Barrier(ctx, f.eventID, set.exerciseID)
			require.NoError(t, err)
			require.Len(t, barrier.EligibleTeamIds, 2)
			original := append([]uuid.UUID(nil), barrier.EligibleTeamIds...)
			_, err = f.db.Pool.Exec(ctx, `UPDATE event_teams SET admitted_manually=true,formed_at=$2 WHERE id=$1`, f.smallID, now)
			require.NoError(t, err)
			frozen, err := repo.Freeze(ctx, f.eventID, set.exerciseID, now)
			require.NoError(t, err)
			require.Equal(t, original, frozen, "actual prepared cohort must remain immutable")
			// Exact synthetic current-generation receipts exercise the SQL boundary;
			// this test makes no native readiness/fabric/cgroup assertion.
			for _, team := range []uuid.UUID{f.blueID, f.redID} {
				_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET agent_uid=$3,agent_generation=1,actual_state='Running',runtime_ready=true,observed_revision=desired_revision WHERE event_exercise_id=$1 AND event_team_id=$2`, set.exerciseID, team, "synthetic-"+team.String())
				require.NoError(t, err)
				_, err = f.db.Pool.Exec(ctx, `UPDATE lab_bindings b SET readiness=1 FROM event_team_labs l WHERE b.lab_id=l.id AND l.event_exercise_id=$1 AND l.event_team_id=$2`, set.exerciseID, team)
				require.NoError(t, err)
				require.NoError(t, repo.OpenReady(ctx, f.eventID, now))
				barrier, err = repo.Barrier(ctx, f.eventID, set.exerciseID)
				require.NoError(t, err)
				require.Equal(t, team == f.redID, barrier.OpenedAt.Valid)
			}
		})
	}
}
