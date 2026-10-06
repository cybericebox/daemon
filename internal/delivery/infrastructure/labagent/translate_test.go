package labagent

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestBuildLabSpec_UsesTechnicalNameForForwardingDisplayName(t *testing.T) {
	switchNode := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "Коммутатор №1 / DMZ", Type: exerciseModel.DeviceTypeUnmanagedSwitch}
	host := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "host", Type: exerciseModel.DeviceTypeContainer}
	encoded, _, err := BuildLabSpec(exerciseModel.Topology{
		Devices: []exerciseModel.Device{switchNode, host},
		Connections: []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{
			{Kind: exerciseModel.EndpointDevice, DeviceID: switchNode.ID, Interface: "GigabitEthernet0/1"},
			{Kind: exerciseModel.EndpointDevice, DeviceID: host.ID, Interface: "eth0"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := decode(t, encoded)
	technical := spec.Devices[0].Name
	if technical == switchNode.Name || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(technical) {
		t.Fatalf("forwarding node lacks safe technical name: %q", technical)
	}
	if spec.Connections[0].Endpoints[0].Device != technical {
		t.Fatalf("connection points to display name, not technical name: %+v", spec.Connections[0])
	}
}

// decode unmarshals the produced spec_json back into the mirror struct so tests
// assert on structure rather than brittle byte output.
func decode(t *testing.T, b []byte) labSpec {
	t.Helper()
	var s labSpec
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	return s
}

func TestBuildLabSpec_NetworksAndDHCP(t *testing.T) {
	topo := exerciseModel.Topology{
		VPN: exerciseModel.NetworkSpec{Enabled: true, DHCP: true,
			DHCPRanges: []exerciseModel.DHCPRange{{Start: 2, End: 50}}},
		Internet: exerciseModel.NetworkSpec{Enabled: true, DHCP: true, DNS: "1.1.1.1",
			DHCPRanges: []exerciseModel.DHCPRange{{Start: 20, End: 100}, {Start: 150, End: 200}}},
	}
	b, _, err := BuildLabSpec(topo)
	if err != nil {
		t.Fatal(err)
	}
	s := decode(t, b)
	if !s.VPN.Enabled || s.VPN.DHCPServer == nil || !s.VPN.DHCPServer.Enabled {
		t.Errorf("vpn dhcp server should be enabled: %+v", s.VPN)
	}
	if !s.Internet.Enabled || s.Internet.DHCPServer == nil || s.Internet.DHCPServer.DNS != "1.1.1.1" {
		t.Errorf("internet DHCP DNS should be configured per lab: %+v", s.Internet)
	}
	if s.VPN.DHCPServer.DNS != "" {
		t.Errorf("VPN DHCP must not advertise a DNS server: %+v", s.VPN)
	}
	if len(s.VPN.DHCPServer.Ranges) != 1 || len(s.Internet.DHCPServer.Ranges) != 2 || s.Internet.DHCPServer.Ranges[1].Start != 150 {
		t.Errorf("DHCP ranges not carried into LabSpec: %+v %+v", s.VPN, s.Internet)
	}
}

func TestBuildLabSpec_InterfaceAddrModes(t *testing.T) {
	dev := exerciseModel.Device{
		ID:   uuid.Must(uuid.NewV7()),
		Name: "host",
		Type: exerciseModel.DeviceTypeContainer,
		Interfaces: []exerciseModel.Interface{
			{Name: "s", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.0.0.2/24"}, Gateway: "10.0.0.1"}},
			{Name: "d", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCP}},
			{Name: "p", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCPPreset}},
			{Name: "n", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeNone}},
		},
	}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{dev}})
	if err != nil {
		t.Fatal(err)
	}
	ifs := decode(t, b).Devices[0].Interfaces
	if ifs[0].Addr == nil || ifs[0].Addr.Type != "static" || ifs[0].Addr.IP != "10.0.0.2/24" || ifs[0].Addr.Gateway != "10.0.0.1" {
		t.Errorf("static iface should preserve its single address: %+v", ifs[0].Addr)
	}
	if ifs[1].Addr == nil || ifs[1].Addr.Type != "dhcp" || ifs[1].Addr.IP != "" {
		t.Errorf("dhcp iface wrong: %+v", ifs[1].Addr)
	}
	if ifs[2].Addr == nil || ifs[2].Addr.Type != "dhcp-preset" {
		t.Errorf("dhcp-preset iface wrong: %+v", ifs[2].Addr)
	}
	if ifs[3].Addr != nil {
		t.Errorf("none iface must omit addr entirely: %+v", ifs[3].Addr)
	}
}

