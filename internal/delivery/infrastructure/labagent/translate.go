package labagent

import (
	"encoding/json"
	"math"
	"strconv"
	"time"

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
		Gateways    []labGateway         `json:"gateways,omitempty"`
		Devices     []labDevice          `json:"devices,omitempty"`
		Connections []labConnection      `json:"connections,omitempty"`
	}

	// labGateway is one entry of spec.gateways: a VPN entry or an Internet exit, named like the gateway device of the task.
	labGateway struct {
		Name       string   `json:"name"`
		Kind       string   `json:"kind"` // "vpn" or "internet"
		DHCPServer *labDHCP `json:"dhcpServer,omitempty"`
		// ReverseConnections (a VPN gateway only; sent only to an agent that enforces the block) lets the lab start
		// connections to the VPN clients through this gateway. Absent = blocked.
		ReverseConnections bool `json:"reverseConnections,omitempty"`
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
		Name string `json:"name"`
		Type string `json:"type"`
		// Profile is how the device runs: absent is the container; microvm runs it in a microVM. Only for type
		// container (a node).
		Profile        string         `json:"profile,omitempty"`
		Image          string         `json:"image,omitempty"`
		SecurityPreset string         `json:"securityPreset,omitempty"`
		Resources      *labResources  `json:"resources,omitempty"`
		Interfaces     []labInterface `json:"interfaces,omitempty"`
		// Access is the ways to reach the device in the browser (http, terminal, vnc, rdp), one entry per kind.
		Access []labAccess `json:"access,omitempty"`
		// Persistence is the per-device state-persistence request; immutable once the lab exists.
		Persistence *labPersistence `json:"persistence,omitempty"`
		// WaitReadyFile: the device is Ready only once /run/ready exists inside the container.
		WaitReadyFile bool `json:"waitReadyFile,omitempty"`
		// Service is what the launcher runs after the bootstrap (contract 1.5); only for a device that has a bootstrap
		// list or a command of its own.
		Service *labService `json:"service,omitempty"`
	}

	labService struct {
		Command    []string `json:"command,omitempty"`
		Args       []string `json:"args,omitempty"`
		User       string   `json:"user,omitempty"`
		WorkingDir string   `json:"workingDir,omitempty"`
	}

	// labAccess is one entry of the device's access list: kind http | terminal | vnc | rdp. http, vnc and rdp take a port
	// (http also a protocol); terminal takes a command (empty = /bin/sh) and a user (empty = the image's user).
	labAccess struct {
		Kind     string   `json:"kind"`
		Port     int32    `json:"port,omitempty"`
		Protocol string   `json:"protocol,omitempty"`
		Command  []string `json:"command,omitempty"`
		User     string   `json:"user,omitempty"`
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
		Gateway string `json:"gateway"`
		Host    int32  `json:"host"`
	}

	labNetworkSubnetRef struct {
		Gateway string `json:"gateway"`
	}

	labResources struct {
		CPURequest    string `json:"cpuRequest,omitempty"`
		MemoryRequest string `json:"memoryRequest,omitempty"`
		CPULimit      string `json:"cpuLimit,omitempty"`
		MemoryLimit   string `json:"memoryLimit,omitempty"`
	}

	labConnection struct {
		Endpoints []labEndpoint `json:"endpoints"`
	}

	labEndpoint struct {
		Device    string `json:"device"`
		Interface string `json:"interface,omitempty"`
	}
)

// LabBuild is everything one lab is created with: the spec and the secret material that travels beside it. The agent
// takes the environment, the bootstrap lists and the checks only as Secrets, so none of them appears in the spec.
type LabBuild struct {
	SpecJSON  []byte
	Env       []*labpb.DeviceEnv
	Bootstrap []*labpb.DeviceBootstrap
	// Checks is nil when the lab has none.
	Checks *labpb.LabChecks
	// ReverseSent names the VPN gateways whose spec carries the key that allows connections from the lab to the VPN clients.
	ReverseSent map[string]bool
}

// BuildLabSpec translates a variant's domain Topology into the laboratory Lab
// spec_json plus the per-device environment (the part of BuildLab the lab always had).
func BuildLabSpec(t exerciseModel.Topology) (specJSON []byte, env []*labpb.DeviceEnv, err error) {
	build, err := BuildLab(t, nil)
	if err != nil {
		return nil, nil, err
	}
	return build.SpecJSON, build.Env, nil
}

