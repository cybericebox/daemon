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

// gatewayStatus is the laboratory's status of one VPN gateway.
func gatewayStatus(name string, blocked bool) *labpb.LabStatus {
	return &labpb.LabStatus{Gateways: []*labpb.LabGatewayStatus{{Name: name, Kind: "vpn", Cidr: "10.128.1.0/24", Ready: true, LabInitiatedBlocked: blocked}}}
}

func reverseOf(t *testing.T, st exerciseModel.LabDeployStatus, name string) string {
	t.Helper()
	for _, g := range st.Gateways {
		if g.Name == name {
			return g.Reverse
		}
	}
	require.FailNow(t, "no gateway in the status", name)
	return ""
}

// The fleet sends the option by what the agent reported, records on the lab what was really requested for each VPN gateway,
// and the status says what is really in force: blocked, allowed, or not enforced on an agent that cannot block.
func TestFleetVPNReverseEndToEnd(t *testing.T) {
	ctx := context.Background()
	capable := &infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}}
	for name, tc := range map[string]struct {
		features *infraModel.AgentFeatures
		reverse  bool
		sent     bool
		// confirmed is lab_initiated_blocked of the gateway in the lab status.
		confirmed bool
		state     string
	}{
		"capable, forbidden, confirmed":     {capable, false, false, true, "blocked"},
		"capable, forbidden, not confirmed": {capable, false, false, false, "not_enforced"},
		"capable, allowed":                  {capable, true, true, false, "allowed"},
		"capable, allowed, a stray confirm": {capable, true, true, true, "allowed"},
		"older, forbidden":                  {&infraModel.AgentFeatures{}, false, false, false, "not_enforced"},
		"older, allowed":                    {&infraModel.AgentFeatures{}, true, false, false, "allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFleetFixture(t)
			f.am.Features = NewFeatureCell(tc.features)
			require.NoError(
				t,
				f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, vpnTopology(vpnGateway("attack", tc.reverse))),
			)
			item := f.a.createLabs.GetItems()[0]
			var spec labSpec
			require.NoError(t, json.Unmarshal(f.a.createLabs.GetVariants()[0].GetSpecJson(), &spec))
			require.Equal(t, tc.sent, gatewayOf(t, spec, "attack").ReverseConnections)
			require.Equal(t, map[bool]string{true: "1", false: "0"}[tc.sent], item.GetLabels()[infraModel.LabelVPNReverseSentPrefix+"attack"])

			f.a.labs = []*labpb.Lab{{Name: "c-1", Labels: item.GetLabels(), Status: gatewayStatus("attack", tc.confirmed)}}
			st, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
			require.NoError(t, err)
			require.Equal(t, tc.state, reverseOf(t, st, "attack"))
			require.Equal(t, tc.reverse, st.Gateways[0].ReverseConnections)
		})
	}
}

// Two VPN gateways of one lab have their own switch and their own state; an Internet gateway has none.
func TestFleetVPNReverse_PerGateway(t *testing.T) {
	ctx := context.Background()
	f := newFleetFixture(t)
	f.am.Features = NewFeatureCell(&infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}})
	topo := vpnTopology(vpnGateway("open", true), vpnGateway("closed", false))
	require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, topo))
	labels := f.a.createLabs.GetItems()[0].GetLabels()
	require.Equal(t, "1", labels[infraModel.LabelVPNReversePrefix+"open"])
	require.Equal(t, "1", labels[infraModel.LabelVPNReverseSentPrefix+"open"])
	require.Equal(t, "0", labels[infraModel.LabelVPNReversePrefix+"closed"])
	require.Equal(t, "0", labels[infraModel.LabelVPNReverseSentPrefix+"closed"])

	f.a.labs = []*labpb.Lab{{Name: "c-1", Labels: labels, Status: &labpb.LabStatus{Gateways: []*labpb.LabGatewayStatus{
		{Name: "open", Kind: "vpn", Cidr: "10.128.1.0/24", Ready: true},
		{Name: "closed", Kind: "vpn", Cidr: "10.128.2.0/24", Ready: true, LabInitiatedBlocked: true},
		{Name: "out", Kind: "internet", Cidr: "10.128.3.0/24", Ready: true},
	}}}}
	st, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
	require.NoError(t, err)
	require.Len(t, st.Gateways, 3)
	require.Equal(t, "allowed", reverseOf(t, st, "open"))
	require.Equal(t, "blocked", reverseOf(t, st, "closed"))
	require.Empty(t, reverseOf(t, st, "out"), "an Internet gateway has no reverse state")
	require.Equal(t, "10.128.3.0/24", st.Gateways[2].CIDR)
}

