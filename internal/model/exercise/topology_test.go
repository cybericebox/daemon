package exerciseModel_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// topo builder helpers keep the table cases readable.
func dev(name string, typ exerciseModel.DeviceType, ifaces ...string) exerciseModel.Device {
	d := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: typ}
	if typ == exerciseModel.DeviceTypeContainer {
		d.Image = "img:latest"
		for _, n := range ifaces {
			d.Interfaces = append(d.Interfaces, exerciseModel.Interface{
				Name: n,
				IP:   exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.0.0.2/24"}},
			})
		}
	}
	return d
}

func TestValidateStructure_TopologyOperatorParity(t *testing.T) {
	const staticIP = `{"type":"static","addresses":["10.0.0.2/24"]}`
	const dhcpIP = `{"type":"dhcp"}`
	cases := []struct {
		name    string
		device  string
		wantErr error
	}{
		{"legacy container", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":` + staticIP + `}]}`, nil},
		{"vm unsupported", `{"name":"web","type":"vm","interfaces":[{"name":"eth0","ip":` + staticIP + `}]}`, exerciseModel.ErrDeviceTypeInvalid.Err()},
		{"two static addresses", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":{"type":"static","addresses":["10.0.0.2/24","10.0.0.3/24"]}}]}`, exerciseModel.ErrDeviceStaticAddressCountInvalid.Err()},
		{"custom resources are refused", `{"name":"web","type":"container","resources":{"cpu_request":"250m","memory_limit":"256Mi"},"interfaces":[{"name":"eth0","ip":` + dhcpIP + `}]}`, exerciseModel.ErrDeviceCustomResources.Err()},
		{"forwarding resources", `{"name":"sw","type":"unmanaged-switch","resources":{"cpu_request":"250m"}}`, exerciseModel.ErrDeviceTypeInvalid.Err()},
		{"valid ipv4 route", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":{"type":"static","addresses":["10.0.0.2/24"],"routes":[{"dst":"10.1.0.0/16","via":"10.0.0.1"}]}}]}`, nil},
		{"valid ipv6 route", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":{"type":"static","addresses":["2001:db8::2/64"],"routes":[{"dst":"2001:db8:1::/64","via":"2001:db8::1"}]}}]}`, nil},
		{"route bad destination", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":{"type":"static","addresses":["10.0.0.2/24"],"routes":[{"dst":"bad","via":"10.0.0.1"}]}}]}`, exerciseModel.ErrDeviceRouteInvalid.Err()},
		{"route bad next hop", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":{"type":"static","addresses":["10.0.0.2/24"],"routes":[{"dst":"10.1.0.0/16","via":"bad"}]}}]}`, exerciseModel.ErrDeviceRouteInvalid.Err()},
		{"route mixed family", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":{"type":"static","addresses":["10.0.0.2/24"],"routes":[{"dst":"2001:db8:1::/64","via":"10.0.0.1"}]}}]}`, exerciseModel.ErrDeviceRouteInvalid.Err()},
		{"route on dhcp", `{"name":"web","type":"container","interfaces":[{"name":"eth0","ip":{"type":"dhcp","routes":[{"dst":"10.1.0.0/16","via":"10.0.0.1"}]}}]}`, exerciseModel.ErrDeviceRouteInvalid.Err()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d exerciseModel.Device
			if err := json.Unmarshal([]byte(tc.device), &d); err != nil {
				t.Fatal(err)
			}
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{d}})}}
			err := version.ValidateStructure()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("validation error = %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateStructure_AddressReferences(t *testing.T) {
	base := func() exerciseModel.Topology {
		host := dev("host", exerciseModel.DeviceTypeContainer, "eth0")
		host.Interfaces[0].IP = exerciseModel.IPConfig{
			Type:       exerciseModel.IPConfigTypeStatic,
			AddressRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 10},
			GatewayRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 1},
			Routes: []exerciseModel.Route{{
				DstRef: &exerciseModel.NetworkSubnetRef{Network: "internet"},
				ViaRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 1},
			}},
		}
		return exerciseModel.Topology{
			VPN: exerciseModel.NetworkSpec{Enabled: true}, Internet: exerciseModel.NetworkSpec{Enabled: true},
			Devices: []exerciseModel.Device{host},
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*exerciseModel.Topology)
		want error
	}{
		{"valid", func(*exerciseModel.Topology) {}, nil},
		{"literal plus ref", func(t *exerciseModel.Topology) { t.Devices[0].Interfaces[0].IP.Addresses = []string{"10.0.0.10/24"} }, exerciseModel.ErrDeviceAddressRefInvalid.Err()},
		{"gateway host .1", func(t *exerciseModel.Topology) { t.Devices[0].Interfaces[0].IP.AddressRef.Host = 1 }, exerciseModel.ErrDeviceAddressRefInvalid.Err()},
		{"broadcast host .255", func(t *exerciseModel.Topology) { t.Devices[0].Interfaces[0].IP.AddressRef.Host = 255 }, exerciseModel.ErrDeviceAddressRefInvalid.Err()},
		{"disabled source", func(t *exerciseModel.Topology) { t.VPN.Enabled = false }, exerciseModel.ErrDeviceAddressRefInvalid.Err()},
		{"vpn address outside DHCP ranges", func(t *exerciseModel.Topology) {
			t.VPN.DHCP = true
			t.VPN.DHCPRanges = []exerciseModel.DHCPRange{{Start: 20, End: 100}}
		}, nil},
		{"internet address with DHCP enabled", func(t *exerciseModel.Topology) {
			t.Devices[0].Interfaces[0].IP.AddressRef.Network = "internet"
			t.Internet.DHCP = true
			t.Internet.DHCPRanges = []exerciseModel.DHCPRange{{Start: 2, End: 9}}
		}, nil},
		{"vpn address inside DHCP range is a warning only", func(t *exerciseModel.Topology) {
			t.VPN.DHCP = true
			t.VPN.DHCPRanges = []exerciseModel.DHCPRange{{Start: 2, End: 10}}
		}, nil},
		{"vpn DHCP needs an explicit range", func(t *exerciseModel.Topology) { t.VPN.DHCP = true }, exerciseModel.ErrNetworkDHCPInvalid.Err()},
		{"nonstatic ref", func(t *exerciseModel.Topology) { t.Devices[0].Interfaces[0].IP.Type = exerciseModel.IPConfigTypeDHCP }, exerciseModel.ErrDeviceAddressRefInvalid.Err()},
		{"route mixed family", func(t *exerciseModel.Topology) {
			t.Devices[0].Interfaces[0].IP.Routes[0].DstRef = nil
			t.Devices[0].Interfaces[0].IP.Routes[0].Dst = "2001:db8::/64"
		}, exerciseModel.ErrDeviceAddressRefInvalid.Err()},
		{"duplicate host", func(t *exerciseModel.Topology) {
			second := dev("second", exerciseModel.DeviceTypeContainer, "eth0")
			second.Interfaces[0].IP = exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic,
				AddressRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 10}}
			t.Devices = append(t.Devices, second)
		}, exerciseModel.ErrDeviceAddressRefInvalid.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			topo := base()
			tc.edit(&topo)
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(topo)}}
			got := version.ValidateStructure()
			if !errors.Is(got, tc.want) {
				t.Fatalf("ValidateStructure() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateStructure_DHCPSettings(t *testing.T) {
	for _, tc := range []struct {
		name string
		topo exerciseModel.Topology
		want error
	}{
		{"multiple disjoint ranges and DNS", exerciseModel.Topology{Internet: exerciseModel.NetworkSpec{Enabled: true, DHCP: true, DNS: "1.1.1.1", DHCPRanges: []exerciseModel.DHCPRange{{Start: 2, End: 50}, {Start: 100, End: 200}}}}, nil},
		{"enabled DHCP without range", exerciseModel.Topology{VPN: exerciseModel.NetworkSpec{Enabled: true, DHCP: true}}, exerciseModel.ErrNetworkDHCPInvalid.Err()},
		{"overlapping ranges", exerciseModel.Topology{Internet: exerciseModel.NetworkSpec{DHCPRanges: []exerciseModel.DHCPRange{{Start: 2, End: 50}, {Start: 50, End: 100}}}}, exerciseModel.ErrNetworkDHCPInvalid.Err()},
		{"range below host two", exerciseModel.Topology{VPN: exerciseModel.NetworkSpec{DHCPRanges: []exerciseModel.DHCPRange{{Start: 1, End: 20}}}}, exerciseModel.ErrNetworkDHCPInvalid.Err()},
		{"bad internet DNS", exerciseModel.Topology{Internet: exerciseModel.NetworkSpec{DNS: "not-an-ip"}}, exerciseModel.ErrNetworkDHCPInvalid.Err()},
		{"VPN DNS forbidden", exerciseModel.Topology{VPN: exerciseModel.NetworkSpec{DNS: "1.1.1.1"}}, exerciseModel.ErrNetworkDHCPInvalid.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(tc.topo)}}
			if err := version.ValidateStructure(); !errors.Is(err, tc.want) {
				t.Fatalf("validation error = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestValidateStructure_NonContainerDisplayNames(t *testing.T) {
	for _, typ := range []exerciseModel.DeviceType{exerciseModel.DeviceTypeUnmanagedSwitch, exerciseModel.DeviceTypeHub} {
		t.Run(string(typ), func(t *testing.T) {
			device := dev("Коммутатор №1 / DMZ", typ)
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{device}})}}
			if err := version.ValidateStructure(); err != nil {
				t.Fatalf("display name rejected: %v", err)
			}
			device.Name = "   "
			version.Variants[0].Topology.Devices[0] = device
			if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrDeviceDisplayNameInvalid.Err()) {
				t.Fatalf("blank display name error = %v", err)
			}
		})
	}
	container := dev("Имя контейнера", exerciseModel.DeviceTypeContainer)
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{container}})}}
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrDeviceNameInvalid.Err()) {
		t.Fatalf("container still needs a DNS label: %v", err)
	}
	for _, name := range []string{"Web", "web_1", "-web", "web-"} {
		version.Variants[0].Topology.Devices[0] = dev(name, exerciseModel.DeviceTypeContainer)
		if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrDeviceNameInvalid.Err()) {
			t.Fatalf("%q must be an invalid DNS label: %v", name, err)
		}
	}
	version.Variants[0].Topology.Devices[0] = dev(strings.Repeat("d", exerciseModel.MaxDeviceNameLen+1), exerciseModel.DeviceTypeContainer)
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrDeviceNameTooLong.Err()) {
		t.Fatalf("36 chars must be too long: %v", err)
	}
	version.Variants[0].Topology.Devices[0] = dev(strings.Repeat("d", exerciseModel.MaxDeviceNameLen), exerciseModel.DeviceTypeContainer)
	if err := version.ValidateStructure(); err != nil {
		t.Fatalf("35 chars must pass: %v", err)
	}
}

