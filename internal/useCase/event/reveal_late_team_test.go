package event

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
)

func revealLab(team uuid.UUID, reserved bool, created time.Time) eventLabModel.Lab {
	lab := eventLabModel.Lab{TeamID: team, CreatedAt: created}
	lab.Allocation.ConfiguredRequestsKnown = reserved
	return lab
}

// A team is declined only when the cohort already started (some lab holds its reservation); the newest unreserved one goes first.
func TestLateTeamToDecline(t *testing.T) {
	first, second, late, later := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()

	_, ok := lateTeamToDecline([]eventLabModel.Lab{revealLab(first, false, now), revealLab(second, false, now)})
	assert.False(t, ok, "the whole cohort unreserved: all or nothing")

	_, ok = lateTeamToDecline([]eventLabModel.Lab{revealLab(first, true, now), revealLab(second, true, now)})
	assert.False(t, ok, "everything reserved: nobody to decline")

	team, ok := lateTeamToDecline([]eventLabModel.Lab{
		revealLab(first, true, now), revealLab(second, true, now),
		revealLab(late, false, now.Add(time.Minute)), revealLab(later, false, now.Add(2*time.Minute)),
	})
	assert.True(t, ok)
	assert.Equal(t, later, team, "the newest late team first")
}
