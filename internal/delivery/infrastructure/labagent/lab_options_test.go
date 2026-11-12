package labagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

func (f *fakeAgent) RunChecks(_ context.Context, in *labpb.RunChecksRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.runChecks = in
	if f.runChecksErr != nil {
		return nil, f.runChecksErr
	}
	out := &labpb.BatchResult{}
	for _, item := range in.GetItems() {
		out.Results = append(out.Results, f.result(&labpb.ItemRef{LabGroup: item.GetLabGroup(), Name: item.GetLab()}))
	}
	return out, nil
}

// labNeedingEverything is a lab with a bootstrap list, a terminal and one check.
func labNeedingEverything() (exerciseModel.Topology, infraModel.LabMeta) {
	id := uuid.Must(uuid.NewV7())
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{{
		ID: id, Name: "r1", Type: exerciseModel.DeviceTypeNode, Image: "frr", Command: []string{"/start"},
		Access:    []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal}},
		Bootstrap: exerciseModel.Bootstrap{Script: "true", Files: []exerciseModel.BootstrapFile{{Path: "/a", Content: "12345"}}},
	}}}
	meta := infraModel.LabMeta{Checks: []exerciseModel.LabCheck{{
		ID: "t:1", TaskKey: "t", Device: "r1", Mode: exerciseModel.StateModeOnce, TimeLimitSeconds: 30,
		Parts: []exerciseModel.LabCheckPart{{Condition: exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/a"}}}},
	}}}
	return topology, meta
}

func allOptions() *infraModel.AgentFeatures {
	return &infraModel.AgentFeatures{
		Checks:    &infraModel.ChecksFeature{Enabled: true, MaxChecksPerLab: 64, MaxPartsPerCheck: 8, MaxTimeLimitSeconds: 600, MaxScheduleSeconds: 86400},
		Access:    &infraModel.AccessFeature{HTTP: true, Terminal: true},
		Bootstrap: &infraModel.BootstrapFeature{MaxScriptBytes: 65536, MaxFiles: 32, MaxFileBytes: 262144, MaxTotalBytes: 524288, MaxTimeLimitSeconds: 3600, MaxAttempts: 3},
	}
}

// A task that needs an option the laboratory does not have is refused with the option named, and nothing is created.
func TestFleetRefusesALabThatNeedsAnOptionTheAgentLacks(t *testing.T) {
	ctx := context.Background()
	topology, meta := labNeedingEverything()

	for name, tc := range map[string]struct {
		features *infraModel.AgentFeatures
		options  string
	}{
		"checks off":   {features: func() *infraModel.AgentFeatures { f := allOptions(); f.Checks.Enabled = false; return f }(), options: "checks"},
		"terminal off": {features: func() *infraModel.AgentFeatures { f := allOptions(); f.Access.Terminal = false; return f }(), options: "terminal"},
		"older agent":  {features: &infraModel.AgentFeatures{}, options: "checks; terminal; bootstrap"},
		"too many files": {features: func() *infraModel.AgentFeatures {
			f := allOptions()
			f.Bootstrap.MaxFiles = 0
			f.Bootstrap.MaxFileBytes = 4
			return f
		}(), options: "bootstrap.max_file_bytes (at most 4)"},
		"check too long":  {features: func() *infraModel.AgentFeatures { f := allOptions(); f.Checks.MaxTimeLimitSeconds = 10; return f }(), options: "checks.max_time_limit_seconds (at most 10)"},
		"total too large": {features: func() *infraModel.AgentFeatures { f := allOptions(); f.Bootstrap.MaxTotalBytes = 8; return f }(), options: "bootstrap.max_total_bytes (at most 8)"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFleetFixture(t)
			f.am.Features = NewFeatureCell(tc.features)
			err := f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", meta, topology)
			require.ErrorIs(t, err, exerciseModel.ErrLabOptionUnavailable.Err())
			require.Contains(t, err.Error(), tc.options, "the error names what is missing")
			require.Nil(t, f.a.createLabs, "nothing is created")
		})
	}

	t.Run("everything offered", func(t *testing.T) {
		f := newFleetFixture(t)
		f.am.Features = NewFeatureCell(allOptions())
		require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", meta, topology))
		item := f.a.createLabs.GetItems()[0]
		require.Len(t, item.GetBootstrap(), 1)
		require.Equal(t, "r1", item.GetBootstrap()[0].GetDevice())
		require.Len(t, item.GetChecks().GetItems(), 1)
	})

	t.Run("not reported yet: the agent refuses on its own", func(t *testing.T) {
		f := newFleetFixture(t)
		require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", meta, topology))
	})

	t.Run("an option switched on after the start is seen at the refusal", func(t *testing.T) {
		f := newFleetFixture(t)
		f.am.Features = NewFeatureCell(&infraModel.AgentFeatures{})
		f.a.features = &labpb.FeaturesResponse{
			Checks: &labpb.ChecksFeature{Enabled: true}, Access: &labpb.AccessFeature{Http: true, Terminal: true}, Bootstrap: &labpb.BootstrapFeature{},
		}
		require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", meta, topology))
	})

	t.Run("a plain lab needs none of them", func(t *testing.T) {
		f := newFleetFixture(t)
		f.am.Features = NewFeatureCell(&infraModel.AgentFeatures{})
		plain := exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeNode, Image: "nginx", Bootstrap: exerciseModel.Bootstrap{Env: []exerciseModel.EnvVar{{Name: "A", Value: "1"}}}}}}
		require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, plain))
	})
}

