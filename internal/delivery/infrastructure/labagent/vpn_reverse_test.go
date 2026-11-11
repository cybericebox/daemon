package labagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

func vpnTopology(vpn exerciseModel.NetworkSpec) exerciseModel.Topology {
	return exerciseModel.Topology{VPN: vpn, Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeNode, Image: "nginx"}}}
}

func specOf(t *testing.T, build LabBuild) labSpec {
	t.Helper()
	var spec labSpec
	require.NoError(t, json.Unmarshal(build.SpecJSON, &spec))
	return spec
}

// The key that allows connections from the lab to the VPN clients is sent only to an agent that enforces the block, only when
// the exercise asks for it and the VPN is on. An older agent rejects the key, so it never gets it.
func TestBuildLabFor_ReverseConnectionsKey(t *testing.T) {
	for name, tc := range map[string]struct {
		vpn      exerciseModel.NetworkSpec
		enforces bool
		sent     bool
	}{
		"allowed, capable agent":   {exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true}, true, true},
		"allowed, older agent":     {exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true}, false, false},
		"forbidden, capable agent": {exerciseModel.NetworkSpec{Enabled: true}, true, false},
		"forbidden, older agent":   {exerciseModel.NetworkSpec{Enabled: true}, false, false},
		"vpn off":                  {exerciseModel.NetworkSpec{ReverseConnections: true}, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			build, err := BuildLabFor(vpnTopology(tc.vpn), nil, tc.enforces)
			require.NoError(t, err)
			require.Equal(t, tc.sent, specOf(t, build).VPN.ReverseConnections)
			if tc.sent {
				require.Contains(t, string(build.SpecJSON), `"reverseConnections":true`)
			} else {
				require.NotContains(t, string(build.SpecJSON), "reverseConnections")
			}
			internet, err := BuildLabFor(vpnTopology(tc.vpn), nil, tc.enforces)
			require.NoError(t, err)
			require.False(t, specOf(t, internet).Internet.ReverseConnections, "never on the internet gateway")
		})
	}
	old, err := BuildLab(vpnTopology(exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true}), nil)
	require.NoError(t, err)
	require.NotContains(t, string(old.SpecJSON), "reverseConnections", "BuildLab is for an agent that does not report the option")
}

func TestFeaturesOfReadsTheVPNOption(t *testing.T) {
	require.Equal(t, &infraModel.VPNFeature{ReverseConnections: true}, FeaturesOf(&labpb.FeaturesResponse{Vpn: &labpb.VPNFeature{ReverseConnections: true}}).VPN)
	require.Nil(t, FeaturesOf(&labpb.FeaturesResponse{}).VPN, "an older agent reports none")
	require.False(t, FeaturesOf(&labpb.FeaturesResponse{}).EnforcesVPNReverse())
}

// The fleet sends the key by what the agent reported, labels the lab with what the exercise asked, and the status says what is
// really in force: blocked, allowed, or not enforced on an agent that cannot block.
func TestFleetVPNReverseEndToEnd(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		features *infraModel.AgentFeatures
		vpn      exerciseModel.NetworkSpec
		key      bool
		label    string
		state    string
	}{
		"capable, forbidden": {&infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}}, exerciseModel.NetworkSpec{Enabled: true}, false, "0", "blocked"},
		"capable, allowed":   {&infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}}, exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true}, true, "1", "allowed"},
		"older, forbidden":   {&infraModel.AgentFeatures{}, exerciseModel.NetworkSpec{Enabled: true}, false, "0", "not_enforced"},
		"older, allowed":     {&infraModel.AgentFeatures{}, exerciseModel.NetworkSpec{Enabled: true, ReverseConnections: true}, false, "1", "allowed"},
		"vpn off":            {&infraModel.AgentFeatures{VPN: &infraModel.VPNFeature{ReverseConnections: true}}, exerciseModel.NetworkSpec{}, false, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFleetFixture(t)
			f.am.Features = NewFeatureCell(tc.features)
			require.NoError(t, f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, vpnTopology(tc.vpn)))
			item := f.a.createLabs.GetItems()[0]
			var spec labSpec
			require.NoError(t, json.Unmarshal(f.a.createLabs.GetVariants()[0].GetSpecJson(), &spec))
			require.Equal(t, tc.key, spec.VPN.ReverseConnections)
			require.Equal(t, tc.label, item.GetLabels()[infraModel.LabelVPNReverse])

			// The agent returns the lab with the labels it was created with.
			f.a.labs = []*labpb.Lab{{Name: "c-1", Labels: item.GetLabels(), Status: &labpb.LabStatus{}}}
			status, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
			require.NoError(t, err)
			require.Equal(t, tc.state, status.VPNReverse)
		})
	}
}
