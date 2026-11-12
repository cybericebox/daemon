package labagent

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

func nodeOf(profile exerciseModel.NodeProfile) exerciseModel.Topology {
	return exerciseModel.Topology{
		Devices: []exerciseModel.Device{
			{Name: "box", Type: exerciseModel.DeviceTypeNode, Profile: profile},
		},
	}
}

func memberWith(enabled bool, reports map[exerciseModel.NodeProfile]ProfileCapacity) *Member {
	cell := NewProfileCell()
	if reports != nil {
		cell.Set(reports)
	}
	return &Member{Enabled: enabled, Profiles: cell}
}

// An agent that reports no profile list is an older one: it offers the container.
func TestProfilesOf_OlderAgentOffersTheContainer(t *testing.T) {
	got := ProfilesOf(&labpb.CapacityResponse{})
	assert.Equal(
		t,
		map[exerciseModel.NodeProfile]ProfileCapacity{
			exerciseModel.NodeProfileContainer: {AvailableNow: true},
		},
		got,
	)
}

func TestProfileCapacity_Runnable(t *testing.T) {
	assert.True(t, ProfileCapacity{AvailableNow: true}.Runnable())
	assert.True(t, ProfileCapacity{ScaleUpKnown: true, AvailableAfterScaleUp: true}.Runnable())
	assert.False(
		t,
		ProfileCapacity{AvailableAfterScaleUp: true}.Runnable(),
		"scale-up counts only when the agent knows it",
	)
	assert.False(t, ProfileCapacity{}.Runnable())
}

func TestRequireProfiles(t *testing.T) {
	ready := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
		exerciseModel.NodeProfileMicroVM:   {AvailableNow: true},
	}
	container := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
	}

	require.NoError(t, requireProfiles(memberWith(true, container), nodeOf("")))
	require.NoError(
		t,
		requireProfiles(memberWith(true, ready), nodeOf(exerciseModel.NodeProfileMicroVM)),
	)
	require.NoError(
		t,
		requireProfiles(memberWith(true, nil), nodeOf(exerciseModel.NodeProfileMicroVM)),
		"no report yet: the agent answers itself",
	)
	require.ErrorIs(
		t,
		requireProfiles(memberWith(true, container), nodeOf(exerciseModel.NodeProfileMicroVM)),
		exerciseModel.ErrNodeProfileUnavailable.Err(),
	)
	require.ErrorIs(
		t,
		requireProfiles(memberWith(true, ready), nodeOf(exerciseModel.NodeProfileVM)),
		exerciseModel.ErrNodeProfileVMUnsupported.Err(),
	)
}

func TestFleet_RunnableNodeProfilesAndTheEarlyRefusal(t *testing.T) {
	container := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
	}
	microvm := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
		exerciseModel.NodeProfileMicroVM:   {AvailableNow: true},
	}
	disabled := memberWith(false, microvm)
	fleet := NewFleet(nil, nil, memberWith(true, container), disabled)

	assert.Equal(
		t,
		map[exerciseModel.NodeProfile]bool{exerciseModel.NodeProfileContainer: true},
		fleet.RunnableNodeProfiles(context.Background()),
	)
	require.ErrorIs(
		t,
		fleet.requireProfilesSomewhere(nodeOf(exerciseModel.NodeProfileMicroVM)),
		exerciseModel.ErrNodeProfileUnavailable.Err(),
		"a disabled agent does not count",
	)

	fleet = NewFleet(nil, nil, memberWith(true, container), memberWith(true, microvm))
	assert.True(
		t,
		fleet.RunnableNodeProfiles(context.Background())[exerciseModel.NodeProfileMicroVM],
	)
	require.NoError(t, fleet.requireProfilesSomewhere(nodeOf(exerciseModel.NodeProfileMicroVM)))
}