func TestBuildLabSpec_ResourcesAndRoutes(t *testing.T) {
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{{
		ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer,
		Resources: &exerciseModel.DeviceResources{CPURequest: "250m", MemoryLimit: "512Mi"},
		Interfaces: []exerciseModel.Interface{{Name: "eth0", IP: exerciseModel.IPConfig{
			Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.0.0.2/24"},
			Routes: []exerciseModel.Route{{Dst: "10.1.0.0/16", Via: "10.0.0.1"}},
		}}},
	}}}
	encoded, _, err := BuildLabSpec(topology)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	device := raw["devices"].([]any)[0].(map[string]any)
	resources, ok := device["resources"].(map[string]any)
	if !ok || resources["cpuRequest"] != "250m" || resources["memoryLimit"] != "512Mi" {
		t.Fatalf("LabSpec resources lost: %#v", device["resources"])
	}
	address := device["interfaces"].([]any)[0].(map[string]any)["addr"].(map[string]any)
	routes, ok := address["routes"].([]any)
	if !ok || len(routes) != 1 || routes[0].(map[string]any)["dst"] != "10.1.0.0/16" || routes[0].(map[string]any)["via"] != "10.0.0.1" {
		t.Fatalf("LabSpec static routes lost: %#v", address["routes"])
	}
}

func TestBuildLabSpec_AddressReferencesRemainTyped(t *testing.T) {
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{{
		ID: uuid.Must(uuid.NewV7()), Name: "host", Type: exerciseModel.DeviceTypeContainer,
		Interfaces: []exerciseModel.Interface{{Name: "eth0", IP: exerciseModel.IPConfig{
			Type:       exerciseModel.IPConfigTypeStatic,
			AddressRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 10},
			GatewayRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 1},
			Routes: []exerciseModel.Route{{DstRef: &exerciseModel.NetworkSubnetRef{Network: "internet"},
				ViaRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 1}}},
		}}},
	}}}
	encoded, _, err := BuildLabSpec(topo)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	addr := raw["devices"].([]any)[0].(map[string]any)["interfaces"].([]any)[0].(map[string]any)["addr"].(map[string]any)
	if _, ok := addr["ip"]; ok {
		t.Fatalf("address resolved before lab allocation: %#v", addr)
	}
	if _, ok := addr["gateway"]; ok {
		t.Fatalf("gateway resolved before lab allocation: %#v", addr)
	}
	if ref := addr["addressRef"].(map[string]any); ref["network"] != "vpn" || ref["host"] != float64(10) {
		t.Fatalf("addressRef missing: %#v", addr)
	}
	if ref := addr["gatewayRef"].(map[string]any); ref["network"] != "vpn" || ref["host"] != float64(1) {
		t.Fatalf("gatewayRef missing: %#v", addr)
	}
	route := addr["routes"].([]any)[0].(map[string]any)
	if route["dstRef"].(map[string]any)["network"] != "internet" || route["viaRef"].(map[string]any)["host"] != float64(1) {
		t.Fatalf("route references missing: %#v", route)
	}
}

// A "none" interface must serialise with no "addr" key at all (not addr:{}),
// otherwise the operator's required AddrSpec.Type rejects the device.
func TestBuildLabSpec_NoneOmitsAddrKey(t *testing.T) {
	dev := exerciseModel.Device{
		ID: uuid.Must(uuid.NewV7()), Name: "host", Type: exerciseModel.DeviceTypeContainer,
		Interfaces: []exerciseModel.Interface{{Name: "n", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeNone}}},
	}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{dev}})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	iface := raw["devices"].([]any)[0].(map[string]any)["interfaces"].([]any)[0].(map[string]any)
	if _, ok := iface["addr"]; ok {
		t.Errorf("addr key must be absent for a none interface: %v", iface)
	}
}

func TestBuildLabSpec_ExposureAndPreset(t *testing.T) {
	dev := exerciseModel.Device{
		ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer,
		Image: "nginx", SecurityPreset: exerciseModel.SecurityPresetService,
		External: &exerciseModel.ExternalAccess{Port: 443, Protocol: "https"},
	}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{dev}})
	if err != nil {
		t.Fatal(err)
	}
	d := decode(t, b).Devices[0]
	if d.SecurityPreset != "service" || d.Image != "nginx" {
		t.Errorf("device meta wrong: %+v", d)
	}
	if d.Exposure == nil || d.Exposure.Web == nil || d.Exposure.Web.Port != 443 || d.Exposure.Web.Protocol != "https" {
		t.Errorf("exposure wrong: %+v", d.Exposure)
	}
}

