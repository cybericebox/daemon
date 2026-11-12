package exercise_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

type capabilityInfra struct {
	fakeInfra
	caps infraModel.LabCapabilities
}

func (c *capabilityInfra) LabCapabilities() infraModel.LabCapabilities { return c.caps }

// The view is the aggregate of the infrastructure plus the platform's node profiles; without a reporting infrastructure it
// is "unknown" (not "none"), and the container profile is ticked by default.
func TestLabCapabilitiesView(t *testing.T) {
	bare := exercise.NewExerciseUseCase(exercise.Dependencies{}).LabCapabilities(context.Background())
	require.False(t, bare.Known)
	require.Equal(t, infraModel.AvailabilityUnknown, bare.Checks)
	require.Equal(t, map[string]infraModel.Availability{"container": infraModel.AvailabilityAll, "microvm": infraModel.AvailabilityNone, "vm": infraModel.AvailabilityNone}, bare.Profiles)

	infra := &capabilityInfra{caps: infraModel.CapabilitiesOf([]*infraModel.AgentFeatures{{Checks: &infraModel.ChecksFeature{Enabled: true}}})}
	view := exercise.NewExerciseUseCase(exercise.Dependencies{Infra: infra}).LabCapabilities(context.Background())
	require.True(t, view.Known)
	require.Equal(t, infraModel.AvailabilityAll, view.Checks)
	require.Equal(t, infraModel.AvailabilityNone, view.Access["terminal"])
}
