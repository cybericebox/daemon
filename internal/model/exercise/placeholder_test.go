package exerciseModel_test

import (
	"errors"
	"strings"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestValidateForPublish_Placeholders(t *testing.T) {
	webExt := dev("web", exerciseModel.DeviceTypeContainer, "eth0")
	webExt.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 8080, Protocol: "http"}, {Kind: exerciseModel.AccessTerminal}, {Kind: exerciseModel.AccessVNC, Port: 5900}}
	plain := dev("plain", exerciseModel.DeviceTypeContainer, "eth0")

	base := exerciseModel.Topology{
		Devices: []exerciseModel.Device{webExt, plain, gw("vpn", exerciseModel.DeviceTypeVPNGateway)},
	}
	noVPN := exerciseModel.Topology{Devices: []exerciseModel.Device{webExt, plain}}

	cases := []struct {
		name    string
		topo    exerciseModel.Topology
		ph      exerciseModel.Placeholder
		wantErr error
	}{
		{"gateway subnet ok", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderGatewaySubnet, Gateway: "vpn"}, nil},
		{"gateway subnet without a gateway", noVPN, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderGatewaySubnet, Gateway: "vpn"}, exerciseModel.ErrPlaceholderNode.Err()},
		{"gateway subnet of an unknown gateway", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderGatewaySubnet, Gateway: "internet"}, exerciseModel.ErrPlaceholderNode.Err()},
		{"gateway subnet of a node", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderGatewaySubnet, Gateway: "web"}, exerciseModel.ErrPlaceholderNode.Err()},
		{"gateway subnet without a name", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderGatewaySubnet}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"ip gateway ok", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "gateway", Gateway: "vpn", LastOctet: 10}, nil},
		{"ip gateway of an unknown gateway", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "gateway", Gateway: "internet", LastOctet: 10}, exerciseModel.ErrPlaceholderNode.Err()},
		{"ip gateway without a name", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "gateway", LastOctet: 10}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"the removed vpn reference", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "vpn", LastOctet: 10}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"ip static ok", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "static", Octets1to3: "10.10.30", LastOctet: 7}, nil},
		{"ip static missing octets", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "static", LastOctet: 7}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"ip static invalid octets", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "static", Octets1to3: "10.999.30", LastOctet: 7}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"ip unknown reference", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "wat"}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"ip bad octet", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "gateway", Gateway: "vpn", LastOctet: 256}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"external link ok", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "web"}, nil},
		{"external link unknown device", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "ghost"}, exerciseModel.ErrPlaceholderNode.Err()},
		{"external link device without an http entry", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "plain"}, exerciseModel.ErrPlaceholderNode.Err()},
		{"external link to the console", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "web", Access: exerciseModel.AccessTerminal}, nil},
		{"external link to vnc validates", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "web", Access: exerciseModel.AccessVNC}, nil},
		{"external link to a missing rdp entry", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "web", Access: exerciseModel.AccessRDP}, exerciseModel.ErrPlaceholderNode.Err()},
		{"external link to a missing console", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "plain", Access: exerciseModel.AccessTerminal}, exerciseModel.ErrPlaceholderNode.Err()},
		{"external link unknown access kind", base, exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "web", Access: "ssh"}, exerciseModel.ErrPlaceholderInvalid.Err()},
		{"unknown kind", base, exerciseModel.Placeholder{Kind: "bogus"}, exerciseModel.ErrPlaceholderInvalid.Err()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := validTask()
			task.Placeholders = []exerciseModel.Placeholder{tc.ph}
			v := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(tc.topo, task)}}
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

func TestValidateForPublish_IPLinkPlaceholder(t *testing.T) {
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{gw("vpn", exerciseModel.DeviceTypeVPNGateway)}}
	link := func(mutate func(*exerciseModel.Placeholder)) exerciseModel.Placeholder {
		p := exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "gateway", Gateway: "vpn", LastOctet: 10, AsLink: true, Scheme: "http"}
		mutate(&p)
		return p
	}
	cases := []struct {
		name string
		ph   exerciseModel.Placeholder
		ok   bool
	}{
		{"http default port", link(func(*exerciseModel.Placeholder) {}), true},
		{"https port and path", link(func(p *exerciseModel.Placeholder) { p.Scheme, p.Port, p.Path = "https", 8443, "/a/b?x=1" }), true},
		{"port max", link(func(p *exerciseModel.Placeholder) { p.Port = 65535 }), true},
		{"missing scheme", link(func(p *exerciseModel.Placeholder) { p.Scheme = "" }), false},
		{"ftp scheme", link(func(p *exerciseModel.Placeholder) { p.Scheme = "ftp" }), false},
		{"port too big", link(func(p *exerciseModel.Placeholder) { p.Port = 65536 }), false},
		{"port negative", link(func(p *exerciseModel.Placeholder) { p.Port = -1 }), false},
		{"path without slash", link(func(p *exerciseModel.Placeholder) { p.Path = "admin" }), false},
		{"path with space", link(func(p *exerciseModel.Placeholder) { p.Path = "/a b" }), false},
		{"path with quote", link(func(p *exerciseModel.Placeholder) { p.Path = "/a\"b" }), false},
		{"path too long", link(func(p *exerciseModel.Placeholder) { p.Path = "/" + strings.Repeat("a", 200) }), false},
		{"mask on link", link(func(p *exerciseModel.Placeholder) { p.ShowMask = true }), false},
		{"link fields without AsLink", exerciseModel.Placeholder{Kind: exerciseModel.PlaceholderIP, IPReference: "vpn", Scheme: "http"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := validTask()
			task.Placeholders = []exerciseModel.Placeholder{tc.ph}
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{publishVariant(topo, task)}}
			err := version.ValidateForPublish()
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && !errors.Is(err, exerciseModel.ErrPlaceholderLinkInvalid.Err()) {
				t.Fatalf("want ErrPlaceholderLinkInvalid, got %v", err)
			}
		})
	}
}
