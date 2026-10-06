package exercise

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestTopologyDTORoundtripResourcesRoutes(t *testing.T) {
	input := exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Type: exerciseModel.DeviceTypeContainer,
		ResourcePreset: "medium",
		Interfaces: []exerciseModel.Interface{{Name: "eth0", IP: exerciseModel.IPConfig{
			Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.0.0.2/24"},
			Routes: []exerciseModel.Route{{Dst: "10.1.0.0/16", Via: "10.0.0.1"}},
		}}},
	}}}
	dto := topologyToDTO(input)
	got := dto.toDomain()
	if got.Devices[0].ResourcePreset != "medium" || got.Devices[0].Resources != nil {
		t.Fatalf("preset lost: %#v", got.Devices[0])
	}
	if !reflect.DeepEqual(got.Devices[0].Interfaces[0].IP.Routes, input.Devices[0].Interfaces[0].IP.Routes) {
		t.Fatalf("routes lost: %#v", got.Devices[0].Interfaces[0].IP.Routes)
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"ResourcePreset":"medium"`, `"Routes"`, `"Dst":"10.1.0.0/16"`, `"Via":"10.0.0.1"`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("response JSON missing %s: %s", field, encoded)
		}
	}
	legacy, err := json.Marshal(topologyToDTO(exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "old", Type: exerciseModel.DeviceTypeContainer}}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), `"Resources"`) || strings.Contains(string(legacy), `"ResourcePreset"`) || strings.Contains(string(legacy), `"Routes"`) {
		t.Errorf("legacy device gained optional settings: %s", legacy)
	}
}

// A custom Resources value still reaches the domain, where the topology validation refuses it (there is no
// custom size); it is never returned.
func TestTopologyDTOAcceptsResourcesRoutes(t *testing.T) {
	var dto topologyDTO
	raw := `{"Devices":[{"Name":"web","Type":"container","Resources":{"CPURequest":"250m"},"Interfaces":[{"Name":"eth0","IP":{"Type":"static","Addresses":["10.0.0.2/24"],"Routes":[{"Dst":"10.1.0.0/16","Via":"10.0.0.1"}]}}]}]}`
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		t.Fatal(err)
	}
	device := dto.toDomain().Devices[0]
	if device.Resources == nil || device.Resources.CPURequest != "250m" || len(device.Interfaces[0].IP.Routes) != 1 {
		t.Fatalf("request settings lost: %+v", device)
	}
	back, err := json.Marshal(topologyToDTO(dto.toDomain()))
	if err != nil || strings.Contains(string(back), `"Resources"`) {
		t.Fatalf("custom resources are never returned: %v %s", err, back)
	}
}

func TestTopologyDTOAddressReferencesRoundTrip(t *testing.T) {
	const request = `{"Devices":[{"Name":"web","Type":"container","Interfaces":[{"Name":"eth0","IP":{"Type":"static","AddressRef":{"Network":"vpn","Host":10},"GatewayRef":{"Network":"vpn","Host":1},"Routes":[{"DstRef":{"Network":"internet"},"ViaRef":{"Network":"vpn","Host":1}}]}}]}]}`
	var dto topologyDTO
	if err := json.Unmarshal([]byte(request), &dto); err != nil {
		t.Fatal(err)
	}
	ip := dto.toDomain().Devices[0].Interfaces[0].IP
	if ip.AddressRef == nil || ip.AddressRef.Network != "vpn" || ip.AddressRef.Host != 10 ||
		ip.GatewayRef == nil || ip.GatewayRef.Host != 1 ||
		len(ip.Routes) != 1 || ip.Routes[0].DstRef == nil || ip.Routes[0].DstRef.Network != "internet" ||
		ip.Routes[0].ViaRef == nil || ip.Routes[0].ViaRef.Host != 1 {
		t.Fatalf("request address references lost: %+v", ip)
	}
	response, err := json.Marshal(topologyToDTO(dto.toDomain()))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"AddressRef":{"Network":"vpn","Host":10}`, `"GatewayRef":{"Network":"vpn","Host":1}`, `"DstRef":{"Network":"internet"}`, `"ViaRef":{"Network":"vpn","Host":1}`} {
		if !strings.Contains(string(response), field) {
			t.Errorf("response missing %s: %s", field, response)
		}
	}
	if strings.Contains(string(response), `"Addresses"`) || strings.Contains(string(response), `"Gateway":`) {
		t.Errorf("response contains literal values for references: %s", response)
	}
}

func TestTopologyDTODHCPSettingsRoundTrip(t *testing.T) {
	const request = `{"VPN":{"Enabled":true,"DHCP":true,"DHCPRanges":[{"Start":2,"End":50},{"Start":100,"End":150}]},"Internet":{"Enabled":true,"DHCP":true,"DHCPRanges":[{"Start":20,"End":80}],"DNS":"1.1.1.1"}}`
	var dto topologyDTO
	if err := json.Unmarshal([]byte(request), &dto); err != nil {
		t.Fatal(err)
	}
	topology := dto.toDomain()
	if len(topology.VPN.DHCPRanges) != 2 || topology.VPN.DHCPRanges[1].Start != 100 || topology.Internet.DNS != "1.1.1.1" {
		t.Fatalf("DHCP settings lost: %+v", topology)
	}
	response, err := json.Marshal(topologyToDTO(topology))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"DHCPRanges":[{"Start":2,"End":50},{"Start":100,"End":150}]`, `"DNS":"1.1.1.1"`} {
		if !strings.Contains(string(response), field) {
			t.Fatalf("response missing %s: %s", field, response)
		}
	}
}

func TestTopologyDTOCarriesDevicePersistenceBothWays(t *testing.T) {
	in := topologyDTO{Devices: []deviceDTO{
		{Name: "db", Type: "container", Persistence: &persistenceDTO{Enabled: true, Debounce: "5s"}},
		{Name: "web", Type: "container"},
	}}
	domain := in.toDomain()
	if p := domain.Devices[0].Persistence; p == nil || !p.Enabled || p.Debounce != "5s" {
		t.Fatalf("domain persistence = %+v", domain.Devices[0].Persistence)
	}
	if domain.Devices[1].Persistence != nil {
		t.Fatalf("a device without persistence stays without: %+v", domain.Devices[1].Persistence)
	}
	out := topologyToDTO(domain)
	if p := out.Devices[0].Persistence; p == nil || !p.Enabled || p.Debounce != "5s" || out.Devices[1].Persistence != nil {
		t.Fatalf("dto persistence = %+v / %+v", out.Devices[0].Persistence, out.Devices[1].Persistence)
	}
	raw, err := json.Marshal(out.Devices[1])
	if err != nil || strings.Contains(string(raw), "Persistence") {
		t.Fatalf("an absent persistence is omitted: %s %v", raw, err)
	}
}