func TestValidateStructure_GatewayEndpointPort(t *testing.T) {
	for _, kind := range []exerciseModel.EndpointKind{exerciseModel.EndpointVPN, exerciseModel.EndpointInternet} {
		for _, tc := range []struct {
			name, port string
			wantErr    error
		}{
			{"empty", "", exerciseModel.ErrConnectionEndpointsInvalid.Err()},
			{"named port", "eth0", nil},
			{"other port", "eth1", exerciseModel.ErrConnectionEndpointsInvalid.Err()},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				host := dev("host", exerciseModel.DeviceTypeContainer, "eth0")
				topo := exerciseModel.Topology{
					VPN: exerciseModel.NetworkSpec{Enabled: true}, Internet: exerciseModel.NetworkSpec{Enabled: true},
					Devices: []exerciseModel.Device{host},
					Connections: []exerciseModel.Connection{conn(
						exerciseModel.Endpoint{Kind: kind, Interface: tc.port}, devEP(host, "eth0"),
					)},
				}
				version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(topo)}}
				err := version.ValidateStructure()
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("gateway %s port %q: got %v, want %v", kind, tc.port, err, tc.wantErr)
				}
			})
		}
	}
}

func TestValidateStructure_FlagTargetCannotOverwriteDeviceEnv(t *testing.T) {
	device := dev("web", exerciseModel.DeviceTypeContainer, "eth0")
	device.EnvVars = []exerciseModel.EnvVar{{Name: "FLAG", Value: "static-value"}}
	task := validTask()
	task.LinkedDeviceID = uuid.NullUUID{UUID: device.ID, Valid: true}
	task.DeviceFlagVar = "FLAG"
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{device}}, task)}}
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrFlagEnvironmentConflict.Err()) {
		t.Fatalf("flag target conflict = %v", err)
	}
}