// The agent is sent the profile only when it is not the container, and the container stays the agent's type.
func TestBuildDevice_Profile(t *testing.T) {
	web := buildDevice(
		exerciseModel.Device{
			Name:    "web",
			Type:    exerciseModel.DeviceTypeNode,
			Profile: exerciseModel.NodeProfileContainer,
		},
		"web",
	)
	assert.Equal(t, "container", web.Type)
	assert.Empty(t, web.Profile)
	box := buildDevice(
		exerciseModel.Device{
			Name:    "box",
			Type:    exerciseModel.DeviceTypeNode,
			Profile: exerciseModel.NodeProfileMicroVM,
		},
		"box",
	)
	assert.Equal(t, "container", box.Type)
	assert.Equal(t, "microvm", box.Profile)
	sw := buildDevice(exerciseModel.Device{Name: "sw", Type: exerciseModel.DeviceTypeHub}, "sw-1")
	assert.Equal(t, "hub", sw.Type)
	assert.Empty(t, sw.Profile)
}

func profileSet(profiles ...exerciseModel.NodeProfile) exerciseModel.NodeProfileSet {
	var set exerciseModel.NodeProfileSet
	for _, p := range profiles {
		set = set.Add(p)
	}
	return set
}

// A new group goes to an agent that can run every profile of the event's tasks, even when another agent has the
// higher priority; with no such agent nothing is created and the reason is the profile.
func TestFleetPlacesANewGroupOnAnAgentThatRunsTheProfiles(t *testing.T) {
	f := newFleetFixture(t)
	f.am.Profiles = NewProfileCell() // a: highest priority, container only
	f.am.Profiles.Set(
		map[exerciseModel.NodeProfile]ProfileCapacity{
			exerciseModel.NodeProfileContainer: {AvailableNow: true},
		},
	)
	f.bm.Profiles = NewProfileCell()
	f.bm.Profiles.Set(map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {
			AvailableNow: true,
		},
		exerciseModel.NodeProfileMicroVM: {AvailableNow: true},
	})
	ctx := infraModel.WithPlacementNeed(
		context.Background(),
		infraModel.PlacementNeed{Profiles: profileSet(exerciseModel.NodeProfileMicroVM)},
	)
	require.NoError(t, f.fleet.EnsureVPNGroup(ctx, "e-1-t-1"))
	assert.Equal(t, f.bm.ID, f.store.groups["e-1-t-1"], "a cannot run microvm")
	assert.NotNil(t, f.b.createGroup)

	// Without a microvm task the priority decides, as before.
	require.NoError(t, f.fleet.EnsureVPNGroup(context.Background(), "e-1-t-2"))
	assert.Equal(t, f.am.ID, f.store.groups["e-1-t-2"])
}

func TestFleetRefusesANewGroupWhenNoAgentRunsTheProfiles(t *testing.T) {
	f := newFleetFixture(t)
	for _, m := range []*Member{f.am, f.bm} {
		m.Profiles = NewProfileCell()
		m.Profiles.Set(
			map[exerciseModel.NodeProfile]ProfileCapacity{
				exerciseModel.NodeProfileContainer: {AvailableNow: true},
			},
		)
	}
	ctx := infraModel.WithPlacementNeed(
		context.Background(),
		infraModel.PlacementNeed{Profiles: profileSet(exerciseModel.NodeProfileMicroVM)},
	)
	err := f.fleet.EnsureVPNGroup(ctx, "e-1-t-1")
	require.ErrorIs(t, err, exerciseModel.ErrNodeProfileUnavailable.Err())
	assert.Nil(t, f.a.createGroup)
	assert.Nil(t, f.b.createGroup)
	assert.Empty(t, f.store.groups, "nothing is placed")

	ctx = infraModel.WithPlacementNeed(
		context.Background(),
		infraModel.PlacementNeed{Profiles: profileSet(exerciseModel.NodeProfileVM)},
	)
	require.ErrorIs(
		t,
		f.fleet.EnsureVPNGroup(ctx, "e-1-t-2"),
		exerciseModel.ErrNodeProfileVMUnsupported.Err(),
	)
}

