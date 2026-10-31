package event

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
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

type fakeDeclines struct{ declined []uuid.UUID }

func (f *fakeDeclines) Decline(_ context.Context, _, _, team uuid.UUID, _ string, _ time.Time) error {
	f.declined = append(f.declined, team)
	return nil
}

// Only a shortage of room, with a cohort already reserved, keeps the newest late team out; other refusals say nothing
// about the newcomer (the snapshot quota, no persistence, a plain not-enough-reserved) and never eject a team.
func TestTrimLateTeamOnlyForTheRoomShortage(t *testing.T) {
	first, late := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	labs := []eventLabModel.Lab{revealLab(first, true, now), revealLab(late, false, now.Add(time.Minute))}
	room := fmt.Errorf("%w: %w", reservationShortage{}, errCohortRoomShort)

	for name, cause := range map[string]error{
		"snapshot quota":       snapshotQuotaShort(2<<30, 1<<30),
		"no persistence":       errPersistenceUnavailable,
		"plain not enough":     calModel.ErrNotEnoughReserved.Err(),
		"room shortage no tag": reservationShortage{},
	} {
		store := &fakeDeclines{}
		trimmed, err := trimLateTeam(context.Background(), store, uuid.Nil, uuid.Nil, labs, cause, now)
		assert.NoError(t, err, name)
		assert.False(t, trimmed, name)
		assert.Empty(t, store.declined, name)
	}

	store := &fakeDeclines{}
	trimmed, err := trimLateTeam(context.Background(), store, uuid.Nil, uuid.Nil, labs, room, now)
	assert.NoError(t, err)
	assert.True(t, trimmed)
	assert.Equal(t, []uuid.UUID{late}, store.declined)

	store = &fakeDeclines{}
	trimmed, _ = trimLateTeam(context.Background(), store, uuid.Nil, uuid.Nil, []eventLabModel.Lab{revealLab(first, false, now), revealLab(late, false, now)}, room, now)
	assert.False(t, trimmed, "nothing is reserved yet: the first reservation is all or nothing")

	trimmed, err = trimLateTeam(context.Background(), nil, uuid.Nil, uuid.Nil, labs, room, now)
	assert.NoError(t, err)
	assert.False(t, trimmed, "declines are off")
}
