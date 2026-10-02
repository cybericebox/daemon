package infrastructure

import (
	"context"

	"k8s.io/apimachinery/pkg/api/resource"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// LimitsFeature are the caps an agent reports for its tenant. Zero means no limit.
type LimitsFeature struct {
	DeviceMaxCPUMillicores     int64 `json:"device_max_cpu_millicores"`
	DeviceMaxMemoryBytes       int64 `json:"device_max_memory_bytes"`
	DeviceDefaultCPUMillicores int64 `json:"device_default_cpu_millicores"`
	// DeviceDefaultMemoryBytes is the profile of a device that sets no resources: the capacity estimate uses it.
	DeviceDefaultMemoryBytes int64 `json:"device_default_memory_bytes"`
	LabMaxDevices            int32 `json:"lab_max_devices"`
	// A group is a team's namespace: all its labs together.
	GroupMaxLabs          int32 `json:"group_max_labs"`
	GroupMaxCPUMillicores int64 `json:"group_max_cpu_millicores"`
	GroupMaxMemoryBytes   int64 `json:"group_max_memory_bytes"`
	TenantMaxLabs         int32 `json:"tenant_max_labs"`
}

// Resources a violation can name.
const (
	FitDevices     = "devices"
	FitCPU         = "cpu"
	FitMemory      = "memory"
	FitGroupLabs   = "groupLabs"
	FitGroupCPU    = "groupCpu"
	FitGroupMemory = "groupMemory"
)

// DeviceNeed is what one container device asks for; zero = it sets nothing and gets the agent's default profile.
type DeviceNeed struct {
	Name           string
	CPUMillicores  int64
	MemoryBytes    int64
	cpuSet, memSet bool
}

// Demand is the resources of one lab: its container devices (switches and hubs run no pod).
type Demand struct {
	Devices []DeviceNeed
}

// DemandOf reads what a topology asks for: a device's limit, else its request, else nothing (the agent's
// default profile). Devices run Guaranteed, so the limit is what counts.
func DemandOf(topo exerciseModel.Topology) Demand {
	var d Demand
	for _, dev := range topo.Devices {
		if dev.Type != exerciseModel.DeviceTypeContainer {
			continue
		}
		need := DeviceNeed{Name: dev.Name}
		if r := dev.Resources; r != nil {
			if v, ok := quantity(first(r.CPULimit, r.CPURequest), true); ok {
				need.CPUMillicores, need.cpuSet = v, true
			}
			if v, ok := quantity(first(r.MemoryLimit, r.MemoryRequest), false); ok {
				need.MemoryBytes, need.memSet = v, true
			}
		}
		d.Devices = append(d.Devices, need)
	}
	return d
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func quantity(s string, cpu bool) (int64, bool) {
	if s == "" {
		return 0, false
	}
	q, err := resource.ParseQuantity(s)
	if err != nil || q.Sign() < 0 {
		return 0, false
	}
	if cpu {
		return q.MilliValue(), true
	}
	return q.Value(), true
}

// Effective is the device's CPU and memory with the agent's default profile where it sets none.
func (l LimitsFeature) Effective(n DeviceNeed) (cpu, memory int64) {
	cpu, memory = n.CPUMillicores, n.MemoryBytes
	if !n.cpuSet {
		cpu = l.DeviceDefaultCPUMillicores
	}
	if !n.memSet {
		memory = l.DeviceDefaultMemoryBytes
	}
	return cpu, memory
}

// FitViolation is the first cap a lab passes at an agent: the device (empty for a lab-wide cap), which
// resource, what the lab asks for and the limit.
type FitViolation struct {
	Device    string
	Resource  string
	Requested int64
	Max       int64
}

// CheckLab mirrors the agent's own check of one lab: its device count and each device against the device
// caps. It returns the planned totals so a group can add its labs up. Nil means the lab fits.
func (l LimitsFeature) CheckLab(d Demand) (v *FitViolation, cpuTotal, memTotal int64) {
	if l.LabMaxDevices > 0 && len(d.Devices) > int(l.LabMaxDevices) {
		return &FitViolation{Resource: FitDevices, Requested: int64(len(d.Devices)), Max: int64(l.LabMaxDevices)}, 0, 0
	}
	for _, n := range d.Devices {
		cpu, mem := l.Effective(n)
		if l.DeviceMaxCPUMillicores > 0 && cpu > l.DeviceMaxCPUMillicores {
			return &FitViolation{Device: n.Name, Resource: FitCPU, Requested: cpu, Max: l.DeviceMaxCPUMillicores}, 0, 0
		}
		if l.DeviceMaxMemoryBytes > 0 && mem > l.DeviceMaxMemoryBytes {
			return &FitViolation{Device: n.Name, Resource: FitMemory, Requested: mem, Max: l.DeviceMaxMemoryBytes}, 0, 0
		}
		cpuTotal, memTotal = cpuTotal+cpu, memTotal+mem
	}
	return nil, cpuTotal, memTotal
}

// Check is one lab alone in a group.
func (l LimitsFeature) Check(d Demand) *FitViolation {
	return l.Fits(PlacementNeed{Labs: []Demand{d}})
}

// FitWarning says one agent cannot run a topology.
type FitWarning struct {
	Agent string
	FitViolation
}

// TopologyFit is how a topology sits on the enabled agents. FitsAny is false only when every agent that
// reported its limits refuses it; an agent that has not reported is never held against a topology.
type TopologyFit struct {
	FitsAny  bool
	Warnings []FitWarning
}

// PlacementNeed is every lab a team's group will hold (the tasks of an event): the agent that takes the
// group must fit them all.
type PlacementNeed struct {
	Labs []Demand
}

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

// Fits reports whether the agent can hold every lab of the need in one group: each lab within the device
// caps, and the labs together within the group's caps (their number, planned CPU and memory).
func (l LimitsFeature) Fits(need PlacementNeed) *FitViolation {
	var sumCPU, sumMem int64
	for _, lab := range need.Labs {
		v, cpu, mem := l.CheckLab(lab)
		if v != nil {
			return v
		}
		sumCPU, sumMem = sumCPU+cpu, sumMem+mem
	}
	if l.GroupMaxLabs > 0 && len(need.Labs) > int(l.GroupMaxLabs) {
		return &FitViolation{Resource: FitGroupLabs, Requested: int64(len(need.Labs)), Max: int64(l.GroupMaxLabs)}
	}
	if l.GroupMaxCPUMillicores > 0 && sumCPU > l.GroupMaxCPUMillicores {
		return &FitViolation{Resource: FitGroupCPU, Requested: sumCPU, Max: l.GroupMaxCPUMillicores}
	}
	if l.GroupMaxMemoryBytes > 0 && sumMem > l.GroupMaxMemoryBytes {
		return &FitViolation{Resource: FitGroupMemory, Requested: sumMem, Max: l.GroupMaxMemoryBytes}
	}
	return nil
}
