package labagent

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// fakeResolver places every unit on agent, and answers the part of each ref by its unit (or its test ids).
type fakeResolver struct {
	agent  uuid.UUID
	parts  map[string]uuid.UUID
	calls  []*uuid.UUID
	failed error
}

func refKey(ref infraModel.ReservationRef) string {
	switch {
	case ref.TestDeployID != nil:
		return "run:" + ref.TestDeployID.String()
	case ref.OwnerID != nil:
		return "owner:" + ref.OwnerID.String()
	}
	return ref.Unit
}

func (r *fakeResolver) Resolve(_ context.Context, ref infraModel.ReservationRef, pinned *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	r.calls = append(r.calls, pinned)
	if r.failed != nil {
		return uuid.Nil, uuid.Nil, r.failed
	}
	agent := r.agent
	if pinned != nil {
		agent = *pinned
	}
	return agent, r.parts[refKey(ref)], nil
}

func teamRef(unit string) infraModel.ReservationRef {
	event := uuid.Must(uuid.NewV7())
	return infraModel.ReservationRef{EventID: &event, Unit: unit}
}

func TestDeployLabSendsReservationID(t *testing.T) {
	f := newFleetFixture(t)
	deploy, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	runPart, groupPart := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	f.fleet.SetPartResolver(&fakeResolver{agent: f.bm.ID, parts: map[string]uuid.UUID{"run:" + deploy.String(): runPart, "owner:" + owner.String(): groupPart}})
	meta := infraModel.LabMeta{Reservation: infraModel.ReservationRef{TestDeployID: &deploy}, GroupReservation: infraModel.ReservationRef{OwnerID: &owner}}
	require.NoError(t, f.fleet.DeployLab(context.Background(), "tu-1", "l-1", meta, exerciseModel.Topology{}))
	require.NotNil(t, f.b.createGroup, "the group goes to the agent of its reservation, not the first by priority")
	assert.Nil(t, f.a.createGroup)
	assert.Equal(t, groupPart.String(), f.b.createGroup.GetItems()[0].GetReservationId())
	assert.Equal(t, runPart.String(), f.b.createLabs.GetItems()[0].GetReservationId(), "the lab runs under its own part")
}

func TestEnsureVPNGroupSendsReservationID(t *testing.T) {
	f := newFleetFixture(t)
	part := uuid.Must(uuid.NewV7())
	f.fleet.SetPartResolver(&fakeResolver{agent: f.am.ID, parts: map[string]uuid.UUID{"team:1": part}})
	ctx := infraModel.WithGroupReservation(context.Background(), teamRef("team:1"))
	require.NoError(t, f.fleet.EnsureVPNGroup(ctx, "e-1-t-1"))
	assert.Equal(t, part.String(), f.a.createGroup.GetItems()[0].GetReservationId())
}

func TestFleetPlacesByAssignedAgent(t *testing.T) {
	f := newFleetFixture(t)
	f.fleet.SetPartResolver(&fakeResolver{agent: f.bm.ID, parts: map[string]uuid.UUID{}})
	ctx := infraModel.WithGroupReservation(context.Background(), teamRef("team:1"))
	require.NoError(t, f.fleet.EnsureVPNGroup(ctx, "e-1-t-1"))
	assert.Equal(t, f.bm.ID, f.store.groups["e-1-t-1"])
	// A group without a reservation still takes the picker's agent.
	require.NoError(t, f.fleet.EnsureVPNGroup(context.Background(), "legacy"))
	assert.Equal(t, f.am.ID, f.store.groups["legacy"])
	// The unit's agent takes no new groups: the group waits for its reservation.
	f.bm.Enabled = false
	err := f.fleet.EnsureVPNGroup(ctx, "e-1-t-2")
	assert.ErrorIs(t, err, infraModel.ErrNoClusterReservation)
}

func TestFleetKeepsExistingPlacement(t *testing.T) {
	f := newFleetFixture(t)
	f.store.groups = map[string]uuid.UUID{"e-1-t-1": f.am.ID}
	f.a.groups = append(f.a.groups, readyGroup()...)
	r := &fakeResolver{agent: f.bm.ID, parts: map[string]uuid.UUID{"team:1": uuid.Must(uuid.NewV7())}}
	f.fleet.SetPartResolver(r)
	ref := teamRef("team:1")
	require.NoError(t, f.fleet.DeployLab(context.Background(), "e-1-t-1", "c-1", infraModel.LabMeta{Reservation: ref, GroupReservation: ref}, exerciseModel.Topology{}))
	assert.NotNil(t, f.a.createLabs, "the group stays on its agent")
	assert.Nil(t, f.b.createLabs)
	for _, pinned := range r.calls {
		require.NotNil(t, pinned, "a placed group resolves its part on its own agent")
		assert.Equal(t, f.am.ID, *pinned)
	}
}
