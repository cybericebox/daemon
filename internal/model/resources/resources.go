// Package resourcesModel is the platform's device resources model: the presets an author picks from, the
// frame every device must sit in, the ceiling an elevation may reach, and the sums the planning uses. It
// holds no agent knowledge: an agent describes its hardware, this package owns the product rules.
package resourcesModel

import (
	"fmt"
	"sort"
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

// Totals is what a topology (or a task version) needs: its container devices, their sum and the same sum in
// whole blocks.
type Totals struct {
	Devices int
	// Blocks is the sum of the devices' blocks (a block is the platform's unit of one device size).
	Blocks int
	Amount
}

// Add returns the sum.
func (t Totals) Add(o Totals) Totals {
	return Totals{Devices: t.Devices + o.Devices, Blocks: t.Blocks + o.Blocks, Amount: t.Amount.Add(o.Amount)}
}

// Max is the larger of each field.
func (t Totals) Max(o Totals) Totals {
	return Totals{Devices: max(t.Devices, o.Devices), Blocks: max(t.Blocks, o.Blocks), Amount: t.Amount.Max(o.Amount)}
}

// Min is the smaller of each field.
func (t Totals) Min(o Totals) Totals {
	return Totals{Devices: min(t.Devices, o.Devices), Blocks: min(t.Blocks, o.Blocks), Amount: t.Amount.Min(o.Amount)}
}

// Preset is a named device size an author picks: a whole number of blocks.
type Preset struct {
	ID     string
	Blocks int
	Amount
}

// MilliCPUPerGiB is the CPU tied to memory: 1000m per 4Gi, so a size has memory/4Gi x 1000m of CPU, rounded DOWN
// to whole millicores. CPU is not configured on its own. With nodes of 1 core per 2-4 GiB or more, memory always
// binds first, so packing stays one-dimensional and memory (binary, halving) leaves no hole.
const milliCPUPerGiB = 250

// cpuFor is the CPU of a memory size by the rule above.
func cpuFor(memoryBytes int64) int64 {
	return memoryBytes * milliCPUPerGiB / (1 << 30)
}

// Policy is the platform settings of device resources. A device is one of the preset sizes (a block being the
// smallest of them); there is no custom size.
type Policy struct {
	// Block is the smallest preset: the unit Blocks count in.
	Block Amount
	// Presets in ascending order of memory; every size divides the next, so largest-first packing leaves no hole.
	Presets       []Preset
	DefaultPreset string
	// FrameBlocks is the most a device gets without an approval; CeilingBlocks the most any approval may give.
	FrameBlocks, CeilingBlocks int
	// Frame and Ceiling are the same limits as amounts.
	Frame   Amount
	Ceiling Amount
}

// DefaultPolicy is the owner's default settings: nano 32Mi/7m, micro 64Mi/15m (default), small 128Mi/31m,
// standard 256Mi/62m, medium 512Mi/125m, large 1Gi/250m (the frame); with an approved elevation xlarge
// 2Gi/500m and max 4Gi/1000m (the ceiling).
func DefaultPolicy() Policy {
	p, err := ParsePolicy("nano=32Mi,micro=64Mi,small=128Mi,standard=256Mi,medium=512Mi,large=1Gi,xlarge=2Gi,max=4Gi", "micro", "large", "max")
	if err != nil {
		panic(err)
	}
	return p
}

// ParsePolicy reads the settings in the form they are configured: the presets as "id=memory" (Kubernetes
// quantities, e.g. 64Mi) separated by commas, and the ids of the default, frame and ceiling presets. The CPU of
// every size follows from its memory.
func ParsePolicy(presets, defaultPreset, framePreset, ceilingPreset string) (Policy, error) {
	p := Policy{DefaultPreset: strings.TrimSpace(defaultPreset)}
	seen := map[string]bool{}
	for _, item := range strings.Split(presets, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, value, ok := strings.Cut(item, "=")
		id = strings.TrimSpace(id)
		q, err := resource.ParseQuantity(strings.TrimSpace(value))
		if !ok || id == "" || seen[id] || err != nil || q.Sign() <= 0 {
			return Policy{}, fmt.Errorf("preset %q: want a unique id=memory with a positive memory", item)
		}
		seen[id] = true
		mem := q.Value()
		p.Presets = append(p.Presets, Preset{ID: id, Amount: Amount{CPUMillicores: cpuFor(mem), MemoryBytes: mem}})
	}
	sort.SliceStable(p.Presets, func(i, j int) bool { return p.Presets[i].MemoryBytes < p.Presets[j].MemoryBytes })
	if len(p.Presets) == 0 {
		return Policy{}, fmt.Errorf("at least one preset is required")
	}
	p.Block = p.Presets[0].Amount
	for i := range p.Presets {
		p.Presets[i].Blocks = int(p.Presets[i].MemoryBytes / p.Block.MemoryBytes)
	}
	frame, ok := p.Preset(strings.TrimSpace(framePreset))
	if !ok {
		return Policy{}, fmt.Errorf("the frame preset %q is not among the presets", framePreset)
	}
	ceiling, ok := p.Preset(strings.TrimSpace(ceilingPreset))
	if !ok {
		return Policy{}, fmt.Errorf("the ceiling preset %q is not among the presets", ceilingPreset)
	}
	p.FrameBlocks, p.Frame, p.CeilingBlocks, p.Ceiling = frame.Blocks, frame.Amount, ceiling.Blocks, ceiling.Amount
	return p, p.Validate()
}

// Amount is the size of a whole number of blocks: memory exactly, CPU by the rule.
func (p Policy) Amount(blocks int) Amount {
	mem := p.Block.MemoryBytes * int64(blocks)
	return Amount{CPUMillicores: cpuFor(mem), MemoryBytes: mem}
}

// fit is the smallest preset that holds the amount in both resources.
func (p Policy) fit(a Amount) (Preset, bool) {
	for _, preset := range p.Presets {
		if a.Within(preset.Amount) {
			return preset, true
		}
	}
	return Preset{}, false
}

// offers reports whether the amount is exactly one of the preset sizes.
func (p Policy) offers(a Amount) bool {
	for _, preset := range p.Presets {
		if preset.Amount == a {
			return true
		}
	}
	return false
}

// BlocksOf is the blocks of the smallest preset that holds the amount; above the largest preset, the blocks of its
// memory rounded up.
func (p Policy) BlocksOf(a Amount) int {
	if p.Block.MemoryBytes <= 0 || a == (Amount{}) {
		return 0
	}
	if preset, ok := p.fit(a); ok {
		return preset.Blocks
	}
	return int((a.MemoryBytes + p.Block.MemoryBytes - 1) / p.Block.MemoryBytes)
}

// RoundUp is the amount rounded up to the next preset memory size, with its CPU by the rule; when that CPU is
// below what the amount needs the next size up is taken. This is what a group's own pods are sent and reserved with.
// Above the largest preset the amount is left as it is.
func (p Policy) RoundUp(a Amount) Amount {
	if a == (Amount{}) {
		return a
	}
	if preset, ok := p.fit(a); ok {
		return preset.Amount
	}
	return a
}

// Validate checks the settings agree: presets exist and every size divides the next larger one, the default is
// one of them, and the ceiling is not below the frame.
func (p Policy) Validate() error {
	if len(p.Presets) == 0 {
		return fmt.Errorf("at least one preset is required")
	}
	if _, ok := p.Preset(p.DefaultPreset); !ok {
		return fmt.Errorf("the default preset %q is not among the presets", p.DefaultPreset)
	}
	for i := 1; i < len(p.Presets); i++ {
		prev, cur := p.Presets[i-1], p.Presets[i]
		if prev.MemoryBytes == cur.MemoryBytes {
			return fmt.Errorf("presets %q and %q have the same memory", prev.ID, cur.ID)
		}
		if cur.MemoryBytes%prev.MemoryBytes != 0 {
			return fmt.Errorf("preset %q is not a multiple of %q", cur.ID, prev.ID)
		}
	}
	if p.CeilingBlocks < p.FrameBlocks {
		return fmt.Errorf("the ceiling is below the frame")
	}
	if last := p.Presets[len(p.Presets)-1]; last.Blocks > p.CeilingBlocks {
		return fmt.Errorf("preset %q is above the ceiling", last.ID)
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

// PresetByBlocks finds the preset of a block count.
func (p Policy) PresetByBlocks(blocks int) (Preset, bool) {
	for _, preset := range p.Presets {
		if preset.Blocks == blocks {
			return preset, true
		}
	}
	return Preset{}, false
}

// Largest is the largest preset whose size fits within the limit (what an agent that can place at most that
// big a device allows); false when even the smallest does not.
func (p Policy) Largest(limit Amount) (Preset, bool) {
	var found Preset
	ok := false
	for _, preset := range p.Presets {
		if preset.Amount.Within(limit) {
			found, ok = preset, true
		}
	}
	return found, ok
}

// Default is the device size of a device that picked nothing.
func (p Policy) Default() Amount {
	preset, _ := p.Preset(p.DefaultPreset)
	return preset.Amount
}

func (p Policy) resolvePreset(d exerciseModel.Device) Preset {
	if preset, ok := p.Preset(d.ResourcePreset); ok {
		return preset
	}
	preset, _ := p.Preset(p.DefaultPreset)
	return preset
}

// Resolve is what a device gets: its preset; an empty or unknown preset id resolves to the default (publishing
// refuses an unknown one, see InvalidPreset). There is no custom size.
func (p Policy) Resolve(d exerciseModel.Device) Amount {
	return p.resolvePreset(d).Amount
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

// Explicitly returns a copy of the topology in which every container device carries explicit resources
// (requests equal limits) and no preset: what the agent is sent.
func (p Policy) Explicitly(t exerciseModel.Topology) exerciseModel.Topology {
	devices := make([]exerciseModel.Device, len(t.Devices))
	copy(devices, t.Devices)
	for i, d := range devices {
		if d.Type != exerciseModel.DeviceTypeContainer {
			continue
		}
		devices[i].Resources = p.Explicit(d)
		devices[i].ResourcePreset = ""
	}
	t.Devices = devices
	return t
}

// DeviceUsage is one container device of a topology with what it gets.
type DeviceUsage struct {
	DeviceID uuid.UUID
	Name     string
	Blocks   int
	Amount
}

// Devices lists the container devices of a topology (switches and hubs run no pod).
func (p Policy) Devices(t exerciseModel.Topology) []DeviceUsage {
	var out []DeviceUsage
	for _, d := range t.Devices {
		if d.Type != exerciseModel.DeviceTypeContainer {
			continue
		}
		preset := p.resolvePreset(d)
		out = append(out, DeviceUsage{DeviceID: d.ID, Name: d.Name, Blocks: preset.Blocks, Amount: preset.Amount})
	}
	return out
}

// Total is the container devices of a topology and the sum of their resources.
func (p Policy) Total(t exerciseModel.Topology) Totals {
	var total Totals
	for _, d := range p.Devices(t) {
		total.Devices++
		total.Blocks += d.Blocks
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
	Blocks    int
	Amount
	// AboveCeiling: no approval can cover it.
	AboveCeiling bool
}

// OutsideFrame lists the devices of the variants that pass the frame.
func (p Policy) OutsideFrame(variants []exerciseModel.Variant) []Outside {
	var out []Outside
	for _, v := range variants {
		for _, d := range p.Devices(v.Topology) {
			if d.Blocks > p.FrameBlocks {
				out = append(out, Outside{VariantID: v.ID, DeviceID: d.DeviceID, Name: d.Name, Blocks: d.Blocks, Amount: d.Amount, AboveCeiling: d.Blocks > p.CeilingBlocks})
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
