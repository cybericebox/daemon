package exercise

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// topology.vpn.reverse_connections is read, kept, written back by the export and refused without the VPN network.
func TestToParsed_VPNReverseConnections(t *testing.T) {
	parsed, err := parseDoc(t, variantDoc(`{"name":"t"}`, `{"vpn":{"enabled":true,"reverse_connections":true},"devices":[{"name":"web","type":"node"}]}`))
	require.NoError(t, err)
	vpn := parsed.Versions[0].Variants[0].Topology.VPN
	require.True(t, vpn.Enabled)
	require.True(t, vpn.ReverseConnections)

	// The export writes the topology from the same model: the key survives the round trip, and is absent when off.
	encoded, err := json.Marshal(parsed.Versions[0].Variants[0].Topology.VPN)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"reverse_connections":true`)
	off, err := json.Marshal(exerciseModel.NetworkSpec{Enabled: true})
	require.NoError(t, err)
	require.NotContains(t, string(off), "reverse_connections")

	// Default: absent means false.
	parsed, err = parseDoc(t, variantDoc(`{"name":"t"}`, `{"vpn":{"enabled":true}}`))
	require.NoError(t, err)
	require.False(t, parsed.Versions[0].Variants[0].Topology.VPN.ReverseConnections)

	// Without the VPN network, or on the internet gateway, it is an error.
	for name, topology := range map[string]string{
		"vpn off":     `{"vpn":{"reverse_connections":true}}`,
		"on internet": `{"vpn":{"enabled":true},"internet":{"enabled":true,"reverse_connections":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, parseErr := parseDoc(t, variantDoc(`{"name":"t"}`, topology))
			require.ErrorIs(t, parseErr, exerciseModel.ErrVPNOptionInvalid.Err())
			require.Contains(t, parseErr.Error(), "topology.vpn.reverse_connections")
			require.Contains(t, parseErr.Error(), "versions[1].variants[1]", "the error carries the path")
		})
	}
}
