package labagent

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

func vpnGateway(name string, reverse bool) exerciseModel.Device {
	return exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: exerciseModel.DeviceTypeVPNGateway, ReverseConnections: reverse}
}

// vpnTopology is a lab with the given gateways and one node.
func vpnTopology(gateways ...exerciseModel.Device) exerciseModel.Topology {
	devices := append([]exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeNode, Image: "nginx"}}, gateways...)
	return exerciseModel.Topology{Devices: devices}
}

func specOf(t *testing.T, build LabBuild) labSpec {
	t.Helper()
	var spec labSpec
	require.NoError(t, json.Unmarshal(build.SpecJSON, &spec))
	return spec
}

func gatewayOf(t *testing.T, spec labSpec, name string) labGateway {
	t.Helper()
	for _, g := range spec.Gateways {
		if g.Name == name {
			return g
		}
	}
	require.FailNow(t, "no gateway in the spec", name)
	return labGateway{}
}

// The key that allows connections from the lab to the VPN clients is sent only to an agent that enforces the block, only when
// the exercise asks for it on that VPN gateway. An older agent rejects the key, so it never gets it.
func TestBuildLabFor_ReverseConnectionsKey(t *testing.T) {
	for name, tc := range map[string]struct {
		reverse  bool
		enforces bool
		sent     bool
	}{
		"allowed, capable agent":   {true, true, true},
		"allowed, older agent":     {true, false, false},
		"forbidden, capable agent": {false, true, false},
		"forbidden, older agent":   {false, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			build, err := BuildLabFor(vpnTopology(vpnGateway("attack", tc.reverse)), nil, tc.enforces)
			require.NoError(t, err)
			require.Equal(t, tc.sent, gatewayOf(t, specOf(t, build), "attack").ReverseConnections)
			require.Equal(t, tc.sent, build.ReverseSent["attack"])
			if tc.sent {
				require.Contains(t, string(build.SpecJSON), `"reverseConnections":true`)
			} else {
				require.NotContains(t, string(build.SpecJSON), "reverseConnections")
			}
		})
	}
	old, err := BuildLab(vpnTopology(vpnGateway("attack", true)), nil)
	require.NoError(t, err)
	require.NotContains(t, string(old.SpecJSON), "reverseConnections", "BuildLab is for an agent that does not report the option")
}

// The key belongs to one VPN gateway: the other gateways of the lab do not get it, and an Internet gateway never does.
func TestBuildLabFor_ReverseConnectionsKeyIsPerGateway(t *testing.T) {
	open, closed := vpnGateway("open", true), vpnGateway("closed", false)
	out := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "out", Type: exerciseModel.DeviceTypeInternetGateway}
	build, err := BuildLabFor(vpnTopology(open, closed, out), nil, true)
	require.NoError(t, err)
	spec := specOf(t, build)
	require.True(t, gatewayOf(t, spec, "open").ReverseConnections)
	require.False(t, gatewayOf(t, spec, "closed").ReverseConnections)
	require.False(t, gatewayOf(t, spec, "out").ReverseConnections, "never on the internet gateway")
	require.Equal(t, map[string]bool{"open": true}, build.ReverseSent)
}

func TestFeaturesOfReadsTheVPNOption(t *testing.T) {
	require.Equal(t, &infraModel.VPNFeature{ReverseConnections: true, MaxGateways: 10},
		FeaturesOf(&labpb.FeaturesResponse{Vpn: &labpb.VPNFeature{ReverseConnections: true, MaxGateways: 10}}).VPN)
	require.Nil(t, FeaturesOf(&labpb.FeaturesResponse{}).VPN, "an older agent reports none")
	require.False(t, FeaturesOf(&labpb.FeaturesResponse{}).EnforcesVPNReverse())
}
