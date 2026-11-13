package labagent

import (
	"encoding/json"
	"reflect"
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

func gatewayDevice(name string, typ exerciseModel.DeviceType) exerciseModel.Device {
	return exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: typ}
}

// The VPN and Internet gateways of a task are devices of their own kinds; the lab gets them as spec.gateways and no device.
func TestBuildLabSpec_GatewaysAndDHCP(t *testing.T) {
	vpn := gatewayDevice("attack", exerciseModel.DeviceTypeVPNGateway)
	vpn.DHCP, vpn.DHCPRanges = true, []exerciseModel.DHCPRange{{Start: 2, End: 50}}
	out := gatewayDevice("out", exerciseModel.DeviceTypeInternetGateway)
	out.DHCP, out.DNS = true, "1.1.1.1"
	out.DHCPRanges = []exerciseModel.DHCPRange{{Start: 20, End: 100}, {Start: 150, End: 200}}
	web := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{vpn, web, out}})
	if err != nil {
		t.Fatal(err)
	}
	s := decode(t, b)
	if len(s.Gateways) != 2 || s.Gateways[0].Name != "attack" || s.Gateways[0].Kind != "vpn" || s.Gateways[1].Name != "out" || s.Gateways[1].Kind != "internet" {
		t.Fatalf("gateways must follow the task's order: %+v", s.Gateways)
	}
	if len(s.Devices) != 1 || s.Devices[0].Name != "web" {
		t.Fatalf("a gateway is not a device of the lab: %+v", s.Devices)
	}
	if d := s.Gateways[0].DHCPServer; d == nil || !d.Enabled || d.DNS != "" || len(d.Ranges) != 1 {
		t.Errorf("vpn dhcp server: %+v", d)
	}
	if d := s.Gateways[1].DHCPServer; d == nil || d.DNS != "1.1.1.1" || len(d.Ranges) != 2 || d.Ranges[1].Start != 150 {
		t.Errorf("internet dhcp server: %+v", d)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"vpn", "internet"} {
		if _, ok := raw[removed]; ok {
			t.Errorf("the spec must not carry %q any more: %s", removed, b)
		}
	}
}

func TestBuildLabSpec_NoGatewayNoKey(t *testing.T) {
	web := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{web}})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["gateways"]; ok {
		t.Fatalf("a lab without a gateway has no gateways key: %s", b)
	}
}

func TestBuildLabSpec_TwoGatewaysOfOneKind(t *testing.T) {
	a, c := gatewayDevice("entry-a", exerciseModel.DeviceTypeVPNGateway), gatewayDevice("entry-b", exerciseModel.DeviceTypeVPNGateway)
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{a, c}})
	if err != nil {
		t.Fatal(err)
	}
	s := decode(t, b)
	if len(s.Gateways) != 2 || s.Gateways[0].Name != "entry-a" || s.Gateways[1].Name != "entry-b" {
		t.Fatalf("both VPN entries must reach the spec: %+v", s.Gateways)
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
			AddressRef: &exerciseModel.NetworkIPRef{Gateway: "vpn", Host: 10},
			GatewayRef: &exerciseModel.NetworkIPRef{Gateway: "vpn", Host: 1},
			Routes: []exerciseModel.Route{{DstRef: &exerciseModel.NetworkSubnetRef{Gateway: "internet"},
				ViaRef: &exerciseModel.NetworkIPRef{Gateway: "vpn", Host: 1}}},
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
	if ref := addr["addressRef"].(map[string]any); ref["gateway"] != "vpn" || ref["host"] != float64(10) {
		t.Fatalf("addressRef missing: %#v", addr)
	}
	if ref := addr["gatewayRef"].(map[string]any); ref["gateway"] != "vpn" || ref["host"] != float64(1) {
		t.Fatalf("gatewayRef missing: %#v", addr)
	}
	route := addr["routes"].([]any)[0].(map[string]any)
	if route["dstRef"].(map[string]any)["gateway"] != "internet" || route["viaRef"].(map[string]any)["host"] != float64(1) {
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

func TestBuildLabSpec_AccessAndPreset(t *testing.T) {
	dev := exerciseModel.Device{
		ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer,
		Image: "nginx", SecurityPreset: exerciseModel.SecurityPresetStandard,
		Access: []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 443, Protocol: "https"}},
	}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{dev}})
	if err != nil {
		t.Fatal(err)
	}
	d := decode(t, b).Devices[0]
	if d.SecurityPreset != "standard" || d.Image != "nginx" {
		t.Errorf("device meta wrong: %+v", d)
	}
	if len(d.Access) != 1 || d.Access[0].Kind != "http" || d.Access[0].Port != 443 || d.Access[0].Protocol != "https" {
		t.Errorf("access wrong: %+v", d.Access)
	}
}