func TestBuildLabSpec_ConnectionsResolveNamesAndGateways(t *testing.T) {
	web := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer}
	sw := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch}
	topo := exerciseModel.Topology{
		VPN:     exerciseModel.NetworkSpec{Enabled: true},
		Devices: []exerciseModel.Device{web, sw},
		Connections: []exerciseModel.Connection{
			{Endpoints: []exerciseModel.Endpoint{
				{Kind: exerciseModel.EndpointDevice, DeviceID: web.ID, Interface: "eth0"},
				{Kind: exerciseModel.EndpointDevice, DeviceID: sw.ID, Interface: "GigabitEthernet0/1"},
			}},
			{Endpoints: []exerciseModel.Endpoint{
				{Kind: exerciseModel.EndpointVPN, Interface: "eth0"},
				{Kind: exerciseModel.EndpointDevice, DeviceID: sw.ID, Interface: "GigabitEthernet0/2"},
			}},
			{Endpoints: []exerciseModel.Endpoint{
				{Kind: exerciseModel.EndpointInternet, Interface: "eth0"},
				{Kind: exerciseModel.EndpointDevice, DeviceID: sw.ID, Interface: "GigabitEthernet0/3"},
			}},
		},
	}
	b, _, err := BuildLabSpec(topo)
	if err != nil {
		t.Fatal(err)
	}
	spec := decode(t, b)
	conns := spec.Connections
	if conns[0].Endpoints[0].Device != "web" || conns[0].Endpoints[0].Interface != "eth0" {
		t.Errorf("device endpoint should resolve to name: %+v", conns[0].Endpoints[0])
	}
	if conns[0].Endpoints[1].Device != spec.Devices[1].Name {
		t.Errorf("switch endpoint should resolve to name: %+v", conns[0].Endpoints[1])
	}
	if conns[1].Endpoints[0].Device != "vpn" {
		t.Errorf("vpn endpoint should map to reserved name: %+v", conns[1].Endpoints[0])
	}
	if conns[2].Endpoints[0].Device != "internet" {
		t.Errorf("internet endpoint should map to reserved name: %+v", conns[2].Endpoints[0])
	}
}

func TestBuildLabSpec_GatewayPorts(t *testing.T) {
	for _, kind := range []exerciseModel.EndpointKind{exerciseModel.EndpointVPN, exerciseModel.EndpointInternet} {
		for _, inputPort := range []string{"eth0"} {
			t.Run(string(kind)+"/"+inputPort, func(t *testing.T) {
				host := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "host", Type: exerciseModel.DeviceTypeContainer}
				topo := exerciseModel.Topology{
					Devices: []exerciseModel.Device{host},
					Connections: []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{
						{Kind: kind, Interface: inputPort},
						{Kind: exerciseModel.EndpointDevice, DeviceID: host.ID, Interface: "eth0"},
					}}},
				}
				encoded, _, err := BuildLabSpec(topo)
				if err != nil {
					t.Fatal(err)
				}
				endpoint := decode(t, encoded).Connections[0].Endpoints[0]
				if endpoint.Device != string(kind) || endpoint.Interface != "eth0" {
					t.Fatalf("gateway endpoint should use logical eth0: %+v", endpoint)
				}
			})
		}
	}
}

func TestBuildLabSpec_DeviceEnv(t *testing.T) {
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{
		{
			ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer,
			EnvVars: []exerciseModel.EnvVar{
				{Name: "FLAG", Value: "CTF{x}", Secret: true},
				{Name: "MODE", Value: "prod"},
			},
		},
		{ID: uuid.Must(uuid.NewV7()), Name: "bare", Type: exerciseModel.DeviceTypeContainer},
	}}
	_, env, err := BuildLabSpec(topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 {
		t.Fatalf("only the device with env should appear: %d", len(env))
	}
	if env[0].Device != "web" || env[0].Vars["FLAG"] != "CTF{x}" || env[0].Vars["MODE"] != "prod" {
		t.Errorf("env wrong: %+v", env[0])
	}
}

func TestBuildLabSpec_PersistenceAndNoVariablesInTheSpec(t *testing.T) {
	dev := exerciseModel.Device{Name: "db", Type: exerciseModel.DeviceTypeContainer, Image: "pg",
		EnvVars:     []exerciseModel.EnvVar{{Name: "FLAG", Value: "secret-flag"}},
		Persistence: &exerciseModel.DevicePersistence{Enabled: true, Debounce: "5s"}}
	b, env, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{dev}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-flag") || strings.Contains(string(b), "launchClass") {
		t.Fatalf("spec leaks variables or the removed launch class: %s", b)
	}
	got := decode(t, b).Devices[0].Persistence
	if got == nil || !got.Enabled || got.Debounce != "5s" {
		t.Fatalf("persistence = %+v", got)
	}
	if len(env) != 1 || env[0].GetVars()["FLAG"] != "secret-flag" {
		t.Fatalf("env = %+v", env)
	}
	plain, _, _ := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeContainer}}})
	if strings.Contains(string(plain), "persistence") {
		t.Fatalf("a device without persistence must not carry the key: %s", plain)
	}
}