// A group that lives on an agent stays; a lab of a task added later with a profile that agent cannot run is refused
// and the group does not move.
func TestFleetDoesNotMoveAnExistingGroupForANewProfile(t *testing.T) {
	f := newFleetFixture(t)
	f.am.Profiles = NewProfileCell()
	f.am.Profiles.Set(
		map[exerciseModel.NodeProfile]ProfileCapacity{
			exerciseModel.NodeProfileContainer: {AvailableNow: true},
		},
	)
	f.bm.Profiles = NewProfileCell()
	f.bm.Profiles.Set(
		map[exerciseModel.NodeProfile]ProfileCapacity{
			exerciseModel.NodeProfileContainer: {AvailableNow: true},
			exerciseModel.NodeProfileMicroVM:   {AvailableNow: true},
		},
	)
	ctx := context.Background()
	require.NoError(t, f.fleet.EnsureVPNGroup(ctx, "e-1-t-1"))
	require.Equal(t, f.am.ID, f.store.groups["e-1-t-1"])

	err := f.fleet.DeployLab(
		ctx,
		"e-1-t-1",
		"c-2",
		infraModel.LabMeta{},
		nodeOf(exerciseModel.NodeProfileMicroVM),
	)
	require.ErrorIs(t, err, exerciseModel.ErrNodeProfileUnavailable.Err())
	assert.Equal(t, f.am.ID, f.store.groups["e-1-t-1"], "the group stays")
	assert.Nil(t, f.b.createLabs)
	assert.Nil(t, f.a.createLabs)
}

func TestProfilesEncodeDecodeRoundTrip(t *testing.T) {
	in := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
		exerciseModel.NodeProfileMicroVM: {
			ScaleUpKnown: true, AvailableAfterScaleUp: true,
			Overhead: resourcesModel.Amount{CPUMillicores: 50, MemoryBytes: 120 << 20},
		},
	}
	out, err := DecodeProfiles(EncodeProfiles(in))
	require.NoError(t, err)
	assert.Equal(t, in, out)
	_, err = DecodeProfiles([]byte(`{`))
	require.Error(t, err)
	out, err = DecodeProfiles([]byte(`[{"profile":"wasm","available_now":true}]`))
	require.NoError(t, err)
	assert.Empty(t, out, "a profile this build does not know is dropped")
}

// A report is taken only when it is not older than the one held; the dependants hear about taken reports only.
func TestProfileCells_NewerReportWinsAndStoredOnesApplyAtStart(t *testing.T) {
	var heard int
	cells := NewProfileCells(
		func(uuid.UUID, map[exerciseModel.NodeProfile]ProfileCapacity) { heard++ },
	)
	agent := uuid.Must(uuid.NewV7())
	now := time.Now()
	microvm := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileMicroVM: {AvailableNow: true},
	}
	none := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
	}

	cells.Update(agent, microvm, now)
	cells.Update(
		agent,
		none,
		now.Add(-time.Hour),
	) // a stale stored report does not undo the live one
	capacity, known := cells.Cell(agent).Get(exerciseModel.NodeProfileMicroVM)
	assert.True(t, known && capacity.AvailableNow)
	assert.Equal(t, 1, heard)

	// A restart: the cell is empty and takes the stored report.
	restarted := NewProfileCells(nil)
	restarted.ApplyStored([]infraModel.AgentProfileReport{
		{AgentID: agent, Data: EncodeProfiles(microvm), ReceivedAt: now},
		{AgentID: uuid.Must(uuid.NewV7()), Data: []byte(`not json`), ReceivedAt: now},
	})
	capacity, known = restarted.Cell(agent).Get(exerciseModel.NodeProfileMicroVM)
	assert.True(t, known && capacity.AvailableNow)
	cell := restarted.Cell(agent)
	assert.Same(t, cell, restarted.Cell(agent), "a rebuilt member keeps its cell")
}