func TestBuildLabSpec_ConnectionsResolveNamesAndGateways(t *testing.T) {
	web := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer}
	sw := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch}
	vpn := gatewayDevice("attack", exerciseModel.DeviceTypeVPNGateway)
	out := gatewayDevice("out", exerciseModel.DeviceTypeInternetGateway)
	ep := func(d exerciseModel.Device, iface string) exerciseModel.Endpoint {
		return exerciseModel.Endpoint{Kind: exerciseModel.EndpointDevice, DeviceID: d.ID, Interface: iface}
	}
	topo := exerciseModel.Topology{
		Devices: []exerciseModel.Device{web, sw, vpn, out},
		Connections: []exerciseModel.Connection{
			{Endpoints: []exerciseModel.Endpoint{ep(web, "eth0"), ep(sw, "GigabitEthernet0/1")}},
			{Endpoints: []exerciseModel.Endpoint{ep(vpn, "eth0"), ep(sw, "GigabitEthernet0/2")}},
			{Endpoints: []exerciseModel.Endpoint{ep(out, "eth0"), ep(web, "eth1")}},
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
	if conns[1].Endpoints[0].Device != "attack" || conns[1].Endpoints[0].Interface != "eth0" {
		t.Errorf("a gateway endpoint is the gateway's own name and its port: %+v", conns[1].Endpoints[0])
	}
	if conns[2].Endpoints[0].Device != "out" || conns[2].Endpoints[0].Interface != "eth0" {
		t.Errorf("an internet gateway endpoint is its own name too: %+v", conns[2].Endpoints[0])
	}
}

func TestBuildLabSpec_GatewayReferencesNameTheGateway(t *testing.T) {
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{
		gatewayDevice("attack", exerciseModel.DeviceTypeVPNGateway),
		{ID: uuid.Must(uuid.NewV7()), Name: "host", Type: exerciseModel.DeviceTypeContainer,
			Interfaces: []exerciseModel.Interface{{Name: "eth0", IP: exerciseModel.IPConfig{
				Type:       exerciseModel.IPConfigTypeStatic,
				AddressRef: &exerciseModel.NetworkIPRef{Gateway: "attack", Host: 10},
			}}}},
	}}
	encoded, _, err := BuildLabSpec(topo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"addressRef":{"gateway":"attack","host":10}`) {
		t.Fatalf("the reference must name the gateway, not a network: %s", encoded)
	}
	if strings.Contains(string(encoded), `"network"`) {
		t.Fatalf("no network key any more: %s", encoded)
	}
}

func TestBuildLabSpec_DeviceEnv(t *testing.T) {
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{
		{
			ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer,
			Bootstrap: exerciseModel.Bootstrap{Env: []exerciseModel.EnvVar{
				{Name: "FLAG", Value: "CTF{x}", Secret: true},
				{Name: "MODE", Value: "prod"},
			}},
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
	dev := exerciseModel.Device{
		Name:  "db",
		Type:  exerciseModel.DeviceTypeContainer,
		Image: "pg",
		Bootstrap: exerciseModel.Bootstrap{
			Env: []exerciseModel.EnvVar{{Name: "FLAG", Value: "secret-flag"}},
		},
		Persistence: &exerciseModel.DevicePersistence{Enabled: true, Debounce: "5s"},
	}
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

func TestBuildLabSpec_WaitReadyFile(t *testing.T) {
	on := exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx", WaitReadyFile: true}
	off := exerciseModel.Device{Name: "db", Type: exerciseModel.DeviceTypeContainer, Image: "pg"}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{on, off}})
	if err != nil {
		t.Fatal(err)
	}
	devices := decode(t, b).Devices
	if !devices[0].WaitReadyFile || devices[1].WaitReadyFile {
		t.Fatalf("waitReadyFile = %v / %v", devices[0].WaitReadyFile, devices[1].WaitReadyFile)
	}
	if !strings.Contains(string(b), `"waitReadyFile":true`) || strings.Count(string(b), "waitReadyFile") != 1 {
		t.Fatalf("only the device that asks carries the key: %s", b)
	}
}

// The access list goes out as it is in the document, one entry per kind; a terminal command is a one-item list.
func TestBuildLabSpec_AccessListWithEveryKind(t *testing.T) {
	dev := exerciseModel.Device{
		ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeNode, Image: "nginx",
		Access: []exerciseModel.Access{
			{Kind: exerciseModel.AccessHTTP, Port: 80},
			{Kind: exerciseModel.AccessTerminal, Command: "/bin/bash", User: "app"},
			{Kind: exerciseModel.AccessVNC, Port: 5900},
			{Kind: exerciseModel.AccessRDP, Port: 3389},
		},
	}
	b, _, err := BuildLabSpec(exerciseModel.Topology{Devices: []exerciseModel.Device{dev}})
	if err != nil {
		t.Fatal(err)
	}
	access := decode(t, b).Devices[0].Access
	want := []labAccess{
		{Kind: "http", Port: 80}, {Kind: "terminal", Command: []string{"/bin/bash"}, User: "app"},
		{Kind: "vnc", Port: 5900}, {Kind: "rdp", Port: 3389},
	}
	if !reflect.DeepEqual(access, want) {
		t.Fatalf("access = %+v, want %+v", access, want)
	}
}