func TestValidateStructure_TasksCannotShareOneFlagTarget(t *testing.T) {
	device := dev("web", exerciseModel.DeviceTypeContainer, "eth0")
	first := validTask()
	first.LinkedDeviceID = uuid.NullUUID{UUID: device.ID, Valid: true}
	first.DeviceFlagVar = "FLAG"
	second := validTask()
	second.Name = "Find the other flag"
	second.LinkedDeviceID = first.LinkedDeviceID
	second.DeviceFlagVar = first.DeviceFlagVar
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{device}}, first, second)}}
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrFlagEnvironmentConflict.Err()) {
		t.Fatalf("duplicate task target conflict = %v", err)
	}
}

func TestValidateStructure_SameFlagVarOnDifferentDevices(t *testing.T) {
	firstDevice := dev("web", exerciseModel.DeviceTypeContainer, "eth0")
	secondDevice := dev("api", exerciseModel.DeviceTypeContainer, "eth0")
	first := validTask()
	first.LinkedDeviceID = uuid.NullUUID{UUID: firstDevice.ID, Valid: true}
	first.DeviceFlagVar = "FLAG"
	second := validTask()
	second.Name = "Find the other flag"
	second.LinkedDeviceID = uuid.NullUUID{UUID: secondDevice.ID, Valid: true}
	second.DeviceFlagVar = "FLAG"
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{firstDevice, secondDevice}}, first, second)}}
	if err := version.ValidateStructure(); err != nil {
		t.Fatalf("independent device variables must remain valid: %v", err)
	}
}

