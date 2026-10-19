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

var testLab = Amount{CPUMillicores: 2000, MemoryBytes: 4 << 30}

func admit(t *testing.T, h *harness, owner uuid.UUID, lease time.Duration, pinned *uuid.UUID) (uuid.UUID, TestLabRoom) {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	room, err := h.uc.AdmitTestLab(context.Background(), TestLabRequest{ID: id, Owner: owner, Size: testLab, Lease: lease, PinnedAgent: pinned})
	require.NoError(t, err)
	return id, room
}

func groupPartOf(t *testing.T, h *harness, owner uuid.UUID) (calModel.Part, bool) {
	t.Helper()
	p, err := h.store.GroupPart(context.Background(), owner)
	if err != nil {
		return calModel.Part{}, false
	}
	return p, true
}

func TestRunPartCreatedOnAdmit(t *testing.T) {
	h, port := syncHarness(t)
	owner := uuid.Must(uuid.NewV7())
	id, room := admit(t, h, owner, 45*time.Minute, nil)
	run, err := h.store.RunPart(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, room.PartID, run.ID)
	assert.Equal(t, room.AgentID, run.AgentID)
	assert.Equal(t, testLab, run.Amount, "the run part is the lab without the group's pods")
	assert.Equal(t, now0, run.Window.Start)
	assert.Equal(t, now0.Add(45*time.Minute), run.Window.End, "the chosen length is the window")
	group, ok := groupPartOf(t, h, owner)
	require.True(t, ok)
	assert.Equal(t, fallbackGroupOverhead, group.Amount)
	assert.Equal(t, run.AgentID, group.AgentID)
	assert.Contains(t, port.held[run.AgentID], run.ID, "the parts are sent at once")
	assert.Contains(t, port.held[run.AgentID], group.ID)
}

func TestGroupPartCoversAllRuns(t *testing.T) {
	h, _ := syncHarness(t)
	owner := uuid.Must(uuid.NewV7())
	_, first := admit(t, h, owner, 30*time.Minute, nil)
	h.clock = now0.Add(10 * time.Minute)
	agent := first.AgentID
	admit(t, h, owner, time.Hour, &agent)
	group, ok := groupPartOf(t, h, owner)
	require.True(t, ok)
	assert.Equal(t, now0, group.Window.Start)
	assert.Equal(t, now0.Add(70*time.Minute), group.Window.End, "from the first start to the latest end")
}

func TestPinnedAgentForSecondRun(t *testing.T) {
	h, _ := syncHarness(t)
	owner := uuid.Must(uuid.NewV7())
	pinned := agentTwo
	_, room := admit(t, h, owner, time.Hour, &pinned)
	assert.Equal(t, agentTwo, room.AgentID, "a run goes to the agent of the author's group")
	group, _ := groupPartOf(t, h, owner)
	assert.Equal(t, agentTwo, group.AgentID)
}

func TestExtendKeepsPartID(t *testing.T) {
	h, port := syncHarness(t)
	owner := uuid.Must(uuid.NewV7())
	id, room := admit(t, h, owner, 30*time.Minute, nil)
	until := now0.Add(90 * time.Minute)
	require.NoError(t, h.uc.ExtendTestLab(context.Background(), id, owner, Amount{}, until))
	run, err := h.store.RunPart(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, room.PartID, run.ID, "extension keeps the part id")
	assert.Equal(t, until, run.Window.End)
	assert.Equal(t, until, port.held[run.AgentID][run.ID].To, "the agent gets the new end (an Update, same id)")
	group, _ := groupPartOf(t, h, owner)
	assert.Equal(t, until, group.Window.End)
}

func TestLastRunRetiresGroupPart(t *testing.T) {
	h, port := syncHarness(t)
	owner := uuid.Must(uuid.NewV7())
	first, room := admit(t, h, owner, time.Hour, nil)
	agent := room.AgentID
	second, _ := admit(t, h, owner, 30*time.Minute, &agent)
	require.NoError(t, h.uc.ReleaseTestLab(context.Background(), first))
	group, ok := groupPartOf(t, h, owner)
	require.True(t, ok)
	assert.Equal(t, now0.Add(30*time.Minute), group.Window.End, "the group part shrinks to the runs that are left")
	assert.NotContains(t, port.held[agent], room.PartID, "the ended run's part is deleted")
	require.NoError(t, h.uc.ReleaseTestLab(context.Background(), second))
	_, ok = groupPartOf(t, h, owner)
	assert.False(t, ok, "the last run retires the group part")
	assert.Empty(t, port.held[agent])
}

func TestBookingRunUsesBookingPart(t *testing.T) {
	h, port := syncHarness(t)
	owner := uuid.Must(uuid.NewV7())
	b, err := h.uc.BookTestLab(context.Background(), owner, now0, 2*time.Hour, testLab, Amount{})
	require.NoError(t, err)
	booking, err := h.store.PartsOfCalendar(context.Background(), b.ID)
	require.NoError(t, err)
	require.Len(t, booking, 1, "a booking is one part")
	assert.Contains(t, port.held[booking[0].AgentID], booking[0].ID)

	id, room := admit(t, h, owner, time.Hour, nil)
	assert.Equal(t, calModel.ViaBooking, room.Via)
	assert.Equal(t, uuid.Nil, room.PartID, "a run inside a booking makes no run part")
	assert.Equal(t, booking[0].AgentID, room.AgentID)
	_, err = h.store.RunPart(context.Background(), id)
	assert.Error(t, err)
	agent, part, err := h.uc.Resolve(context.Background(), infraModel.ReservationRef{TestDeployID: &id}, nil)
	require.NoError(t, err)
	assert.Equal(t, booking[0].ID, part, "the lab runs under the booking's part")
	assert.Equal(t, booking[0].AgentID, agent)
	group, ok := groupPartOf(t, h, owner)
	require.True(t, ok, "the author's group still has its own part")
	assert.Equal(t, booking[0].AgentID, group.AgentID)
}
