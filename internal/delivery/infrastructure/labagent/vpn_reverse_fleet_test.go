package labagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// The fleet sends the option by what the agent reported, records on the lab what was really requested, and the status says
// what is really in force: blocked, allowed, or not enforced on an agent that cannot block.
func TestFleetVPNReverseEndToEnd(t *testing.T) {
	ctx := context.Background()
	capable := &infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}}
	for name, tc := range map[string]struct {
		features *infraModel.AgentFeatures
		vpn      exerciseModel.NetworkSpec
		sent     bool
		state    string
	}{
		"capable, forbidden": {capable, exerciseModel.NetworkSpec{Enabled: true}, false, "blocked"},
		"capable, allowed":   {capable, exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true}, true, "allowed"},
		"older, forbidden":   {&infraModel.AgentFeatures{}, exerciseModel.NetworkSpec{Enabled: true}, false, "not_enforced"},
		"older, allowed":     {&infraModel.AgentFeatures{}, exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true}, false, "allowed"},
		"vpn off":            {capable, exerciseModel.NetworkSpec{}, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFleetFixture(t)
			f.am.Features = NewFeatureCell(tc.features)
			require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, vpnTopology(tc.vpn)))
			item := f.a.createLabs.GetItems()[0]
			var spec labSpec
			require.NoError(t, json.Unmarshal(f.a.createLabs.GetVariants()[0].GetSpecJson(), &spec))
			require.Equal(t, tc.sent, spec.VPN.ReverseConnections)
			if tc.sent {
				require.Equal(t, "1", item.GetLabels()[infraModel.LabelVPNReverseSent])
			}

			f.a.labs = []*labpb.Lab{{Name: "c-1", Labels: item.GetLabels(), Status: &labpb.LabStatus{}}}
			st, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
			require.NoError(t, err)
			require.Equal(t, tc.state, st.VPNReverse)
		})
	}
}

// Features not received yet and an exercise that allows the connections: no lab is made without the option (a capable agent
// would block them and the task could not be solved). The start waits as for an agent that does not answer, and goes through
// once the features arrive. An exercise that forbids them starts at once: no option is the right request for it.
func TestFleetVPNReverse_FeaturesUnknownAtCreation(t *testing.T) {
	ctx := context.Background()
	allowed := vpnTopology(exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true})
	f := newFleetFixture(t) // no features reported, and the agent cannot be asked
	f.am.Features = NewFeatureCell(nil)

	err := f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, allowed)
	require.Equal(t, codes.Unavailable, status.Code(err), "a transient wait, not a failure of the lab")
	require.Nil(t, f.a.createLabs, "nothing is created")

	// The features arrive (a capable agent): the same start now sends the option and records it.
	f.am.Features.Set(infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}})
	require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, allowed))
	var spec labSpec
	require.NoError(t, json.Unmarshal(f.a.createLabs.GetVariants()[0].GetSpecJson(), &spec))
	require.True(t, spec.VPN.ReverseConnections)
	labels := f.a.createLabs.GetItems()[0].GetLabels()
	require.Equal(t, "1", labels[infraModel.LabelVPNReverse])
	require.Equal(t, "1", labels[infraModel.LabelVPNReverseSent])
	f.a.labs = []*labpb.Lab{{Name: "c-1", Labels: labels, Status: &labpb.LabStatus{}}}
	st, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
	require.NoError(t, err)
	require.Equal(t, "allowed", st.VPNReverse)

	// The features can also be read at the moment of the start: it does not wait for the next poll.
	late := newFleetFixture(t)
	late.am.Features = NewFeatureCell(nil)
	late.a.features = &labpb.FeaturesResponse{Vpn: &labpb.VPNFeature{ReverseConnections: true}}
	require.NoError(t, late.fleet.DeployLab(ctx, "e-2-t-1", "c-1", infraModel.LabMeta{}, allowed))
	require.Contains(t, string(late.a.createLabs.GetVariants()[0].GetSpecJson()), `"reverseConnections":true`)

	// A forbidding exercise does not wait. Its status follows what was requested and what the agent turns out to be.
	forbidden := newFleetFixture(t)
	forbidden.am.Features = NewFeatureCell(nil)
	require.NoError(t, forbidden.fleet.DeployLab(ctx, "e-3-t-1", "c-1", infraModel.LabMeta{}, vpnTopology(exerciseModel.NetworkSpec{Enabled: true})))
	forbidden.a.labs = []*labpb.Lab{{Name: "c-1", Labels: forbidden.a.createLabs.GetItems()[0].GetLabels(), Status: &labpb.LabStatus{}}}
	forbidden.am.Features.Set(infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}})
	st, err = forbidden.fleet.LabStatus(ctx, "e-3-t-1", "c-1")
	require.NoError(t, err)
	require.Equal(t, "blocked", st.VPNReverse)
	forbidden.am.Features.Set(infraModel.AgentFeatures{})
	st, err = forbidden.fleet.LabStatus(ctx, "e-3-t-1", "c-1")
	require.NoError(t, err)
	require.Equal(t, "not_enforced", st.VPNReverse)
}