func TestFeaturesOfReadsTheOptionsOfContract15(t *testing.T) {
	got := FeaturesOf(&labpb.FeaturesResponse{
		Checks:    &labpb.ChecksFeature{Enabled: true, MaxChecksPerLab: 64, MaxPartsPerCheck: 8, MaxTimeLimitSeconds: 600, MaxScheduleSeconds: 86400, MaxDetailBytes: 2048, Observations: []string{"file", "custom", "directory"}},
		Access:    &labpb.AccessFeature{Http: true, Terminal: true, TerminalHost: "shell"},
		Bootstrap: &labpb.BootstrapFeature{MaxScriptBytes: 1, MaxFiles: 2, MaxFileBytes: 3, MaxTotalBytes: 4, MaxTimeLimitSeconds: 5, MaxAttempts: 6},
	})
	require.Equal(t, &infraModel.ChecksFeature{Enabled: true, MaxChecksPerLab: 64, MaxPartsPerCheck: 8, MaxTimeLimitSeconds: 600, MaxScheduleSeconds: 86400, MaxDetailBytes: 2048, Observations: []string{"file", "custom", "directory"}}, got.Checks)
	require.Equal(t, &infraModel.AccessFeature{HTTP: true, Terminal: true, TerminalHost: "shell"}, got.Access)
	require.Equal(t, &infraModel.BootstrapFeature{MaxScriptBytes: 1, MaxFiles: 2, MaxFileBytes: 3, MaxTotalBytes: 4, MaxTimeLimitSeconds: 5, MaxAttempts: 6}, got.Bootstrap)

	old := FeaturesOf(&labpb.FeaturesResponse{})
	require.Nil(t, old.Checks)
	require.Nil(t, old.Access)
	require.Nil(t, old.Bootstrap, "an agent older than the fields reports none")
}

func TestRunLabChecksAsksTheAgentForOneLab(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	require.NoError(t, f.fleet.EnsureVPNGroup(ctx, "e-1-t-1"))

	require.NoError(t, f.fleet.RunLabChecks(ctx, "e-1-t-1", "lab-1", []string{"ospf:1", "ospf:2"}))
	item := f.a.runChecks.GetItems()[0]
	require.Equal(t, "e-1-t-1", item.GetLabGroup())
	require.Equal(t, "lab-1", item.GetLab())
	require.Equal(t, []string{"ospf:1", "ospf:2"}, item.GetCheckIds())

	// A laboratory with the checks option off refuses the call as a whole.
	f.a.runChecksErr = status.Error(codes.FailedPrecondition, "checks are off")
	err := f.fleet.RunLabChecks(ctx, "e-1-t-1", "lab-1", nil)
	require.ErrorIs(t, err, exerciseModel.ErrLabOptionUnavailable.Err())
	require.Contains(t, err.Error(), "checks")

	// The cooldown of the laboratory between manual runs is our "asked too often" with the wait it names.
	f.a.runChecksErr = status.Error(codes.ResourceExhausted, "wait 7 seconds before the next manual run of this lab")
	err = f.fleet.RunLabChecks(ctx, "e-1-t-1", "lab-1", nil)
	require.ErrorIs(t, err, exerciseModel.ErrChecksRunTooOften.Err())
	wait, ok := cooldownSeconds("wait 7 seconds before the next manual run of this lab")
	require.True(t, ok)
	require.EqualValues(t, 7, wait)
	_, ok = cooldownSeconds("something else")
	require.False(t, ok)

	// A lab the agent does not know.
	f.a.runChecksErr = nil
	f.a.itemState = labpb.ItemState_ITEM_STATE_NOT_FOUND
	err = f.fleet.RunLabChecks(ctx, "e-1-t-1", "gone", nil)
	require.ErrorIs(t, err, errNotFound)

	// A refusal of the lab itself (no manual run allowed) is an error.
	f.a.itemState = labpb.ItemState_ITEM_STATE_FAILED
	f.a.itemError = "check ospf:1 does not allow a manual run"
	require.Error(t, f.fleet.RunLabChecks(ctx, "e-1-t-1", "lab-1", []string{"ospf:1"}))
}
