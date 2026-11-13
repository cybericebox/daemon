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

// A check whose time limit is above the laboratory's ceiling is refused with the task and the key named.
func TestRequireLabOptionsNamesTheTaskOfACheckAboveTheCeiling(t *testing.T) {
	features := &infraModel.AgentFeatures{Checks: &infraModel.ChecksFeature{Enabled: true, MaxTimeLimitSeconds: 120}}
	err := features.RequireLabOptions(exerciseModel.LabNeeds{Checks: true, MaxCheckTimeLimit: 300, LongestCheck: "ospf:2"})
	require.ErrorIs(t, err, exerciseModel.ErrLabOptionUnavailable.Err())
	var withContext interface{ PublicContext() map[string]any }
	require.ErrorAs(t, err, &withContext)
	ctx := withContext.PublicContext()
	require.Equal(t, "checks.max_time_limit_seconds (at most 120)", ctx["options"])
	require.Equal(t, "ospf", ctx["task"])
	require.Equal(t, "check.state.conditions[].timeout", ctx["field"])
	require.EqualValues(t, 120, ctx["limit"])
	require.NoError(t, features.RequireLabOptions(exerciseModel.LabNeeds{Checks: true, MaxCheckTimeLimit: 120, LongestCheck: "ospf:2"}))
}

// A laboratory that lets fewer gateways into a lab than the task format refuses the lab with an error that names the limit; a
// laboratory that reports none (0) or enough refuses nothing, and so does one that reports no VPN option at all.
func TestRequireLabOptionsRefusesMoreGatewaysThanTheLaboratoryAccepts(t *testing.T) {
	needs := exerciseModel.LabNeeds{Gateways: 6}
	few := &infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{MaxGateways: 4}}
	err := few.RequireLabOptions(needs)
	require.ErrorIs(t, err, exerciseModel.ErrLabOptionUnavailable.Err())
	var withContext interface{ PublicContext() map[string]any }
	require.ErrorAs(t, err, &withContext)
	require.Equal(t, "gateways (at most 4)", withContext.PublicContext()["options"])

	require.NoError(t, (&infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{MaxGateways: 10}}).RequireLabOptions(needs))
	require.NoError(t, (&infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{}}).RequireLabOptions(needs), "0 is not reported")
	require.NoError(t, (&infraModel.AgentFeatures{}).RequireLabOptions(needs))
	require.NoError(t, few.RequireLabOptions(exerciseModel.LabNeeds{Gateways: 4}), "the limit itself is allowed")
}

func TestNeedsOfCountsTheGateways(t *testing.T) {
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{
		{Name: "web", Type: exerciseModel.DeviceTypeNode},
		{Name: "a", Type: exerciseModel.DeviceTypeVPNGateway},
		{Name: "b", Type: exerciseModel.DeviceTypeInternetGateway},
		{Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
	}}
	require.Equal(t, 2, exerciseModel.NeedsOf(topology, nil).Gateways)
}
