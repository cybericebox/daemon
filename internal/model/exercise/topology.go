package exerciseModel

import (
	"encoding/json"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

type DeviceType string

const (
	// DeviceTypeNode is the «Вузол»: a machine of the lab that runs as the container, microVM or VM of its Profile.
	DeviceTypeNode DeviceType = "node"
	// DeviceTypeContainer is the former type of a container device. It is only read: Device.Normalize (run by the
	// JSON decoding and by every import) turns it into a node with the container profile, and nothing writes it.
	DeviceTypeContainer       DeviceType = "container"
	DeviceTypeUnmanagedSwitch DeviceType = "unmanaged-switch"
	DeviceTypeHub             DeviceType = "hub"
)

func (t DeviceType) Valid() bool {
	switch t {
	case DeviceTypeNode, DeviceTypeContainer, DeviceTypeUnmanagedSwitch, DeviceTypeHub:
		return true
	}
	return false
}

// IsNode reports whether the device runs a workload (a node, or a not yet normalized former container).
func (t DeviceType) IsNode() bool { return t == DeviceTypeNode || t == DeviceTypeContainer }

// NodeProfile is how a node runs: «Контейнер», «Мікро-VM» or «VM».
type NodeProfile string

const (
	NodeProfileContainer NodeProfile = "container"
	NodeProfileMicroVM   NodeProfile = "microvm"
	// NodeProfileVM is reserved in the model: it is refused where a lab would be deployed.
	NodeProfileVM NodeProfile = "vm"
)

// NodeProfiles lists every profile of the model in display order.
var NodeProfiles = []NodeProfile{NodeProfileContainer, NodeProfileMicroVM, NodeProfileVM}

func (p NodeProfile) Valid() bool {
	switch p {
	case NodeProfileContainer, NodeProfileMicroVM, NodeProfileVM:
		return true
	}
	return false
}

// IsForwarding reports whether the device forwards L2 frames (switch/hub).
// Such devices are unmanaged and sit outside the declared topology's control
// (real switches can be cabled arbitrarily), so loop/broadcast-domain shape is
// not validated here — the editor UI warns the user instead.
func (t DeviceType) IsForwarding() bool {
	return t == DeviceTypeUnmanagedSwitch || t == DeviceTypeHub
}

// IPConfigType types an interface's IP CONFIG (how it gets an address), not
// the address itself — hence the name, distinct from the interface's MAC.
type IPConfigType string

const (
	IPConfigTypeStatic IPConfigType = "static"
	// IPConfigTypeDHCP: the device image runs its OWN DHCP client (leases and
	// configures the interface). Grants the device the caps that needs.
	IPConfigTypeDHCP IPConfigType = "dhcp"
	// IPConfigTypeDHCPPreset: the image has no DHCP client, so the platform
	// leases an address and applies it for the device (no caps needed).
	IPConfigTypeDHCPPreset IPConfigType = "dhcp-preset"
	IPConfigTypeNone       IPConfigType = "none"
)

func (t IPConfigType) Valid() bool {
	switch t {
	case IPConfigTypeStatic, IPConfigTypeDHCP, IPConfigTypeDHCPPreset, IPConfigTypeNone:
		return true
	}
	return false
}

// SecurityPreset selects a named capability profile for a device container. The
// concrete Linux capabilities behind each preset are resolved by the
// infrastructure, not here — this is only the vocabulary the editor exposes.
type SecurityPreset string

const (
	SecurityPresetStandard SecurityPreset = "standard" // web, API, databases, SSH, cracking, forensics, gdb and strace
	SecurityPresetExtended SecurityPreset = "extended" // standard plus raw sockets, NET_ADMIN and /dev/net/tun
)

func (p SecurityPreset) Valid() bool {
	switch p {
	case SecurityPresetStandard, SecurityPresetExtended:
		return true
	}
	return false
}

type EndpointKind string

const (
	EndpointDevice   EndpointKind = "device"
	EndpointVPN      EndpointKind = "vpn"
	EndpointInternet EndpointKind = "internet"
)

func (k EndpointKind) Valid() bool {
	return k == EndpointDevice || k == EndpointVPN || k == EndpointInternet
}

type (
	// Topology is one variant's lab layout. Spec §8/§9: devices + direct
	// connections + the vpn/internet singletons as NetworkSpec fields.
	Topology struct {
		// Variables are set once per instance and used as {{var.<name>}} in any device's bootstrap and in the
		// expectations of the conditions.
		Variables    []Variable      `json:"variables,omitempty"`
		VPN          NetworkSpec     `json:"vpn"`
		Internet     NetworkSpec     `json:"internet"`
		Devices      []Device        `json:"devices"`
		Connections  []Connection    `json:"connections"`
		VisualRender json.RawMessage `json:"visual_render,omitempty"` // frontend layout, opaque
	}

	NetworkSpec struct {
		Enabled    bool        `json:"enabled"`
		DHCP       bool        `json:"dhcp"`
		DHCPRanges []DHCPRange `json:"dhcp_ranges,omitempty"`
		DNS        string      `json:"dns,omitempty"`
	}

	DHCPRange struct {
		Start int32 `json:"start"`
		End   int32 `json:"end"`
	}

	Device struct {
		ID   uuid.UUID  `json:"id"`
		Name string     `json:"name"` // logical lab name; DNS label
		Type DeviceType `json:"type"`
		// Profile (a node only) is how the node runs; empty counts as the container.
		Profile        NodeProfile    `json:"profile,omitempty"`
		Image          string         `json:"image,omitempty"`
		SecurityPreset SecurityPreset `json:"security_preset,omitempty"` // empty = standard
		// ResourcePreset is a platform preset id (micro, small, medium, large); when empty the device
		// carries its own custom Resources, and with neither it gets the default preset. Requests always
		// equal limits at the agent.
		ResourcePreset string           `json:"resource_preset,omitempty"`
		Resources      *DeviceResources `json:"resources,omitempty"`
		Interfaces     []Interface      `json:"interfaces,omitempty"`
		External       *ExternalAccess  `json:"external,omitempty"` // exposure: web
		// Command and Args replace the entrypoint and arguments of the image (empty = the image's own).
		Command []string `json:"command,omitempty"`
		Args    []string `json:"args,omitempty"`
		// System and Hardware are reserved for the vm profile (default linux / fast).
		System   SystemKind   `json:"system,omitempty"`
		Hardware HardwareKind `json:"hardware,omitempty"`
		// Bootstrap is what happens to a clean device before its service starts; its Env are the variables of the
		// service process.
		Bootstrap Bootstrap `json:"bootstrap,omitzero"`
		// Terminal opens a terminal of the device in the participant's browser; off unless enabled.
		Terminal Terminal `json:"terminal,omitzero"`
		// Persistence keeps the device's writable layer across an unplanned restart; nil = off.
		Persistence *DevicePersistence `json:"persistence,omitempty"`
		// WaitReadyFile makes the device Ready only once the application has created /run/ready inside the
		// container. A port published through the web proxy is checked automatically either way. Off by
		// default, so exercises saved before it keep their behaviour.
		WaitReadyFile bool `json:"wait_ready_file,omitempty"`
	}

	// DevicePersistence is the state-persistence policy of a device, set at creation and
	// immutable. Unset fields take the platform defaults.
	DevicePersistence struct {
		Enabled bool `json:"enabled,omitempty"`
		// Debounce is a Go duration ("5s"): how long the writable layer stays quiet before a snapshot.
		Debounce string `json:"debounce,omitempty"`
	}

	Interface struct {
		Name string   `json:"name"`
		MAC  string   `json:"mac,omitempty"`
		IP   IPConfig `json:"ip"`
	}

	IPConfig struct {
		Type       IPConfigType  `json:"type"`
		Addresses  []string      `json:"addresses,omitempty"` // CIDR list, static only
		AddressRef *NetworkIPRef `json:"address_ref,omitempty"`
		Gateway    string        `json:"gateway,omitempty"`
		GatewayRef *NetworkIPRef `json:"gateway_ref,omitempty"`
		Routes     []Route       `json:"routes,omitempty"`
	}

	NetworkIPRef struct {
		Network string `json:"network"`
		Host    int32  `json:"host"`
	}

	NetworkSubnetRef struct {
		Network string `json:"network"`
	}

	DeviceResources struct {
		CPURequest    string `json:"cpu_request,omitempty"`
		MemoryRequest string `json:"memory_request,omitempty"`
		CPULimit      string `json:"cpu_limit,omitempty"`
		MemoryLimit   string `json:"memory_limit,omitempty"`
	}

	Route struct {
		Dst    string            `json:"dst,omitempty"`
		DstRef *NetworkSubnetRef `json:"dst_ref,omitempty"`
		Via    string            `json:"via,omitempty"`
		ViaRef *NetworkIPRef     `json:"via_ref,omitempty"`
	}

	EnvVar struct {
		Name   string `json:"name"`
		Value  string `json:"value"` // ciphertext at rest when Secret
		Secret bool   `json:"secret"`
		// Origin (a secret only; server-side, never from or to a client or an archive) is an opaque id of where the
		// stored value came from: a fresh one for every value an author types, carried along when the value is kept,
		// inherited by a linked variant or copied. Two secrets with one origin hold the same value, so the editor can
		// group them without any value-derived hash an author could test guesses against. Empty for values stored
		// before it existed.
		Origin string `json:"origin,omitempty"`
	}

	ExternalAccess struct {
		Port     int32  `json:"port"`
		Protocol string `json:"protocol"` // http | https (re-encrypt), spec §5
	}

	Connection struct {
		Endpoints []Endpoint `json:"endpoints"`
	}

	Endpoint struct {
		Kind      EndpointKind `json:"kind"`
		DeviceID  uuid.UUID    `json:"device_id,omitempty"`
		Interface string       `json:"interface,omitempty"`
	}
)

// MaxDeviceNameLen: a web-exposed device is served at <device>-<code>.<domain>,
// and <device>-<code> must be one DNS label (<=63 chars): device + "-" + the
// random base36 code of at most 4 chars, so at most 63-1-4 = 58. We keep 35 for
// a round, predictable limit.
// Keep in sync with laboratory internal/names.MaxDeviceNameLen.
const MaxDeviceNameLen = 35

var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// validateStructure checks per-item shape only (types, names, endpoint arity).
// Cross-item graph rules (uniqueness, resolution, port occupancy) run at
// publish time — validateGraph, Task 4.
func (t Topology) validateStructure() error {
	if t.VPN.Enabled && t.VPN.DHCP && len(t.VPN.DHCPRanges) == 0 ||
		t.Internet.Enabled && t.Internet.DHCP && len(t.Internet.DHCPRanges) == 0 ||
		!validDHCPRanges(t.VPN.DHCPRanges) || !validDHCPRanges(t.Internet.DHCPRanges) {
		return ErrNetworkDHCPInvalid.Err()
	}
	if t.VPN.DNS != "" || t.Internet.DNS != "" && !validIPv4(t.Internet.DNS) {
		return ErrNetworkDHCPInvalid.Err()
	}
	usedAddressRefs := make(map[NetworkIPRef]bool)
	for _, d := range t.Devices {
		if d.Type.IsNode() {
			if !dnsLabelRe.MatchString(d.Name) {
				return ErrDeviceNameInvalid.WithContext("device", d.Name).Err()
			}
			if len(d.Name) > MaxDeviceNameLen {
				return ErrDeviceNameTooLong.WithContext("device", d.Name).Err()
			}
		}
		if d.Type.IsForwarding() && strings.TrimSpace(d.Name) == "" {
			return ErrDeviceDisplayNameInvalid.WithContext("device", d.Name).Err()
		}
		if !d.Type.Valid() ||
			(d.Type.IsForwarding() && (len(d.Interfaces) > 0 || d.Image != "" || d.External != nil || d.WaitReadyFile || d.SecurityPreset != "" || d.Resources != nil || d.ResourcePreset != "")) {
			return ErrDeviceTypeInvalid.WithContext("device", d.Name).Err()
		}
		if d.Profile != "" && (!d.Type.IsNode() || !d.Profile.Valid()) {
			return ErrNodeProfileInvalid.WithPublicContext("device", d.Name).
				WithPublicContext("profile", string(d.Profile)).
				Err()
		}
		if reason := invalidPersistence(d); reason != "" {
			return ErrDevicePersistenceInvalid.WithContext("device", d.Name).WithContext("reason", reason).Err()
		}
		if d.Resources != nil {
			return ErrDeviceCustomResources.WithPublicContext("device", d.Name).Err()
		}
		if d.SecurityPreset != "" && !d.SecurityPreset.Valid() {
			return ErrDeviceSecurityPresetInvalid.WithContext("device", d.Name).Err()
		}
		for _, iface := range d.Interfaces {
			if reason := t.addressReferencesInvalid(iface.IP, usedAddressRefs); reason != "" {
				return ErrDeviceAddressRefInvalid.WithContext("device", d.Name).WithContext("interface", iface.Name).WithContext("reason", reason).Err()
			}
			if iface.IP.Type == IPConfigTypeStatic && iface.IP.AddressRef == nil && len(iface.IP.Addresses) != 1 {
				return ErrDeviceStaticAddressCountInvalid.WithContext("device", d.Name).WithContext("interface", iface.Name).Err()
			}
			if routesInvalid(iface.IP) {
				return ErrDeviceRouteInvalid.WithContext("device", d.Name).WithContext("interface", iface.Name).Err()
			}
			if interfaceInvalid(iface) {
				return ErrDeviceInterfaceInvalid.WithContext("device", d.Name).Err()
			}
		}
		if err := validateDeviceRuntime(d); err != nil {
			return err
		}
		if d.External != nil && (d.External.Port < 1 || d.External.Port > 65535 ||
			(d.External.Protocol != "http" && d.External.Protocol != "https")) {
			return ErrDeviceExternalInvalid.WithContext("device", d.Name).Err()
		}
	}
	for _, c := range t.Connections {
		if len(c.Endpoints) != 2 {
			return ErrConnectionArityInvalid.Err()
		}
		for _, ep := range c.Endpoints {
			if !ep.Kind.Valid() ||
				(ep.Kind == EndpointDevice && ep.DeviceID == uuid.Nil) ||
				(ep.Kind != EndpointDevice && (ep.DeviceID != uuid.Nil || ep.Interface != "eth0")) {
				return ErrConnectionEndpointsInvalid.Err()
			}
		}
	}
	return nil
}

// validateGraph enforces the cross-item publish rules (spec §8/§9): unique
// names, endpoint resolution and port occupancy. L2 shape
// (loops, broadcast-domain separation) is not validated here: unmanaged
// switches/hubs can be cabled to real hardware outside the declared topology,
// so a static check would be meaningless — the editor UI warns instead.
func (t Topology) validateGraph() error {
	// 1. Unique device names + index by id.
	byID := make(map[uuid.UUID]Device, len(t.Devices))
	names := make(map[string]bool, len(t.Devices))
	for _, d := range t.Devices {
		if names[d.Name] {
			return ErrDeviceNameDuplicate.WithContext("device", d.Name).Err()
		}
		names[d.Name] = true
		byID[d.ID] = d
	}

	type portKey struct {
		dev   uuid.UUID
		iface string
	}
	usedPorts := make(map[portKey]bool)
	vpnConn, inetConn := -1, -1

	for ci, c := range t.Connections {
		for _, ep := range c.Endpoints {
			switch ep.Kind {
			case EndpointDevice:
				d, ok := byID[ep.DeviceID]
				if !ok {
					return ErrEndpointUnresolved.WithContext("connection", ci).Err()
				}
				if d.Type.IsForwarding() {
					if !validForwardingPort(ep.Interface) {
						return ErrForwardingPortInvalid.WithContext("device", d.Name).WithContext("interface", ep.Interface).Err()
					}
				} else if !hasInterface(d, ep.Interface) {
					return ErrEndpointUnresolved.WithContext("connection", ci).Err()
				}
				key := portKey{dev: d.ID, iface: ep.Interface}
				if usedPorts[key] {
					return ErrPortInUse.WithContext("device", d.Name).WithContext("interface", ep.Interface).Err()
				}
				usedPorts[key] = true
			case EndpointVPN:
				if !t.VPN.Enabled {
					return ErrVPNDisabled.WithContext("connection", ci).Err()
				}
				if vpnConn >= 0 {
					return ErrVPNGatewayInUse.Err()
				}
				vpnConn = ci
			case EndpointInternet:
				if !t.Internet.Enabled {
					return ErrInternetDisabled.WithContext("connection", ci).Err()
				}
				if inetConn >= 0 {
					return ErrInternetGatewayInUse.Err()
				}
				inetConn = ci
			}
		}
	}
	if err := t.validateAddressRefReachability(byID); err != nil {
		return err
	}

	return nil
}

// Each container interface is its own L2 vertex. A switch or hub has one
// shared vertex for all ports, since either forwards between them.
func (t Topology) validateAddressRefReachability(byID map[uuid.UUID]Device) error {
	endpointKey := func(ep Endpoint) string {
		switch ep.Kind {
		case EndpointVPN:
			return "gateway:vpn"
		case EndpointInternet:
			return "gateway:internet"
		default:
			key := "device:" + ep.DeviceID.String()
			if !byID[ep.DeviceID].Type.IsForwarding() {
				key += ":" + ep.Interface
			}
			return key
		}
	}
	links := make(map[string][]string)
	for _, connection := range t.Connections {
		left := endpointKey(connection.Endpoints[0])
		right := endpointKey(connection.Endpoints[1])
		links[left] = append(links[left], right)
		links[right] = append(links[right], left)
	}
	for _, device := range t.Devices {
		for _, iface := range device.Interfaces {
			if iface.IP.AddressRef == nil {
				continue
			}
			start := endpointKey(Endpoint{Kind: EndpointDevice, DeviceID: device.ID, Interface: iface.Name})
			goal := "gateway:" + iface.IP.AddressRef.Network
			seen := map[string]bool{start: true}
			queue := []string{start}
			for len(queue) > 0 && !seen[goal] {
				current := queue[0]
				queue = queue[1:]
				for _, adjacent := range links[current] {
					if !seen[adjacent] {
						seen[adjacent] = true
						queue = append(queue, adjacent)
					}
				}
			}
			if !seen[goal] {
				return ErrDeviceAddressRefUnreachable.WithContext("device", device.Name).WithContext("interface", iface.Name).WithContext("network", iface.IP.AddressRef.Network).Err()
			}
		}
	}
	return nil
}

func validForwardingPort(name string) bool {
	const prefix = "GigabitEthernet0/"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	number, parseErr := strconv.Atoi(strings.TrimPrefix(name, prefix))
	return parseErr == nil && number >= 1 && number <= 48 && name == prefix+strconv.Itoa(number)
}

func hasInterface(d Device, name string) bool {
	for _, iface := range d.Interfaces {
		if iface.Name == name {
			return true
		}
	}
	return false
}

// interfaceInvalid reports whether an interface's shape violates any of its
// rules, classify-style (mirrors classifyPlaceholder in placeholder.go) so
// every rule shares the ONE ErrDeviceInterfaceInvalid call site in
// validateStructure:
//   - Name non-empty.
//   - MAC optional; when set, must parse as a 48-bit (6-octet) MAC address.
//   - IP.Type must be one of static/dhcp/dhcp-preset/none.
//   - static requires exactly one Addresses entry, a valid CIDR;
//     non-static must have no Addresses.
//   - Gateway optional; when set, must parse as an IP and is only allowed
//     when IP.Type is static.
func interfaceInvalid(iface Interface) bool {
	if iface.Name == "" {
		return true
	}
	if iface.MAC != "" {
		mac, err := net.ParseMAC(iface.MAC)
		if err != nil || len(mac) != 6 {
			return true
		}
	}
	if !iface.IP.Type.Valid() {
		return true
	}
	isStatic := iface.IP.Type == IPConfigTypeStatic
	if isStatic && iface.IP.AddressRef == nil && len(iface.IP.Addresses) != 1 {
		return true
	}
	if !isStatic && len(iface.IP.Addresses) > 0 {
		return true
	}
	for _, addr := range iface.IP.Addresses {
		if _, err := netip.ParsePrefix(addr); err != nil {
			return true
		}
	}
	if iface.IP.Gateway != "" {
		if !isStatic {
			return true
		}
		if _, err := netip.ParseAddr(iface.IP.Gateway); err != nil {
			return true
		}
	}
	return false
}

// addressReferencesInvalid validates the typed address choices. Literal-only
// configurations keep their existing error classification and behavior.
func (t Topology) addressReferencesInvalid(ip IPConfig, used map[NetworkIPRef]bool) string {
	hasRef := ip.AddressRef != nil || ip.GatewayRef != nil
	for _, route := range ip.Routes {
		hasRef = hasRef || route.DstRef != nil || route.ViaRef != nil
	}
	if !hasRef {
		return ""
	}
	if ip.Type != IPConfigTypeStatic {
		return "references require static mode"
	}
	if ip.AddressRef != nil {
		if len(ip.Addresses) != 0 || !t.networkIPRefValid(*ip.AddressRef, 2) {
			return "invalid interface address reference"
		}
		if used[*ip.AddressRef] {
			return "duplicate interface address reference"
		}
		used[*ip.AddressRef] = true
	}
	if ip.GatewayRef != nil {
		if ip.Gateway != "" || !t.networkIPRefValid(*ip.GatewayRef, 1) {
			return "invalid gateway reference"
		}
		if len(ip.Addresses) == 1 {
			address, err := netip.ParsePrefix(ip.Addresses[0])
			if err == nil && !address.Addr().Is4() {
				return "gateway reference requires an IPv4 interface"
			}
		}
	}
	if ip.AddressRef != nil && ip.Gateway != "" {
		gateway, err := netip.ParseAddr(ip.Gateway)
		if err == nil && !gateway.Is4() {
			return "IPv4 address reference cannot use an IPv6 gateway"
		}
	}
	for _, route := range ip.Routes {
		if route.DstRef != nil {
			if route.Dst != "" || !t.networkRefEnabled(route.DstRef.Network) {
				return "invalid route destination reference"
			}
		}
		if route.ViaRef != nil {
			if route.Via != "" || !t.networkIPRefValid(*route.ViaRef, 1) {
				return "invalid route next-hop reference"
			}
		}
		if route.DstRef != nil && route.Via == "" && route.ViaRef == nil ||
			route.ViaRef != nil && route.Dst == "" && route.DstRef == nil {
			return "route requires a destination and next hop"
		}
		if route.DstRef != nil && route.Via != "" {
			via, err := netip.ParseAddr(route.Via)
			if err != nil || !via.Is4() {
				return "route destination and next hop must use IPv4"
			}
		}
		if route.ViaRef != nil && route.Dst != "" {
			dst, err := netip.ParsePrefix(route.Dst)
			if err != nil || !dst.Addr().Is4() {
				return "route destination and next hop must use IPv4"
			}
		}
	}
	return ""
}

func (t Topology) networkRefEnabled(network string) bool {
	switch network {
	case "vpn":
		return t.VPN.Enabled
	case "internet":
		return t.Internet.Enabled
	default:
		return false
	}
}

func (t Topology) networkIPRefValid(ref NetworkIPRef, minimum int32) bool {
	return ref.Host >= minimum && ref.Host <= 254 && t.networkRefEnabled(ref.Network)
}

func validDHCPRanges(ranges []DHCPRange) bool {
	used := make(map[int32]bool)
	for _, r := range ranges {
		if r.Start < 2 || r.End > 254 || r.Start > r.End {
			return false
		}
		for host := r.Start; host <= r.End; host++ {
			if used[host] {
				return false
			}
			used[host] = true
		}
	}
	return true
}

func validIPv4(value string) bool {
	parsed, err := netip.ParseAddr(value)
	return err == nil && parsed.Is4()
}

func routesInvalid(ip IPConfig) bool {
	if ip.Type != IPConfigTypeStatic {
		return len(ip.Routes) > 0
	}
	for _, route := range ip.Routes {
		if route.DstRef != nil || route.ViaRef != nil {
			continue // typed references are validated by addressReferencesInvalid
		}
		dst, dstErr := netip.ParsePrefix(route.Dst)
		via, viaErr := netip.ParseAddr(route.Via)
		if dstErr != nil || viaErr != nil || dst.Addr().Is4() != via.Is4() {
			return true
		}
	}
	return false
}

// maxPersistenceDebounce bounds how long a device's writable layer may stay quiet before a snapshot.
const maxPersistenceDebounce = 24 * time.Hour

// invalidPersistence names what is wrong with a device's state persistence, or "" when it is fine.
// Persistence needs a container (a switch or hub has no state); the debounce is a Go duration.
func invalidPersistence(d Device) string {
	if d.Persistence == nil {
		return ""
	}
	if !d.Type.IsNode() {
		return "device_type"
	}
	if d.Persistence.Debounce != "" {
		debounce, err := time.ParseDuration(d.Persistence.Debounce)
		if err != nil || debounce < time.Second || debounce > maxPersistenceDebounce {
			return "debounce"
		}
	}
	return ""
}

// EffectiveProfile is the profile a node runs as: an unset profile is the container. "" for a switch or a hub.
func (d Device) EffectiveProfile() NodeProfile {
	if !d.Type.IsNode() {
		return ""
	}
	if d.Profile == "" {
		return NodeProfileContainer
	}
	return d.Profile
}

// IsNode reports whether the device is a node (it runs a workload, whatever its profile).
func (d Device) IsNode() bool { return d.Type.IsNode() }

// Normalize rewrites the former container type as a node with the container profile and gives a node without a
// profile the container profile. Idempotent.
func (d *Device) Normalize() {
	if d.Type == DeviceTypeContainer {
		d.Type = DeviceTypeNode
		if d.Profile == "" {
			d.Profile = NodeProfileContainer
		}
	}
	if d.Type == DeviceTypeNode && d.Profile == "" {
		d.Profile = NodeProfileContainer
	}
}

// UnmarshalJSON reads a device and normalizes it, so stored data and archives in the former form (type container)
// load as nodes.
func (d *Device) UnmarshalJSON(data []byte) error {
	type plain Device
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*d = Device(decoded)
	d.Normalize()
	return nil
}

// NodeProfilesUsed lists the profiles of the nodes of the variants, in the order of NodeProfiles.
func NodeProfilesUsed(variants []Variant) []NodeProfile {
	seen := make(map[NodeProfile]bool, len(NodeProfiles))
	for _, variant := range variants {
		for _, device := range variant.Topology.Devices {
			if profile := device.EffectiveProfile(); profile != "" {
				seen[profile] = true
			}
		}
	}
	var out []NodeProfile
	for _, profile := range NodeProfiles {
		if seen[profile] {
			out = append(out, profile)
		}
	}
	return out
}

// NodeProfileSet is a set of node profiles, small and comparable (a field of the placement need).
type NodeProfileSet uint8

func profileBit(profile NodeProfile) NodeProfileSet {
	for i, known := range NodeProfiles {
		if known == profile {
			return 1 << i
		}
	}
	return 0
}

// Add returns the set with the profile.
func (s NodeProfileSet) Add(profile NodeProfile) NodeProfileSet { return s | profileBit(profile) }

// Union returns the profiles of both sets.
func (s NodeProfileSet) Union(o NodeProfileSet) NodeProfileSet { return s | o }

// Has reports whether the profile is in the set.
func (s NodeProfileSet) Has(profile NodeProfile) bool {
	return profile != "" && s&profileBit(profile) != 0
}

// List is the profiles of the set in the order of NodeProfiles.
func (s NodeProfileSet) List() []NodeProfile {
	var out []NodeProfile
	for _, profile := range NodeProfiles {
		if s.Has(profile) {
			out = append(out, profile)
		}
	}
	return out
}

// NodeProfileSetOf is the profiles the nodes of the variants run as.
func NodeProfileSetOf(variants []Variant) NodeProfileSet {
	var set NodeProfileSet
	for _, profile := range NodeProfilesUsed(variants) {
		set = set.Add(profile)
	}
	return set
}