// BuildLab translates a variant's domain Topology (its bootstraps resolved, secret values opened) and the checks of its
// state tasks into the laboratory Lab spec_json and the secret material of contract 1.5. Env values are passed through
// verbatim: the caller is responsible for decrypting secret vars first, so this stays a pure, side-effect-free mapping.
// Devices are keyed by their domain UUID for connection-endpoint resolution; the operator addresses them by name. An
// error names the device whose service command cannot be told to the laboratory.
func BuildLab(t exerciseModel.Topology, checks []exerciseModel.LabCheck) (LabBuild, error) {
	return BuildLabFor(t, checks, false)
}

// BuildLabFor is BuildLab for an agent that does (enforcesVPNReverse) or does not report that it enforces the block of
// connections the lab starts to VPN clients. The key that allows them is sent only to an agent that does, and only when the
// exercise asks for it and the VPN is on: an older agent rejects the key and allows everything anyway, and a capable agent
// blocks by default.
func BuildLabFor(t exerciseModel.Topology, checks []exerciseModel.LabCheck, enforcesVPNReverse bool) (LabBuild, error) {
	var spec labSpec
	var build LabBuild

	nameByID := make(map[string]string, len(t.Devices))
	labNames := exerciseModel.LabDeviceNames(t.Devices)
	for i, d := range t.Devices {
		labName := labNames[i]
		nameByID[d.ID.String()] = labName
		if d.Type.IsGateway() {
			gateway := buildGateway(d, enforcesVPNReverse)
			spec.Gateways = append(spec.Gateways, gateway)
			if gateway.ReverseConnections {
				if build.ReverseSent == nil {
					build.ReverseSent = map[string]bool{}
				}
				build.ReverseSent[gateway.Name] = true
			}
			continue
		}
		device, err := withRuntime(buildDevice(d, labName), d)
		if err != nil {
			return LabBuild{}, err
		}
		spec.Devices = append(spec.Devices, device)
		if de := buildDeviceEnv(d); de != nil {
			build.Env = append(build.Env, de)
		}
		if bootstrap := buildDeviceBootstrap(d); bootstrap != nil {
			build.Bootstrap = append(build.Bootstrap, bootstrap)
		}
	}

	for _, c := range t.Connections {
		conn := labConnection{}
		for _, ep := range c.Endpoints {
			conn.Endpoints = append(conn.Endpoints, buildEndpoint(ep, nameByID))
		}
		spec.Connections = append(spec.Connections, conn)
	}
	if len(checks) > 0 {
		var checksErr error
		if build.Checks, checksErr = buildLabChecks(checks); checksErr != nil {
			return LabBuild{}, checksErr
		}
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return LabBuild{}, err
	}
	build.SpecJSON = specJSON
	return build, nil
}

