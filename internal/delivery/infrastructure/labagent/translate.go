package labagent

import (
	"encoding/json"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The laboratory Lab spec is delivered to the agent as an opaque spec_json blob
// (protoToLab unmarshals it into the operator's typed LabSpec). We mirror only
// the JSON shape here rather than importing the operator's k8s API types, so the
// daemon does not pull controller-runtime into its dependency graph. The field
// tags MUST match api/laboratory/v1alpha1 LabSpec exactly.
type (
	labInitialLifecycle struct {
		DesiredState string `json:"desiredState"`
		OperationID  string `json:"operationId"`
		Revision     int64  `json:"revision"`
	}
	labSpec struct {
		Lifecycle   *labInitialLifecycle `json:"lifecycle,omitempty"`
		VPN         labNetwork           `json:"vpn,omitempty"`
		Internet    labNetwork           `json:"internet,omitempty"`
		Devices     []labDevice          `json:"devices,omitempty"`
		Connections []labConnection      `json:"connections,omitempty"`
	}

	labNetwork struct {
		Enabled    bool     `json:"enabled,omitempty"`
		DHCPServer *labDHCP `json:"dhcpServer,omitempty"`
	}

	labDHCP struct {
		Enabled bool       `json:"enabled,omitempty"`
		Ranges  []labRange `json:"ranges,omitempty"`
		DNS     string     `json:"dns,omitempty"`
	}

	labRange struct {
		Start int32 `json:"start"`
		End   int32 `json:"end"`
	}

	labDevice struct {
		Name           string         `json:"name"`
		Type           string         `json:"type"`
		Image          string         `json:"image,omitempty"`
		SecurityPreset string         `json:"securityPreset,omitempty"`
		Resources      *labResources  `json:"resources,omitempty"`
		Interfaces     []labInterface `json:"interfaces,omitempty"`
		Exposure       *labExposure   `json:"exposure,omitempty"`
		// Persistence is the per-device state-persistence request; immutable once the lab exists.
		Persistence *labPersistence `json:"persistence,omitempty"`
	}

	labPersistence struct {
		Enabled  bool   `json:"enabled,omitempty"`
		Debounce string `json:"debounce,omitempty"`
	}

	labInterface struct {
		Name string `json:"name"`
		MAC  string `json:"mac,omitempty"`
		// Addr is a pointer so an IPConfigTypeNone interface serialises with no
		// "addr" key at all — the operator's AddrSpec.Type is required only when
		// addr is present, so an omitted addr is a legitimate L2-only interface.
		Addr *labAddr `json:"addr,omitempty"`
	}

	labAddr struct {
		Type       string           `json:"type"`
		IP         string           `json:"ip,omitempty"`
		AddressRef *labNetworkIPRef `json:"addressRef,omitempty"`
		Gateway    string           `json:"gateway,omitempty"`
		GatewayRef *labNetworkIPRef `json:"gatewayRef,omitempty"`
		Routes     []labRoute       `json:"routes,omitempty"`
	}

	labRoute struct {
		Dst    string               `json:"dst,omitempty"`
		DstRef *labNetworkSubnetRef `json:"dstRef,omitempty"`
		Via    string               `json:"via,omitempty"`
		ViaRef *labNetworkIPRef     `json:"viaRef,omitempty"`
	}

	labNetworkIPRef struct {
		Network string `json:"network"`
		Host    int32  `json:"host"`
	}

	labNetworkSubnetRef struct {
		Network string `json:"network"`
	}

	labResources struct {
		CPURequest    string `json:"cpuRequest,omitempty"`
		MemoryRequest string `json:"memoryRequest,omitempty"`
		CPULimit      string `json:"cpuLimit,omitempty"`
		MemoryLimit   string `json:"memoryLimit,omitempty"`
	}

	labExposure struct {
		Web *labWeb `json:"web,omitempty"`
	}

	labWeb struct {
		Port     int32  `json:"port"`
		Protocol string `json:"protocol,omitempty"`
	}

	labConnection struct {
		Endpoints []labEndpoint `json:"endpoints"`
	}

	labEndpoint struct {
		Device    string `json:"device"`
		Interface string `json:"interface,omitempty"`
	}
)

// Reserved endpoint device names the operator interprets as the lab's VPN and
// internet gateways (lab_controller/connection_reconciler match these literals).
const (
	reservedDeviceVPN      = "vpn"
	reservedDeviceInternet = "internet"
)

// BuildLabSpec translates a variant's domain Topology into the laboratory Lab
// spec_json plus the per-device environment. Env values are passed through
// verbatim: the caller is responsible for decrypting secret vars first, so this
// stays a pure, side-effect-free mapping. The agent takes the variables only as Secrets, so
// they never appear in the returned spec. Devices are keyed by their domain
// UUID for connection-endpoint resolution; the operator addresses them by name.
func BuildLabSpec(t exerciseModel.Topology) (specJSON []byte, env []*labpb.DeviceEnv, err error) {
	spec := labSpec{
		VPN:      buildNetwork(t.VPN),
		Internet: buildNetwork(t.Internet),
	}

	nameByID := make(map[string]string, len(t.Devices))
	labNames := exerciseModel.LabDeviceNames(t.Devices)
	for i, d := range t.Devices {
		labName := labNames[i]
		nameByID[d.ID.String()] = labName
		spec.Devices = append(spec.Devices, buildDevice(d, labName))
		if de := buildDeviceEnv(d); de != nil {
			env = append(env, de)
		}
	}

	for _, c := range t.Connections {
		conn := labConnection{}
		for _, ep := range c.Endpoints {
			conn.Endpoints = append(conn.Endpoints, buildEndpoint(ep, nameByID))
		}
		spec.Connections = append(spec.Connections, conn)
	}

	specJSON, err = json.Marshal(spec)
	if err != nil {
		return nil, nil, err
	}
	return specJSON, env, nil
}

func buildNetwork(n exerciseModel.NetworkSpec) labNetwork {
	out := labNetwork{Enabled: n.Enabled}
	if n.Enabled && n.DHCP {
		out.DHCPServer = &labDHCP{Enabled: true, DNS: n.DNS}
		for _, r := range n.DHCPRanges {
			out.DHCPServer.Ranges = append(out.DHCPServer.Ranges, labRange{Start: r.Start, End: r.End})
		}
	}
	return out
}

// buildDevice maps a domain device. Forwarding devices (switch/hub) carry no
// image/interfaces/exposure by construction, so only Name/Type are set.
func buildDevice(d exerciseModel.Device, labName string) labDevice {
	out := labDevice{
		Name:           labName,
		Type:           string(d.Type),
		Image:          d.Image,
		SecurityPreset: string(d.SecurityPreset),
	}
	if d.Resources != nil {
		out.Resources = &labResources{
			CPURequest: d.Resources.CPURequest, MemoryRequest: d.Resources.MemoryRequest,
			CPULimit: d.Resources.CPULimit, MemoryLimit: d.Resources.MemoryLimit,
		}
	}
	for _, iface := range d.Interfaces {
		out.Interfaces = append(out.Interfaces, buildInterface(iface))
	}
	if d.Persistence != nil {
		out.Persistence = &labPersistence{Enabled: d.Persistence.Enabled, Debounce: d.Persistence.Debounce}
	}
	if d.External != nil {
		out.Exposure = &labExposure{Web: &labWeb{Port: d.External.Port, Protocol: d.External.Protocol}}
	}
	return out
}

// buildInterface maps IP config. IPConfigType values line up 1:1 with the
// operator's AddrType (static/dhcp/dhcp-preset); "none" yields no addr. The
// operator's AddrSpec takes a single IP. Domain validation requires exactly
// one static address before this mapper is called.
func buildInterface(iface exerciseModel.Interface) labInterface {
	out := labInterface{Name: iface.Name, MAC: iface.MAC}
	switch iface.IP.Type {
	case exerciseModel.IPConfigTypeStatic:
		addr := &labAddr{
			Type:       string(exerciseModel.IPConfigTypeStatic),
			AddressRef: buildNetworkIPRef(iface.IP.AddressRef),
			Gateway:    iface.IP.Gateway,
			GatewayRef: buildNetworkIPRef(iface.IP.GatewayRef),
		}
		if len(iface.IP.Addresses) > 0 {
			addr.IP = iface.IP.Addresses[0]
		}
		for _, route := range iface.IP.Routes {
			addr.Routes = append(addr.Routes, labRoute{Dst: route.Dst, DstRef: buildNetworkSubnetRef(route.DstRef), Via: route.Via, ViaRef: buildNetworkIPRef(route.ViaRef)})
		}
		out.Addr = addr
	case exerciseModel.IPConfigTypeDHCP, exerciseModel.IPConfigTypeDHCPPreset:
		out.Addr = &labAddr{Type: string(iface.IP.Type)}
	case exerciseModel.IPConfigTypeNone:
		// no addr: L2-only interface.
	}
	return out
}

func buildNetworkIPRef(ref *exerciseModel.NetworkIPRef) *labNetworkIPRef {
	if ref == nil {
		return nil
	}
	return &labNetworkIPRef{Network: ref.Network, Host: ref.Host}
}

func buildNetworkSubnetRef(ref *exerciseModel.NetworkSubnetRef) *labNetworkSubnetRef {
	if ref == nil {
		return nil
	}
	return &labNetworkSubnetRef{Network: ref.Network}
}

// buildEndpoint resolves a domain endpoint to the operator's {device, interface}
// form. VPN/internet endpoints become the reserved gateway device names; a
// device endpoint resolves its UUID to the device's name.
func buildEndpoint(ep exerciseModel.Endpoint, nameByID map[string]string) labEndpoint {
	switch ep.Kind {
	case exerciseModel.EndpointVPN:
		return labEndpoint{Device: reservedDeviceVPN, Interface: ep.Interface}
	case exerciseModel.EndpointInternet:
		return labEndpoint{Device: reservedDeviceInternet, Interface: ep.Interface}
	default:
		return labEndpoint{Device: nameByID[ep.DeviceID.String()], Interface: ep.Interface}
	}
}

// buildDeviceEnv collects a device's environment into a proto DeviceEnv the agent
// writes to a write-only Secret. Returns nil when the device declares no env.
func buildDeviceEnv(d exerciseModel.Device) *labpb.DeviceEnv {
	if len(d.EnvVars) == 0 {
		return nil
	}
	vars := make(map[string]string, len(d.EnvVars))
	for _, ev := range d.EnvVars {
		vars[ev.Name] = ev.Value
	}
	return &labpb.DeviceEnv{Device: d.Name, Vars: vars}
}
