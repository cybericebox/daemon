// Package resourcesModel is the platform's device resources model: the presets an author picks from, the
// frame every device must sit in, the ceiling an elevation may reach, and the sums the planning uses. It
// holds no agent knowledge: an agent describes its hardware, this package owns the product rules.
package resourcesModel

import (
	"fmt"
	"strings"

	"github.com/gofrs/uuid"
	"k8s.io/apimachinery/pkg/api/resource"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Constants of the laboratory, not settings: they are the same on every agent.
const (
	// MaxDevicesPerLab is the container devices one lab holds.
	MaxDevicesPerLab = 32
	// InterfacesPerContainerDevice and PortsPerSwitch are the laboratory's wiring limits.
	InterfacesPerContainerDevice = 16
	PortsPerSwitch               = 48
)

// Amount is the CPU and memory of one device, or a sum of them.
type Amount struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
}

// Add returns the sum.
func (a Amount) Add(b Amount) Amount {
	return Amount{CPUMillicores: a.CPUMillicores + b.CPUMillicores, MemoryBytes: a.MemoryBytes + b.MemoryBytes}
}

// Max is the larger of each resource.
func (a Amount) Max(b Amount) Amount {
	return Amount{CPUMillicores: max(a.CPUMillicores, b.CPUMillicores), MemoryBytes: max(a.MemoryBytes, b.MemoryBytes)}
}

// Min is the smaller of each resource.
func (a Amount) Min(b Amount) Amount {
	return Amount{CPUMillicores: min(a.CPUMillicores, b.CPUMillicores), MemoryBytes: min(a.MemoryBytes, b.MemoryBytes)}
}

// Within reports whether neither resource passes the limit.
func (a Amount) Within(limit Amount) bool {
	return a.CPUMillicores <= limit.CPUMillicores && a.MemoryBytes <= limit.MemoryBytes
}

// Totals is what a topology (or a task version) needs: its container devices and their sum.
type Totals struct {
	Devices int
	Amount
}

// Add returns the sum.
func (t Totals) Add(o Totals) Totals {
	return Totals{Devices: t.Devices + o.Devices, Amount: t.Amount.Add(o.Amount)}
}

// Max is the larger of each field.
func (t Totals) Max(o Totals) Totals {
	return Totals{Devices: max(t.Devices, o.Devices), Amount: t.Amount.Max(o.Amount)}
}

// Min is the smaller of each field.
func (t Totals) Min(o Totals) Totals {
	return Totals{Devices: min(t.Devices, o.Devices), Amount: t.Amount.Min(o.Amount)}
}

// Preset is a named device size an author picks.
type Preset struct {
	ID string
	Amount
}

// Policy is the platform settings of device resources.
type Policy struct {
	// Presets in the order they are offered; the last one is the frame in the default settings.
	Presets       []Preset
	DefaultPreset string
	// Frame is the most a device gets without an approval.
	Frame Amount
	// Ceiling is the most any approval may give a device.
	Ceiling Amount
}

// DefaultPolicy is the owner's default settings: Micro 25m/64Mi (default), Small 50m/128Mi, Medium
// 125m/512Mi, Large 250m/1Gi (the frame), elevation ceiling 1 CPU / 4Gi.
func DefaultPolicy() Policy {
	const mi, gi = 1 << 20, 1 << 30
	return Policy{
		Presets: []Preset{
			{ID: "micro", Amount: Amount{25, 64 * mi}},
			{ID: "small", Amount: Amount{50, 128 * mi}},
			{ID: "medium", Amount: Amount{125, 512 * mi}},
			{ID: "large", Amount: Amount{250, gi}},
		},
		DefaultPreset: "micro",
		Frame:         Amount{250, gi},
		Ceiling:       Amount{1000, 4 * gi},
	}
}

// ParsePolicy reads the settings in the form they are configured: presets as "id=cpu/memory" separated by
// commas, the frame and the ceiling as "cpu/memory" (Kubernetes quantities: 25m, 1, 64Mi, 4Gi).
func ParsePolicy(presets, defaultPreset, frame, ceiling string) (Policy, error) {
	var p Policy
	seen := map[string]bool{}
	for _, item := range strings.Split(presets, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, value, ok := strings.Cut(item, "=")
		id = strings.TrimSpace(id)
		if !ok || id == "" || seen[id] {
			return Policy{}, fmt.Errorf("preset %q: want a unique id=cpu/memory", item)
		}
		amount, err := ParseAmount(value)
		if err != nil {
			return Policy{}, fmt.Errorf("preset %q: %w", id, err)
		}
		seen[id] = true
		p.Presets = append(p.Presets, Preset{ID: id, Amount: amount})
	}
	var err error
	if p.Frame, err = ParseAmount(frame); err != nil {
		return Policy{}, fmt.Errorf("frame: %w", err)
	}
	if p.Ceiling, err = ParseAmount(ceiling); err != nil {
		return Policy{}, fmt.Errorf("ceiling: %w", err)
	}
	p.DefaultPreset = strings.TrimSpace(defaultPreset)
	return p, p.Validate()
}

// ParseAmount reads "cpu/memory".
func ParseAmount(s string) (Amount, error) {
	cpu, mem, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok {
		return Amount{}, fmt.Errorf("%q: want cpu/memory", s)
	}
	cpuQ, err := resource.ParseQuantity(strings.TrimSpace(cpu))
	if err != nil || cpuQ.Sign() <= 0 {
		return Amount{}, fmt.Errorf("%q: cpu must be a positive quantity", cpu)
	}
	memQ, err := resource.ParseQuantity(strings.TrimSpace(mem))
	if err != nil || memQ.Sign() <= 0 {
		return Amount{}, fmt.Errorf("%q: memory must be a positive quantity", mem)
	}
	return Amount{CPUMillicores: cpuQ.MilliValue(), MemoryBytes: memQ.Value()}, nil
}

