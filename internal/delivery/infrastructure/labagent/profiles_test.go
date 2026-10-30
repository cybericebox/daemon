package labagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func nodeOf(profile exerciseModel.NodeProfile) exerciseModel.Topology {
	return exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "box", Type: exerciseModel.DeviceTypeNode, Profile: profile}}}
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
	assert.Equal(t, map[exerciseModel.NodeProfile]ProfileCapacity{exerciseModel.NodeProfileContainer: {AvailableNow: true}}, got)
}

func TestProfileCapacity_Runnable(t *testing.T) {
	assert.True(t, ProfileCapacity{AvailableNow: true}.Runnable())
	assert.True(t, ProfileCapacity{ScaleUpKnown: true, AvailableAfterScaleUp: true}.Runnable())
	assert.False(t, ProfileCapacity{AvailableAfterScaleUp: true}.Runnable(), "scale-up counts only when the agent knows it")
	assert.False(t, ProfileCapacity{}.Runnable())
}

func TestRequireProfiles(t *testing.T) {
	ready := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
		exerciseModel.NodeProfileMicroVM:   {AvailableNow: true},
	}
	container := map[exerciseModel.NodeProfile]ProfileCapacity{exerciseModel.NodeProfileContainer: {AvailableNow: true}}

	require.NoError(t, requireProfiles(memberWith(true, container), nodeOf("")))
	require.NoError(t, requireProfiles(memberWith(true, ready), nodeOf(exerciseModel.NodeProfileMicroVM)))
	require.NoError(t, requireProfiles(memberWith(true, nil), nodeOf(exerciseModel.NodeProfileMicroVM)), "no report yet: the agent answers itself")
	require.ErrorIs(t, requireProfiles(memberWith(true, container), nodeOf(exerciseModel.NodeProfileMicroVM)), exerciseModel.ErrNodeProfileUnavailable.Err())
	require.ErrorIs(t, requireProfiles(memberWith(true, ready), nodeOf(exerciseModel.NodeProfileVM)), exerciseModel.ErrNodeProfileVMUnsupported.Err())
}

func TestFleet_RunnableNodeProfilesAndTheEarlyRefusal(t *testing.T) {
	container := map[exerciseModel.NodeProfile]ProfileCapacity{exerciseModel.NodeProfileContainer: {AvailableNow: true}}
	microvm := map[exerciseModel.NodeProfile]ProfileCapacity{
		exerciseModel.NodeProfileContainer: {AvailableNow: true},
		exerciseModel.NodeProfileMicroVM:   {AvailableNow: true},
	}
	disabled := memberWith(false, microvm)
	fleet := NewFleet(nil, nil, memberWith(true, container), disabled)

	assert.Equal(t, map[exerciseModel.NodeProfile]bool{exerciseModel.NodeProfileContainer: true}, fleet.RunnableNodeProfiles(context.Background()))
	require.ErrorIs(t, fleet.requireProfilesSomewhere(nodeOf(exerciseModel.NodeProfileMicroVM)), exerciseModel.ErrNodeProfileUnavailable.Err(), "a disabled agent does not count")

	fleet = NewFleet(nil, nil, memberWith(true, container), memberWith(true, microvm))
	assert.True(t, fleet.RunnableNodeProfiles(context.Background())[exerciseModel.NodeProfileMicroVM])
	require.NoError(t, fleet.requireProfilesSomewhere(nodeOf(exerciseModel.NodeProfileMicroVM)))
}

// The agent is sent the profile only when it is not the container, and the container stays the agent's type.
func TestBuildDevice_Profile(t *testing.T) {
	web := buildDevice(exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileContainer}, "web")
	assert.Equal(t, "container", web.Type)
	assert.Empty(t, web.Profile)
	box := buildDevice(exerciseModel.Device{Name: "box", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileMicroVM}, "box")
	assert.Equal(t, "container", box.Type)
	assert.Equal(t, "microvm", box.Profile)
	sw := buildDevice(exerciseModel.Device{Name: "sw", Type: exerciseModel.DeviceTypeHub}, "sw-1")
	assert.Equal(t, "hub", sw.Type)
	assert.Empty(t, sw.Profile)
}
