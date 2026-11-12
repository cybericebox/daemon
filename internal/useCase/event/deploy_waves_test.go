package event

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

func pendingLab(team, exercise uuid.UUID, created time.Time, deployed bool) labBindingRepo.PendingLab {
	b := labBindingModel.Binding{ID: uuid.Must(uuid.NewV7()), EventTeamID: team, CreatedAt: created}
	if deployed {
		at := created
		b.DeployedAt = &at
	}
	return labBindingRepo.PendingLab{Binding: b, EventExerciseID: exercise}
}

// Owner, 2026-10-26: a deploy wave starts with the teams that are ready; a team that becomes ready later gets its
// own wave with a higher priority.
func TestDeployGroupOrderPutsALateTeamBeforeTheRestOfTheFirstWave(t *testing.T) {
	a, b, late := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	x, y := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	t0 := time.Now()
	first := []labBindingRepo.PendingLab{
		pendingLab(a, x, t0, false), pendingLab(b, x, t0.Add(time.Second), false),
		pendingLab(a, y, t0.Add(2*time.Second), false), pendingLab(b, y, t0.Add(3*time.Second), false),
	}
	lateLabs := []labBindingRepo.PendingLab{pendingLab(late, x, t0.Add(10*time.Minute), false), pendingLab(late, y, t0.Add(10*time.Minute), false)}
	observed := pendingLab(a, x, t0, true)
	got := deployGroupOrder(append(append([]labBindingRepo.PendingLab{observed}, first...), lateLabs...))

	require.Len(t, got, 7)
	require.Equal(t, late, got[0].Binding.EventTeamID)
	require.Equal(t, late, got[1].Binding.EventTeamID)
	// The first wave keeps its grouping by exercise.
	require.Equal(t, []uuid.UUID{x, x, y, y}, []uuid.UUID{got[2].EventExerciseID, got[3].EventExerciseID, got[4].EventExerciseID, got[5].EventExerciseID})
	require.NotNil(t, got[6].Binding.DeployedAt, "labs to observe come last")
}

func TestDeployGroupOrderWithoutALateTeamKeepsTheOrder(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	x, y := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	t0 := time.Now()
	got := deployGroupOrder([]labBindingRepo.PendingLab{pendingLab(a, x, t0, false), pendingLab(a, y, t0, false), pendingLab(b, x, t0.Add(time.Second), false)})
	require.Equal(t, []uuid.UUID{x, x, y}, []uuid.UUID{got[0].EventExerciseID, got[1].EventExerciseID, got[2].EventExerciseID})
}
