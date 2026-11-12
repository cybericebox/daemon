package infrastructure_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// A laboratory that does not offer an observation kind the checks use is refused with the kind named; nothing is dropped.
func TestRequireLabOptionsRefusesObservationsTheCheckerLacks(t *testing.T) {
	needs := exerciseModel.LabNeeds{Checks: true, Observations: []string{"custom", "directory", "file", "file.checksum"}}
	refused := func(f *infraModel.AgentFeatures) string {
		err := f.RequireLabOptions(needs)
		require.ErrorIs(t, err, exerciseModel.ErrLabOptionUnavailable.Err())
		var withContext interface{ PublicContext() map[string]any }
		require.ErrorAs(t, err, &withContext)
		return withContext.PublicContext()["options"].(string)
	}

	// A checker that lists nothing is the first generation: file and custom only, and not the file keys type and checksum.
	first := &infraModel.AgentFeatures{Checks: &infraModel.ChecksFeature{Enabled: true}}
	require.Equal(t, "checks.observation.directory; checks.observation.file.checksum", refused(first))

	// A checker that lists its kinds reads the file keys too (they come with "file"), but runs only what it lists.
	newer := &infraModel.AgentFeatures{Checks: &infraModel.ChecksFeature{
		Enabled: true, Observations: []string{"file", "custom", "process"},
	}}
	require.Equal(t, "checks.observation.directory", refused(newer))

	full := &infraModel.AgentFeatures{Checks: &infraModel.ChecksFeature{
		Enabled: true, Observations: []string{"file", "custom", "directory"},
	}}
	require.NoError(t, full.RequireLabOptions(needs))
	require.NoError(t, first.RequireLabOptions(exerciseModel.LabNeeds{Checks: true, Observations: []string{"custom", "file"}}))
}