func devEP(d exerciseModel.Device, iface string) exerciseModel.Endpoint {
	return exerciseModel.Endpoint{Kind: exerciseModel.EndpointDevice, DeviceID: d.ID, Interface: iface}
}

func conn(a, b exerciseModel.Endpoint) exerciseModel.Connection {
	return exerciseModel.Connection{Endpoints: []exerciseModel.Endpoint{a, b}}
}

func publishVariant(topo exerciseModel.Topology, tasks ...exerciseModel.Task) exerciseModel.Variant {
	if len(tasks) == 0 {
		tasks = []exerciseModel.Task{validTask()}
	}
	return exerciseModel.Variant{Tasks: tasks, Topology: topo}
}

func TestValidateForPublish_Graph(t *testing.T) {
	web := dev("web", exerciseModel.DeviceTypeContainer, "eth0", "eth1")
	db := dev("db", exerciseModel.DeviceTypeContainer, "eth0")
	sw1 := dev("sw1", exerciseModel.DeviceTypeUnmanagedSwitch)
	vpnEP := exerciseModel.Endpoint{Kind: exerciseModel.EndpointVPN, Interface: "eth0"}

	cases := []struct {
		name    string
		topo    exerciseModel.Topology
		wantErr error
	}{
		{
			"ok: web-sw-db with vpn",
			exerciseModel.Topology{
				VPN:     exerciseModel.NetworkSpec{Enabled: true},
				Devices: []exerciseModel.Device{web, db, sw1},
				Connections: []exerciseModel.Connection{
					conn(devEP(web, "eth0"), devEP(sw1, "GigabitEthernet0/1")),
					conn(devEP(db, "eth0"), devEP(sw1, "GigabitEthernet0/2")),
					conn(vpnEP, devEP(sw1, "GigabitEthernet0/3")),
				},
			},
			nil,
		},
		{
			"duplicate device names",
			exerciseModel.Topology{Devices: []exerciseModel.Device{web, dev("web", exerciseModel.DeviceTypeContainer, "eth0")}},
			exerciseModel.ErrDeviceNameDuplicate.Err(),
		},
		{
			"endpoint references unknown device",
			exerciseModel.Topology{
				Devices:     []exerciseModel.Device{web},
				Connections: []exerciseModel.Connection{conn(devEP(web, "eth0"), devEP(db, "eth0"))},
			},
			exerciseModel.ErrEndpointUnresolved.Err(),
		},
		{
			"endpoint references unknown interface",
			exerciseModel.Topology{
				Devices:     []exerciseModel.Device{web, db},
				Connections: []exerciseModel.Connection{conn(devEP(web, "eth9"), devEP(db, "eth0"))},
			},
			exerciseModel.ErrEndpointUnresolved.Err(),
		},
		{
			"vpn endpoint while vpn disabled",
			exerciseModel.Topology{
				Devices:     []exerciseModel.Device{web},
				Connections: []exerciseModel.Connection{conn(vpnEP, devEP(web, "eth0"))},
			},
			exerciseModel.ErrVPNDisabled.Err(),
		},
		{
			"switch endpoint must name a valid port",
			exerciseModel.Topology{
				Devices:     []exerciseModel.Device{web, sw1},
				Connections: []exerciseModel.Connection{conn(devEP(web, "eth0"), devEP(sw1, "p1"))},
			},
			exerciseModel.ErrForwardingPortInvalid.Err(),
		},
		{
			"port used twice",
			exerciseModel.Topology{
				Devices: []exerciseModel.Device{web, db, sw1},
				Connections: []exerciseModel.Connection{
					conn(devEP(web, "eth0"), devEP(sw1, "GigabitEthernet0/1")),
					conn(devEP(web, "eth0"), devEP(db, "eth0")),
				},
			},
			exerciseModel.ErrPortInUse.Err(),
		},
		{
			"two vpn links",
			exerciseModel.Topology{
				VPN:     exerciseModel.NetworkSpec{Enabled: true},
				Devices: []exerciseModel.Device{web, db},
				Connections: []exerciseModel.Connection{
					conn(vpnEP, devEP(web, "eth0")),
					conn(vpnEP, devEP(db, "eth0")),
				},
			},
			exerciseModel.ErrVPNGatewayInUse.Err(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(tc.topo)}}
			err := v.ValidateForPublish()
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateForPublish_AddressRefReachability(t *testing.T) {
	host := dev("host", exerciseModel.DeviceTypeContainer, "eth0")
	host.Interfaces[0].IP = exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic,
		AddressRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 10}}
	switchNode := dev("sw", exerciseModel.DeviceTypeUnmanagedSwitch)
	vpn := exerciseModel.Endpoint{Kind: exerciseModel.EndpointVPN, Interface: "eth0"}
	internet := exerciseModel.Endpoint{Kind: exerciseModel.EndpointInternet, Interface: "eth0"}
	for _, tc := range []struct {
		name        string
		connections []exerciseModel.Connection
		want        error
	}{
		{"direct VPN", []exerciseModel.Connection{conn(devEP(host, "eth0"), vpn)}, nil},
		{"through switch", []exerciseModel.Connection{
			conn(devEP(host, "eth0"), devEP(switchNode, "GigabitEthernet0/1")),
			conn(devEP(switchNode, "GigabitEthernet0/2"), vpn),
		}, nil},
		{"wrong gateway", []exerciseModel.Connection{conn(devEP(host, "eth0"), internet)}, exerciseModel.ErrDeviceAddressRefUnreachable.Err()},
		{"unconnected", nil, exerciseModel.ErrDeviceAddressRefUnreachable.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			topo := exerciseModel.Topology{
				VPN: exerciseModel.NetworkSpec{Enabled: true}, Internet: exerciseModel.NetworkSpec{Enabled: true},
				Devices: []exerciseModel.Device{host, switchNode}, Connections: tc.connections,
			}
			variant := publishVariant(topo)
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}
			if err := version.ValidateForPublish(); !errors.Is(err, tc.want) {
				t.Fatalf("publish = %v, want %v", err, tc.want)
			}
			if err := variant.ValidateTopologyForDeploy(); !errors.Is(err, tc.want) {
				t.Fatalf("deploy = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateForPublish_ForwardingPorts(t *testing.T) {
	sw := dev("sw", exerciseModel.DeviceTypeUnmanagedSwitch)
	a := dev("a", exerciseModel.DeviceTypeContainer, "eth0")
	b := dev("b", exerciseModel.DeviceTypeContainer, "eth0")
	for _, tc := range []struct {
		name, first, second string
		wantValid           bool
		wantBusy            bool
	}{
		{"valid distinct", "GigabitEthernet0/1", "GigabitEthernet0/48", true, false},
		{"missing", "", "GigabitEthernet0/2", false, false},
		{"zero", "GigabitEthernet0/0", "GigabitEthernet0/2", false, false},
		{"outside range", "GigabitEthernet0/49", "GigabitEthernet0/2", false, false},
		{"reused", "GigabitEthernet0/1", "GigabitEthernet0/1", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := publishVariant(exerciseModel.Topology{
				Devices: []exerciseModel.Device{sw, a, b},
				Connections: []exerciseModel.Connection{
					conn(devEP(sw, tc.first), devEP(a, "eth0")),
					conn(devEP(sw, tc.second), devEP(b, "eth0")),
				},
			})
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{v}}
			err := version.ValidateForPublish()
			if tc.wantValid && err != nil || !tc.wantValid && err == nil {
				t.Fatalf("validation = %v, want valid %v", err, tc.wantValid)
			}
			if tc.wantBusy && !errors.Is(err, exerciseModel.ErrPortInUse.Err()) {
				t.Fatalf("reused port error = %v", err)
			}
		})
	}
}

func TestValidateForPublish_FlagDeviceLink(t *testing.T) {
	web := dev("web", exerciseModel.DeviceTypeContainer, "eth0")
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{web}}

	linked := validTask()
	linked.LinkedDeviceID = uuid.NullUUID{UUID: web.ID, Valid: true}
	linked.DeviceFlagVar = "FLAG"
	okV := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(topo, linked)}}
	if err := okV.ValidateForPublish(); err != nil {
		t.Fatalf("linked flag to existing device must pass: %v", err)
	}

	ghost := validTask()
	ghost.LinkedDeviceID = uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	ghost.DeviceFlagVar = "FLAG"
	badV := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(topo, ghost)}}
	if !errors.Is(badV.ValidateForPublish(), exerciseModel.ErrFlagDeviceUnresolved.Err()) {
		t.Fatal("flag linked to a ghost device must fail")
	}
}