// Features not received yet and an exercise that allows the connections: no lab is made without the option (a capable agent
// would block them and the task could not be solved). The start waits as for an agent that does not answer, and goes through
// once the features arrive. An exercise that forbids them starts at once: no option is the right request for it.
func TestFleetVPNReverse_FeaturesUnknownAtCreation(t *testing.T) {
	ctx := context.Background()
	allowed := vpnTopology(vpnGateway("attack", true))
	f := newFleetFixture(t) // no features reported, and the agent cannot be asked
	f.am.Features = NewFeatureCell(nil)

	err := f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, allowed)
	require.Equal(
		t,
		codes.Unavailable,
		status.Code(err),
		"a transient wait, not a failure of the lab",
	)
	require.Nil(t, f.a.createLabs, "nothing is created")

	// The features arrive (a capable agent): the same start now sends the option and records it.
	f.am.Features.Set(
		infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}},
	)
	require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, allowed))
	var spec labSpec
	require.NoError(t, json.Unmarshal(f.a.createLabs.GetVariants()[0].GetSpecJson(), &spec))
	require.True(t, gatewayOf(t, spec, "attack").ReverseConnections)
	labels := f.a.createLabs.GetItems()[0].GetLabels()
	require.Equal(t, "1", labels[infraModel.LabelVPNReversePrefix+"attack"])
	require.Equal(t, "1", labels[infraModel.LabelVPNReverseSentPrefix+"attack"])
	f.a.labs = []*labpb.Lab{{Name: "c-1", Labels: labels, Status: gatewayStatus("attack", false)}}
	st, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
	require.NoError(t, err)
	require.Equal(t, "allowed", reverseOf(t, st, "attack"))

	// The features can also be read at the moment of the start: it does not wait for the next poll.
	late := newFleetFixture(t)
	late.am.Features = NewFeatureCell(nil)
	late.a.features = &labpb.FeaturesResponse{Vpn: &labpb.VPNFeature{ReverseConnections: true}}
	require.NoError(t, late.fleet.DeployLab(ctx, "e-2-t-1", "c-1", infraModel.LabMeta{}, allowed))
	require.Contains(
		t,
		string(late.a.createLabs.GetVariants()[0].GetSpecJson()),
		`"reverseConnections":true`,
	)

	// A forbidding exercise does not wait. Its status follows what was requested and what the agent turns out to be.
	forbidden := newFleetFixture(t)
	forbidden.am.Features = NewFeatureCell(nil)
	require.NoError(
		t,
		forbidden.fleet.DeployLab(ctx, "e-3-t-1", "c-1", infraModel.LabMeta{}, vpnTopology(vpnGateway("attack", false))),
	)
	labelsOf := forbidden.a.createLabs.GetItems()[0].GetLabels()
	forbidden.a.labs = []*labpb.Lab{{Name: "c-1", Labels: labelsOf, Status: gatewayStatus("attack", false)}}
	forbidden.am.Features.Set(
		infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}},
	)
	st, err = forbidden.fleet.LabStatus(ctx, "e-3-t-1", "c-1")
	require.NoError(t, err)
	require.Equal(t, "not_enforced", reverseOf(t, st, "attack"), "the block is not confirmed by the laboratory yet")
	forbidden.a.labs = []*labpb.Lab{{Name: "c-1", Labels: labelsOf, Status: gatewayStatus("attack", true)}}
	st, err = forbidden.fleet.LabStatus(ctx, "e-3-t-1", "c-1")
	require.NoError(t, err)
	require.Equal(t, "blocked", reverseOf(t, st, "attack"))
	forbidden.am.Features.Set(infraModel.AgentFeatures{})
	forbidden.a.labs = []*labpb.Lab{{Name: "c-1", Labels: labelsOf, Status: gatewayStatus("attack", false)}}
	st, err = forbidden.fleet.LabStatus(ctx, "e-3-t-1", "c-1")
	require.NoError(t, err)
	require.Equal(t, "not_enforced", reverseOf(t, st, "attack"))
}
