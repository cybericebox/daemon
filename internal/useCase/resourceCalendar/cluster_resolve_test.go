package resourceCalendarUseCase

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

func TestResolveSyncsMissingPartOnce(t *testing.T) {
	h, port := syncHarness(t)
	port.down[agentOne] = assert.AnError
	res := setEvent(t, h, EventReservationInput{})
	team := uuid.Must(uuid.NewV7())
	ref := infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.TeamUnit(team)}
	// The part is not on its agent and the agent does not answer: the group waits.
	_, _, err := h.uc.Resolve(context.Background(), ref, nil)
	assert.ErrorIs(t, err, infraModel.ErrNoClusterReservation)
	// The agent is back: the resolve sends the part once and answers it.
	delete(port.down, agentOne)
	calls := len(port.calls)
	agent, part, err := h.uc.Resolve(context.Background(), ref, nil)
	require.NoError(t, err)
	assert.Equal(t, agentOne, agent, "the first team goes to the first share")
	assert.Equal(t, partsOf(t, h, res.Reservation.ID)[agentOne].ID, part)
	assert.Contains(t, port.calls[calls:], "create "+part.String())
	// Resolved again: no call, the same agent.
	calls = len(port.calls)
	again, samePart, err := h.uc.Resolve(context.Background(), ref, nil)
	require.NoError(t, err)
	assert.Equal(t, agent, again)
	assert.Equal(t, part, samePart)
	assert.Len(t, port.calls, calls)
}

func TestUnitsFillTheShares(t *testing.T) {
	h, _ := syncHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	byAgent := map[uuid.UUID]int{}
	for i := 0; i < 12; i++ {
		agent, _, err := h.uc.Resolve(context.Background(), infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.TeamUnit(uuid.UUID{0: byte(i + 1)})}, nil)
		require.NoError(t, err)
		byAgent[agent]++
	}
	for _, sh := range res.Reservation.Placement {
		assert.Equal(t, sh.Units, byAgent[sh.AgentID])
	}
	_, _, err := h.uc.Resolve(context.Background(), infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.TeamUnit(uuid.UUID{0: 99})}, nil)
	assert.ErrorIs(t, err, infraModel.ErrNoClusterReservation, "a team beyond the shares has no place")
}

func TestPreviewGroupUnderEventPart(t *testing.T) {
	h, _ := syncHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	owner := uuid.Must(uuid.NewV7())
	agent, part, err := h.uc.Resolve(context.Background(), infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.PreviewUnit(owner)}, nil)
	require.NoError(t, err)
	assert.Equal(t, res.Reservation.Placement[0].AgentID, agent)
	assert.Equal(t, partsOf(t, h, res.Reservation.ID)[agent].ID, part, "a preview runs under the event part of its agent")
}

func TestPinnedGroupKeepsItsAgent(t *testing.T) {
	h, _ := syncHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	ref := infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.TeamUnit(uuid.Must(uuid.NewV7()))}
	pinned := agentTwo
	agent, part, err := h.uc.Resolve(context.Background(), ref, &pinned)
	require.NoError(t, err)
	assert.Equal(t, agentTwo, agent, "a group that lives on an agent never moves")
	assert.Equal(t, partsOf(t, h, res.Reservation.ID)[agentTwo].ID, part)
	units, _ := h.store.Units(context.Background(), res.Reservation.ID)
	assert.Equal(t, agentTwo, units[ref.Unit])
}

func TestTestLabGroupUnderGroupPart(t *testing.T) {
	h, port := syncHarness(t)
	owner, deploy := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w := calModel.Window{Start: now0, End: now0.Add(time.Hour)}
	group := calModel.NewPart(uuid.Must(uuid.NewV7()), agentTwo, calModel.PartTestGroup, w, Amount{CPUMillicores: 100, MemoryBytes: 100}, now0)
	group.OwnerID = &owner
	run := calModel.NewPart(uuid.Must(uuid.NewV7()), agentTwo, calModel.PartTestRun, w, Amount{CPUMillicores: 500, MemoryBytes: 500}, now0)
	run.OwnerID, run.TestDeployID = &owner, &deploy
	require.NoError(t, h.store.SavePart(context.Background(), group))
	require.NoError(t, h.store.SavePart(context.Background(), run))

	agent, part, err := h.uc.Resolve(context.Background(), infraModel.ReservationRef{OwnerID: &owner}, nil)
	require.NoError(t, err)
	assert.Equal(t, agentTwo, agent)
	assert.Equal(t, group.ID, part, "the author's group runs under the group part")
	assert.Contains(t, port.held[agentTwo], group.ID, "a part not on its agent is sent first")
	_, part, err = h.uc.Resolve(context.Background(), infraModel.ReservationRef{TestDeployID: &deploy}, &agent)
	require.NoError(t, err)
	assert.Equal(t, run.ID, part, "the lab runs under its run part")
	other := agentOne
	_, _, err = h.uc.Resolve(context.Background(), infraModel.ReservationRef{TestDeployID: &deploy}, &other)
	assert.ErrorIs(t, err, infraModel.ErrPlacementMismatch)
}