func TestValidateForPublish_LinkedDeviceAcceptsMixedCandidates(t *testing.T) {
	web := dev("web", exerciseModel.DeviceTypeContainer, "eth0")
	linked := validTask()
	linked.LinkedDeviceID = uuid.NullUUID{UUID: web.ID, Valid: true}
	linked.DeviceFlagVar = "FLAG"
	linked.Flag = []string{"ICE{fixed}", `template:ICE{\d}`}
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{web}}, linked)}}
	if err := version.ValidateForPublish(); err != nil {
		t.Fatalf("linked task should accept mixed candidates: %v", err)
	}
}

func TestValidateStructure_SecurityPreset(t *testing.T) {
	base := func(preset exerciseModel.SecurityPreset) exerciseModel.Topology {
		d := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "img:latest", SecurityPreset: preset}
		return exerciseModel.Topology{Devices: []exerciseModel.Device{d}}
	}
	cases := []struct {
		name    string
		topo    exerciseModel.Topology
		wantErr error
	}{
		{"empty preset defaults ok", base(""), nil},
		{"basic ok", base(exerciseModel.SecurityPresetBasic), nil},
		{"net ok", base(exerciseModel.SecurityPresetNet), nil},
		{"debug ok", base(exerciseModel.SecurityPresetDebug), nil},
		{"bogus preset", base("root"), exerciseModel.ErrDeviceSecurityPresetInvalid.Err()},
		{
			"forwarding device must not set a preset",
			func() exerciseModel.Topology {
				sw := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch, SecurityPreset: exerciseModel.SecurityPresetNet}
				return exerciseModel.Topology{Devices: []exerciseModel.Device{sw}}
			}(),
			exerciseModel.ErrDeviceTypeInvalid.Err(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(tc.topo)}}
			err := v.ValidateStructure()
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// devWithInterface builds a one-device topology whose single interface is set
// to iface, for TestValidateStructure_Interface's per-item rule cases.
func devWithInterface(iface exerciseModel.Interface) exerciseModel.Topology {
	d := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "img:latest"}
	d.Interfaces = []exerciseModel.Interface{iface}
	return exerciseModel.Topology{Devices: []exerciseModel.Device{d}}
}