// Validate checks the settings agree: presets exist, the default is one of them, every preset sits in the
// frame, the frame sits in the ceiling.
func (p Policy) Validate() error {
	if len(p.Presets) == 0 {
		return fmt.Errorf("at least one preset is required")
	}
	if _, ok := p.Preset(p.DefaultPreset); !ok {
		return fmt.Errorf("the default preset %q is not among the presets", p.DefaultPreset)
	}
	for _, preset := range p.Presets {
		if !preset.Within(p.Frame) {
			return fmt.Errorf("preset %q is above the frame", preset.ID)
		}
	}
	if !p.Frame.Within(p.Ceiling) {
		return fmt.Errorf("the frame is above the elevation ceiling")
	}
	return nil
}

// Preset finds a preset by id.
func (p Policy) Preset(id string) (Preset, bool) {
	for _, preset := range p.Presets {
		if preset.ID == id {
			return preset, true
		}
	}
	return Preset{}, false
}

// Default is the device size of a device that picked nothing.
func (p Policy) Default() Amount {
	preset, _ := p.Preset(p.DefaultPreset)
	return preset.Amount
}

// Resolve is what a device gets: its preset; else its custom values (the limit, else the request, per
// resource; requests always equal limits); else the default preset. An unknown preset id resolves to the
// default (publishing refuses it, see InvalidPreset).
func (p Policy) Resolve(d exerciseModel.Device) Amount {
	if d.ResourcePreset != "" {
		if preset, ok := p.Preset(d.ResourcePreset); ok {
			return preset.Amount
		}
		return p.Default()
	}
	out := p.Default()
	if r := d.Resources; r != nil {
		if v, ok := quantity(first(r.CPULimit, r.CPURequest), true); ok {
			out.CPUMillicores = v
		}
		if v, ok := quantity(first(r.MemoryLimit, r.MemoryRequest), false); ok {
			out.MemoryBytes = v
		}
	}
	return out
}

// InvalidPreset reports a preset id the platform does not offer.
func (p Policy) InvalidPreset(d exerciseModel.Device) bool {
	if d.ResourcePreset == "" {
		return false
	}
	_, ok := p.Preset(d.ResourcePreset)
	return !ok
}

// Explicit is the resources the backend sends to the agent: requests equal limits, always set.
func (p Policy) Explicit(d exerciseModel.Device) *exerciseModel.DeviceResources {
	a := p.Resolve(d)
	cpu := resource.NewMilliQuantity(a.CPUMillicores, resource.DecimalSI).String()
	mem := resource.NewQuantity(a.MemoryBytes, resource.BinarySI).String()
	return &exerciseModel.DeviceResources{CPURequest: cpu, CPULimit: cpu, MemoryRequest: mem, MemoryLimit: mem}
}

// DeviceUsage is one container device of a topology with what it gets.
type DeviceUsage struct {
	DeviceID uuid.UUID
	Name     string
	Amount
}

// Devices lists the container devices of a topology (switches and hubs run no pod).
func (p Policy) Devices(t exerciseModel.Topology) []DeviceUsage {
	var out []DeviceUsage
	for _, d := range t.Devices {
		if d.Type != exerciseModel.DeviceTypeContainer {
			continue
		}
		out = append(out, DeviceUsage{DeviceID: d.ID, Name: d.Name, Amount: p.Resolve(d)})
	}
	return out
}

// Total is the container devices of a topology and the sum of their resources.
func (p Policy) Total(t exerciseModel.Topology) Totals {
	var total Totals
	for _, d := range p.Devices(t) {
		total.Devices++
		total.Amount = total.Amount.Add(d.Amount)
	}
	return total
}

// Range is the least and the most a task needs over its variants.
type Range struct {
	Min, Max Totals
}

// VariantRange folds the totals of every variant; a task without variants needs nothing.
func (p Policy) VariantRange(variants []exerciseModel.Variant) Range {
	var r Range
	for i, v := range variants {
		total := p.Total(v.Topology)
		if i == 0 {
			r = Range{Min: total, Max: total}
			continue
		}
		r.Min, r.Max = r.Min.Min(total), r.Max.Max(total)
	}
	return r
}

// Outside is a device that does not sit in the frame.
type Outside struct {
	VariantID uuid.UUID
	DeviceID  uuid.UUID
	Name      string
	Amount
	// AboveCeiling: no approval can cover it.
	AboveCeiling bool
}

// OutsideFrame lists the devices of the variants that pass the frame.
func (p Policy) OutsideFrame(variants []exerciseModel.Variant) []Outside {
	var out []Outside
	for _, v := range variants {
		for _, d := range p.Devices(v.Topology) {
			if !d.Amount.Within(p.Frame) {
				out = append(out, Outside{VariantID: v.ID, DeviceID: d.DeviceID, Name: d.Name, Amount: d.Amount, AboveCeiling: !d.Amount.Within(p.Ceiling)})
			}
		}
	}
	return out
}

// Approval is the values approved for one device.
type Approval struct {
	DeviceID uuid.UUID `json:"device_id"`
	Name     string    `json:"name"`
	Amount
}

// Covered reports whether the device is inside the approved values (each resource at or below them).
func Covered(o Outside, approved []Approval) bool {
	for _, a := range approved {
		if a.DeviceID == o.DeviceID && o.Amount.Within(a.Amount) {
			return true
		}
	}
	return false
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
	if err != nil || q.Sign() <= 0 {
		return 0, false
	}
	if cpu {
		return q.MilliValue(), true
	}
	return q.Value(), true
}