// buildGateway maps a gateway device to its entry of spec.gateways. The key that allows connections from the lab to the VPN
// clients is sent only when the exercise asks for it on this VPN gateway and the agent is known to enforce the block (an older
// agent rejects the key and allows everything anyway, and a capable agent blocks by default).
func buildGateway(d exerciseModel.Device, enforcesVPNReverse bool) labGateway {
	out := labGateway{Name: d.Name, Kind: "internet"}
	if d.Type == exerciseModel.DeviceTypeVPNGateway {
		out.Kind = "vpn"
		out.ReverseConnections = enforcesVPNReverse && d.ReverseConnections
	}
	if d.DHCP {
		out.DHCPServer = &labDHCP{Enabled: true, DNS: d.DNS}
		for _, r := range d.DHCPRanges {
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
		Type:           wireDeviceType(d),
		Profile:        wireProfile(d),
		Image:          d.Image,
		SecurityPreset: string(d.SecurityPreset),
		WaitReadyFile:  d.WaitReadyFile,
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
	out.Access = buildAccess(d)
	return out
}

// withRuntime adds what the document says about running a node (contract 1.5): the service command, from the author or the
// image, and the terminal. An error names the device whose service command is unknown.
func withRuntime(out labDevice, d exerciseModel.Device) (labDevice, error) {
	if !d.IsNode() {
		return out, nil
	}
	service, err := d.LabService()
	if err != nil {
		return labDevice{}, err
	}
	if service != nil {
		out.Service = &labService{Command: service.Command, Args: service.Args, User: service.User, WorkingDir: service.WorkingDir}
	}
	return out, nil
}

// buildAccess maps the access list of a device, one entry per kind as the document has it. The http protocol is written as the
// document has it (empty = http, the default of the laboratory); a terminal command is one shell, so a one-item list.
func buildAccess(d exerciseModel.Device) []labAccess {
	var out []labAccess
	for _, a := range d.Access {
		entry := labAccess{Kind: string(a.Kind), Port: a.Port, Protocol: a.Protocol, User: a.User}
		if a.Command != "" {
			entry.Command = []string{a.Command}
		}
		out = append(out, entry)
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
	return &labNetworkIPRef{Gateway: ref.Gateway, Host: ref.Host}
}

func buildNetworkSubnetRef(ref *exerciseModel.NetworkSubnetRef) *labNetworkSubnetRef {
	if ref == nil {
		return nil
	}
	return &labNetworkSubnetRef{Gateway: ref.Gateway}
}

// buildEndpoint resolves a domain endpoint to the operator's {device, interface} form: the UUID of the device (a gateway
// included, which the lab addresses by its name and its one port eth0) becomes the device's name in the lab.
func buildEndpoint(ep exerciseModel.Endpoint, nameByID map[string]string) labEndpoint {
	return labEndpoint{Device: nameByID[ep.DeviceID.String()], Interface: ep.Interface}
}

// buildDeviceEnv collects a device's environment (the bootstrap.env of the resolved topology, substitutions made)
// into a proto DeviceEnv the agent writes to a write-only Secret. Returns nil when the device declares no env. The
// rest of the bootstrap (files, script) travels in buildDeviceBootstrap; env stays on this path.
func buildDeviceEnv(d exerciseModel.Device) *labpb.DeviceEnv {
	if len(d.Bootstrap.Env) == 0 {
		return nil
	}
	vars := make(map[string]string, len(d.Bootstrap.Env))
	for _, ev := range d.Bootstrap.Env {
		vars[ev.Name] = ev.Value
	}
	return &labpb.DeviceEnv{Device: d.Name, Vars: vars}
}

// wireDeviceType is the device type the agent knows. A node with the container profile is the agent's "container";
// the other profiles reach the agent through its microVM contract.
func wireDeviceType(d exerciseModel.Device) string {
	if d.IsNode() {
		return string(exerciseModel.DeviceTypeContainer)
	}
	return string(d.Type)
}

// wireProfile is the profile the agent is sent: none for the container (the agent's default, so an old agent and
// every stored lab spec stay as they are).
func wireProfile(d exerciseModel.Device) string {
	if profile := d.EffectiveProfile(); profile != "" &&
		profile != exerciseModel.NodeProfileContainer {
		return string(profile)
	}
	return ""
}

// buildDeviceBootstrap is the bootstrap list of a device whose resolved bootstrap has files or a script, with every
// substitution made (Source files already hold their bytes as Content); nil for a device without one. A bootstrap with env
// only needs none.
func buildDeviceBootstrap(d exerciseModel.Device) *labpb.DeviceBootstrap {
	if !d.IsNode() || !d.HasBootstrapList() {
		return nil
	}
	out := &labpb.DeviceBootstrap{
		Device:      d.Name,
		Script:      d.Bootstrap.Script,
		Interpreter: d.Bootstrap.Interpreter,
	}
	if d.Bootstrap.Timeout != "" {
		if timeout, err := time.ParseDuration(d.Bootstrap.Timeout); err == nil && timeout > 0 {
			out.TimeLimitSeconds = int32(min((timeout+time.Second-1)/time.Second, math.MaxInt32))
		}
	}
	for _, f := range d.Bootstrap.Files {
		file := &labpb.BootstrapFile{Path: f.Path, Content: []byte(f.Content), Owner: f.Owner, Group: f.Group}
		if mode, err := strconv.ParseUint(f.Mode, 8, 32); err == nil {
			file.Mode = uint32(mode)
		}
		out.Files = append(out.Files, file)
	}
	return out
}

// buildLabChecks maps the checks of a lab (at least one). The observation of each part is mapped in
// checks_parts.go; a kind the laboratory contract has no message for is an error, never a part left out.
func buildLabChecks(checks []exerciseModel.LabCheck) (*labpb.LabChecks, error) {
	out := &labpb.LabChecks{}
	for _, c := range checks {
		check := &labpb.Check{
			Id:               c.ID,
			Device:           c.Device,
			Mode:             wireCheckMode(c.Mode),
			ScheduleSeconds:  c.ScheduleSeconds,
			ManualRun:        c.Manual,
			TimeLimitSeconds: c.TimeLimitSeconds,
		}
		for _, part := range c.Parts {
			wire, err := buildCheckPart(c.Device, part.Condition)
			if err != nil {
				return nil, err
			}
			check.Parts = append(check.Parts, wire)
		}
		out.Items = append(out.Items, check)
	}
	return out, nil
}

func wireCheckMode(mode exerciseModel.StateMode) labpb.CheckMode {
	if mode == exerciseModel.StateModeContinuous {
		return labpb.CheckMode_CHECK_MODE_CONTINUOUS
	}
	return labpb.CheckMode_CHECK_MODE_ONCE
}

// optionalText is a pointer to s, nil for an expectation that is not set.
func optionalText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