func TestValidateStructure_Interface(t *testing.T) {
	cases := []struct {
		name    string
		iface   exerciseModel.Interface
		wantErr error
	}{
		{
			"static ok with mac and gateway",
			exerciseModel.Interface{
				Name: "eth0", MAC: "02:00:00:00:00:01",
				IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.0.0.2/24"}, Gateway: "10.0.0.1"},
			},
			nil,
		},
		{"dhcp ok, no addresses", exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCP}}, nil},
		{"dhcp-preset ok, no addresses", exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCPPreset}}, nil},
		{
			"dhcp-preset with addresses set",
			exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCPPreset, Addresses: []string{"10.0.0.2/24"}}},
			exerciseModel.ErrDeviceInterfaceInvalid.Err(),
		},
		{"none ok, no addresses", exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeNone}}, nil},
		{"blank name", exerciseModel.Interface{Name: "", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeNone}}, exerciseModel.ErrDeviceInterfaceInvalid.Err()},
		{"bad ip type", exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: "bogus"}}, exerciseModel.ErrDeviceInterfaceInvalid.Err()},
		{
			"malformed mac",
			exerciseModel.Interface{Name: "eth0", MAC: "not-a-mac", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeNone}},
			exerciseModel.ErrDeviceInterfaceInvalid.Err(),
		},
		{
			"64-bit EUI MAC rejected (not 48-bit)",
			exerciseModel.Interface{Name: "eth0", MAC: "02:00:00:00:00:00:00:01", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeNone}},
			exerciseModel.ErrDeviceInterfaceInvalid.Err(),
		},
		{
			"static without addresses",
			exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic}},
			exerciseModel.ErrDeviceStaticAddressCountInvalid.Err(),
		},
		{
			"static with malformed cidr",
			exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.0.0.2"}}},
			exerciseModel.ErrDeviceInterfaceInvalid.Err(),
		},
		{
			"dhcp with addresses set",
			exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCP, Addresses: []string{"10.0.0.2/24"}}},
			exerciseModel.ErrDeviceInterfaceInvalid.Err(),
		},
		{
			"gateway malformed",
			exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.0.0.2/24"}, Gateway: "not-an-ip"}},
			exerciseModel.ErrDeviceInterfaceInvalid.Err(),
		},
		{
			"gateway on non-static interface",
			exerciseModel.Interface{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCP, Gateway: "10.0.0.1"}},
			exerciseModel.ErrDeviceInterfaceInvalid.Err(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(devWithInterface(tc.iface))}}
			err := v.ValidateStructure()
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateStructure_DevicePersistence(t *testing.T) {
	validate := func(device exerciseModel.Device) error {
		version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{Devices: []exerciseModel.Device{device}})}}
		return version.ValidateStructure()
	}
	container := dev("db", exerciseModel.DeviceTypeContainer)
	for _, debounce := range []string{"", "5s", "1s", "10m", "24h"} {
		container.Persistence = &exerciseModel.DevicePersistence{Enabled: true, Debounce: debounce}
		if err := validate(container); err != nil {
			t.Fatalf("debounce %q rejected: %v", debounce, err)
		}
	}
	for _, debounce := range []string{"fast", "0s", "500ms", "-5s", "25h", "5"} {
		container.Persistence = &exerciseModel.DevicePersistence{Enabled: true, Debounce: debounce}
		if err := validate(container); !errors.Is(err, exerciseModel.ErrDevicePersistenceInvalid.Err()) {
			t.Fatalf("debounce %q must be invalid: %v", debounce, err)
		}
	}
	for _, typ := range []exerciseModel.DeviceType{exerciseModel.DeviceTypeUnmanagedSwitch, exerciseModel.DeviceTypeHub} {
		forwarding := dev("Switch", typ)
		forwarding.Persistence = &exerciseModel.DevicePersistence{Enabled: true}
		if err := validate(forwarding); !errors.Is(err, exerciseModel.ErrDevicePersistenceInvalid.Err()) {
			t.Fatalf("%s must not keep state: %v", typ, err)
		}
	}
}
