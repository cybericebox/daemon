package exercise

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// reverse_connections is a key of a VPN gateway device: read, kept, written back by the export and refused on any other device.
func TestToParsed_VPNReverseConnections(t *testing.T) {
	parsed, err := parseDoc(t, variantDoc(`{"name":"t"}`, `{"devices":[{"name":"attack","type":"vpn_gateway","reverse_connections":true},{"name":"web","type":"node"}]}`))
	require.NoError(t, err)
	gateway := parsed.Versions[0].Variants[0].Topology.Devices[0]
	require.Equal(t, exerciseModel.DeviceTypeVPNGateway, gateway.Type)
	require.True(t, gateway.ReverseConnections)

	// The export writes the topology from the same model: the key survives the round trip, and is absent when off.
	encoded, err := json.Marshal(archiveDeviceV2{Name: "attack", Type: gateway.Type, ReverseConnections: gateway.ReverseConnections})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"reverse_connections":true`)
	off, err := json.Marshal(archiveDeviceV2{Name: "attack", Type: gateway.Type})
	require.NoError(t, err)
	require.NotContains(t, string(off), "reverse_connections")

	// Default: absent means false.
	parsed, err = parseDoc(t, variantDoc(`{"name":"t"}`, `{"devices":[{"name":"attack","type":"vpn_gateway"}]}`))
	require.NoError(t, err)
	require.False(t, parsed.Versions[0].Variants[0].Topology.Devices[0].ReverseConnections)

	// On the Internet gateway, or on a node, it is an error.
	for name, topology := range map[string]string{
		"on an internet gateway": `{"devices":[{"name":"out","type":"internet_gateway","reverse_connections":true}]}`,
		"on a node":              `{"devices":[{"name":"web","type":"node","reverse_connections":true}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, parseErr := parseDoc(t, variantDoc(`{"name":"t"}`, topology))
			require.Error(t, parseErr)
			require.Contains(t, parseErr.Error(), "versions[1].variants[1]", "the error carries the path")
		})
	}
}

// The task format has no `vpn` and `internet` keys of the topology and no endpoint kinds vpn and internet any more: an old
// document is refused (an unknown key, an unknown kind), not converted.
func TestToParsed_RemovedGatewayKeysAreRefused(t *testing.T) {
	for name, topology := range map[string]string{
		"vpn key":             `{"vpn":{"enabled":true},"devices":[{"name":"web","type":"node"}]}`,
		"internet key":        `{"internet":{"enabled":true},"devices":[{"name":"web","type":"node"}]}`,
		"endpoint kind vpn":   `{"devices":[{"name":"web","type":"node"}],"connections":[{"endpoints":[{"kind":"vpn","interface":"eth0"},{"kind":"device","device":"web","interface":"eth0"}]}]}`,
		"address ref network": `{"devices":[{"name":"web","type":"node","interfaces":[{"name":"eth0","ip":{"type":"static","address_ref":{"network":"vpn","host":10}}}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseDoc(t, variantDoc(`{"name":"t"}`, topology))
			require.Error(t, err)
		})
	}
}

// A gateway is a device of the document: it round-trips by name, with its settings and its connection.
func TestToParsed_GatewayDevicesAndConnections(t *testing.T) {
	parsed, err := parseDoc(t, variantDoc(`{"name":"t"}`, `{"devices":[`+
		`{"name":"attack","type":"vpn_gateway","dhcp":true,"dhcp_ranges":[{"start":100,"end":200}]},`+
		`{"name":"out","type":"internet_gateway","dns":"1.1.1.1"},`+
		`{"name":"web","type":"node","interfaces":[{"name":"eth0","ip":{"type":"static","address_ref":{"gateway":"attack","host":10}}}]}],`+
		`"connections":[{"endpoints":[{"kind":"device","device":"attack","interface":"eth0"},{"kind":"device","device":"web","interface":"eth0"}]}]}`))
	require.NoError(t, err)
	topology := parsed.Versions[0].Variants[0].Topology
	require.Len(t, topology.Devices, 3)
	require.True(t, topology.Devices[0].DHCP)
	require.Equal(t, []exerciseModel.DHCPRange{{Start: 100, End: 200}}, topology.Devices[0].DHCPRanges)
	require.Equal(t, "1.1.1.1", topology.Devices[1].DNS)
	require.Equal(t, "attack", topology.Devices[2].Interfaces[0].IP.AddressRef.Gateway)
	require.Equal(t, topology.Devices[0].ID, topology.Connections[0].Endpoints[0].DeviceID)
	require.Equal(t, exerciseModel.EndpointDevice, topology.Connections[0].Endpoints[0].Kind)
}
