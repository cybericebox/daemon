package infrastructure

import (
	"context"

	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// LimitsFeature is what an agent reports about its hardware and the sizing of a lab group's own pods. The
// product rules (presets, frame, ceiling) are the platform's, see resourcesModel. Zero means no limit.
type LimitsFeature struct {
	// DeviceMax is the most one container device may get; LabMaxDevices the container devices of one lab
	// (the platform needs at least resourcesModel.MaxDevicesPerLab).
	DeviceMaxCPUMillicores int64 `json:"device_max_cpu_millicores"`
	DeviceMaxMemoryBytes   int64 `json:"device_max_memory_bytes"`
	LabMaxDevices          int32 `json:"lab_max_devices"`
	TenantMaxLabs          int32 `json:"tenant_max_labs"`
	// VPN and Gateway size the group's own pods: the VPN grows with the users (peers) of the group, the
	// gateway with the labs of the group that use the internet. Each is base + units * per-unit, at most Max.
	VPN     GroupPodSizing `json:"vpn"`
	Gateway GroupPodSizing `json:"gateway"`
	// DeviceProfiles are the enabled device security profiles of the cluster.
	DeviceProfiles []string `json:"device_profiles,omitempty"`
	// DefaultVPN and DefaultGateway are the pod sizes the agent uses when a group names none.
	DefaultVPN     resourcesModel.Amount `json:"default_vpn"`
	DefaultGateway resourcesModel.Amount `json:"default_gateway"`
	SizingV2       []GroupSizingProfile  `json:"sizing_v2,omitempty"`
}

// GroupPodSizing is the size of one kind of group pod: Base plus PerUnit for every unit (a user for the VPN, an
// internet lab for the gateway), with at most MaxUnits units (0 = not reported). The pods are Guaranteed and
// created at the planned size.
type GroupPodSizing struct {
	Base     resourcesModel.Amount `json:"base"`
	PerUnit  resourcesModel.Amount `json:"per_unit"`
	MaxUnits int32                 `json:"max_units"`
}

// Reported is false for an agent that did not report this sizing (its pod then adds nothing to a plan).
func (s GroupPodSizing) Reported() bool { return s != GroupPodSizing{} }

// Size is the pod for the given number of units; units above MaxUnits are not sized (the plan is refused by
// Fits first).
func (s GroupPodSizing) Size(units int) resourcesModel.Amount {
	units = max(units, 0)
	if s.MaxUnits > 0 {
		units = min(units, int(s.MaxUnits))
	}
	return resourcesModel.Amount{
		CPUMillicores: s.Base.CPUMillicores + int64(units)*s.PerUnit.CPUMillicores,
		MemoryBytes:   s.Base.MemoryBytes + int64(units)*s.PerUnit.MemoryBytes,
	}
}

// Resources a violation can name.
const (
	FitDevices      = "devices"
	FitCPU          = "cpu"
	FitMemory       = "memory"
	FitUsers        = "users"
	FitInternetLabs = "internetLabs"
	FitDeviceCPU    = "deviceCpu"
	FitDeviceMemory = "deviceMemory"
)

// FitViolation is the first limit something passes at an agent: which resource, what it asks for and the
// limit.
type FitViolation struct {
	Resource  string
	Requested int64
	Max       int64
}

// GroupPlan is what sizes the pods of a lab group: the users (VPN peers) it serves at most, and the
// number of its labs that use the internet.
type GroupPlan struct {
	MaxUsers         int
	InternetLabs     int
	MaxActiveLabs    int
	AllowedRelations int
	ProfileID        string
	Envelope         TrafficEnvelope
}

// GroupSizes are the sizes the backend passes explicitly when it creates a lab group. They are computed
// with the formula of the agent that takes the group.
type GroupSizes struct {
	VPN     resourcesModel.Amount
	Gateway resourcesModel.Amount
}

// Total is the group's own pods together.
func (g GroupSizes) Total() resourcesModel.Amount { return g.VPN.Add(g.Gateway) }

// SizesFor computes the group's pod sizes for a plan.
func (l LimitsFeature) SizesFor(plan GroupPlan) GroupSizes {
	for _, p := range l.SizingV2 {
		if p.Eligible(plan) {
			return GroupSizes{VPN: p.VPN.Size(plan, plan.Envelope.VPNRetainedFlows), Gateway: p.Gateway.Size(plan, plan.Envelope.GatewayRetainedFlows)}
		}
	}
	return GroupSizes{VPN: l.VPN.Size(plan.MaxUsers).Max(l.DefaultVPN), Gateway: l.Gateway.Size(plan.InternetLabs).Max(l.DefaultGateway)}
}

type sizesKey struct{}

// WithGroupSizes carries the sizes of the group's own pods to the call that creates the group.
func WithGroupSizes(ctx context.Context, sizes GroupSizes) context.Context {
	return context.WithValue(ctx, sizesKey{}, sizes)
}

// GroupSizesFrom returns what WithGroupSizes stored; false when the caller set none.
func GroupSizesFrom(ctx context.Context) (GroupSizes, bool) {
	sizes, ok := ctx.Value(sizesKey{}).(GroupSizes)
	return sizes, ok
}

// PlacementNeed is what the event's group asks of an agent: the largest container device any of its tasks
// runs, the most container devices one lab has, and the plan that sizes the group's pods. A team lives on
// one agent, so the agent must fit all of it.
type PlacementNeed struct {
	Device     resourcesModel.Amount
	LabDevices int
	Plan       GroupPlan
}

// Known is true when the need says anything about the labs.
func (n PlacementNeed) Known() bool { return n != PlacementNeed{} }

type needKey struct{}

// WithPlacementNeed tells the placement of a new group what it will have to hold.
func WithPlacementNeed(ctx context.Context, need PlacementNeed) context.Context {
	return context.WithValue(ctx, needKey{}, need)
}

// PlacementNeedFrom returns what WithPlacementNeed stored.
func PlacementNeedFrom(ctx context.Context) PlacementNeed {
	need, _ := ctx.Value(needKey{}).(PlacementNeed)
	return need
}

// Fits reports the first limit of the agent that the need passes; nil when it fits.
func (l LimitsFeature) Fits(need PlacementNeed) *FitViolation {
	if l.DeviceMaxCPUMillicores > 0 && need.Device.CPUMillicores > l.DeviceMaxCPUMillicores {
		return &FitViolation{Resource: FitCPU, Requested: need.Device.CPUMillicores, Max: l.DeviceMaxCPUMillicores}
	}
	if l.DeviceMaxMemoryBytes > 0 && need.Device.MemoryBytes > l.DeviceMaxMemoryBytes {
		return &FitViolation{Resource: FitMemory, Requested: need.Device.MemoryBytes, Max: l.DeviceMaxMemoryBytes}
	}
	if l.LabMaxDevices > 0 && need.LabDevices > int(l.LabMaxDevices) {
		return &FitViolation{Resource: FitDevices, Requested: int64(need.LabDevices), Max: int64(l.LabMaxDevices)}
	}
	if l.VPN.MaxUnits > 0 && need.Plan.MaxUsers > int(l.VPN.MaxUnits) {
		return &FitViolation{Resource: FitUsers, Requested: int64(need.Plan.MaxUsers), Max: int64(l.VPN.MaxUnits)}
	}
	if l.Gateway.MaxUnits > 0 && need.Plan.InternetLabs > int(l.Gateway.MaxUnits) {
		return &FitViolation{Resource: FitInternetLabs, Requested: int64(need.Plan.InternetLabs), Max: int64(l.Gateway.MaxUnits)}
	}
	return nil
}

// UnmetRequirements lists where the agent is below what the platform requires: a device maximum under the
// frame, or fewer than resourcesModel.MaxDevicesPerLab devices per lab. Empty means it meets them. A limit
// of 0 is no limit.
func (l LimitsFeature) UnmetRequirements(frame resourcesModel.Amount) []FitViolation {
	var out []FitViolation
	if l.DeviceMaxCPUMillicores > 0 && l.DeviceMaxCPUMillicores < frame.CPUMillicores {
		out = append(out, FitViolation{Resource: FitDeviceCPU, Requested: frame.CPUMillicores, Max: l.DeviceMaxCPUMillicores})
	}
	if l.DeviceMaxMemoryBytes > 0 && l.DeviceMaxMemoryBytes < frame.MemoryBytes {
		out = append(out, FitViolation{Resource: FitDeviceMemory, Requested: frame.MemoryBytes, Max: l.DeviceMaxMemoryBytes})
	}
	if l.LabMaxDevices > 0 && l.LabMaxDevices < resourcesModel.MaxDevicesPerLab {
		out = append(out, FitViolation{Resource: FitDevices, Requested: resourcesModel.MaxDevicesPerLab, Max: int64(l.LabMaxDevices)})
	}
	return out
}
