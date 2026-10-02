// Package resourcesModel is the platform's device resources model: the presets an author picks from, the
// frame every device must sit in, the ceiling an elevation may reach, and the sums the planning uses. It
// holds no agent knowledge: an agent describes its hardware, this package owns the product rules.
package resourcesModel

import (
	"fmt"
	"sort"
	"strconv"
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

// Policy is the platform settings of device resources. A device is a whole number of blocks, one block being
// Block (CPU tied to memory, so packing is one-dimensional). There is no custom size.
type Policy struct {
	// Block is one unit of a device size (its CPU rounded up to whole millicores; the exact value is blockMicro).
	Block Amount
	// blockMicro is the CPU of one block in millionths of a core: CPU is tied to memory (1 core : 4 GiB), so a
	// 64Mi block is 15625 and 16 blocks make exactly 250m.
	blockMicro int64
	// Presets in ascending order of blocks; every count divides the next, so largest-first packing leaves no hole.
	Presets       []Preset
	DefaultPreset string
	// FrameBlocks is the most a device gets without an approval; CeilingBlocks the most any approval may give.
	FrameBlocks, CeilingBlocks int
	// Frame and Ceiling are the same limits as amounts.
	Frame   Amount
	Ceiling Amount
}

// DefaultPolicy is the owner's default settings: block 64Mi / ~16m (15625u, so 16 blocks are exactly 250m); Micro 1 (default), Small 2, Medium 8, Large 16
// (the frame); with an approved elevation 32 and 64 (the ceiling, 1 CPU / 4Gi).
func DefaultPolicy() Policy {
	p, err := ParsePolicy("15625u/64Mi", "micro=1,small=2,medium=8,large=16,xlarge=32,huge=64", "micro", 16, 64)
	if err != nil {
		panic(err)
	}
	return p
}

// ParsePolicy reads the settings in the form they are configured: the block as "cpu/memory" (Kubernetes
// quantities: 16m, 64Mi), the presets as "id=blocks" separated by commas, and the frame and the ceiling in blocks.
func ParsePolicy(block, presets, defaultPreset string, frameBlocks, ceilingBlocks int) (Policy, error) {
	p := Policy{FrameBlocks: frameBlocks, CeilingBlocks: ceilingBlocks, DefaultPreset: strings.TrimSpace(defaultPreset)}
	var err error
	if p.Block, p.blockMicro, err = parseBlock(block); err != nil {
		return Policy{}, fmt.Errorf("block: %w", err)
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(presets, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, value, ok := strings.Cut(item, "=")
		id = strings.TrimSpace(id)
		count, convErr := strconv.Atoi(strings.TrimSpace(value))
		if !ok || id == "" || seen[id] || convErr != nil || count < 1 {
			return Policy{}, fmt.Errorf("preset %q: want a unique id=blocks with blocks of at least 1", item)
		}
		seen[id] = true
		p.Presets = append(p.Presets, Preset{ID: id, Blocks: count})
	}
	sort.SliceStable(p.Presets, func(i, j int) bool { return p.Presets[i].Blocks < p.Presets[j].Blocks })
	for i := range p.Presets {
		p.Presets[i].Amount = p.Amount(p.Presets[i].Blocks)
	}
	p.Frame, p.Ceiling = p.Amount(frameBlocks), p.Amount(ceilingBlocks)
	return p, p.Validate()
}

// Amount is the size of a whole number of blocks: memory exactly, CPU rounded up to whole millicores.
func (p Policy) Amount(blocks int) Amount {
	return Amount{CPUMillicores: (p.blockMicro*int64(blocks) + 999) / 1000, MemoryBytes: p.Block.MemoryBytes * int64(blocks)}
}

// BlocksOf is the whole blocks an amount takes (rounded up, over both resources): the fewest blocks whose size
// holds it.
func (p Policy) BlocksOf(a Amount) int {
	if p.blockMicro <= 0 || p.Block.MemoryBytes <= 0 {
		return 0
	}
	n := max(a.CPUMillicores*1000/p.blockMicro, (a.MemoryBytes+p.Block.MemoryBytes-1)/p.Block.MemoryBytes)
	for p.Amount(int(n)).CPUMillicores < a.CPUMillicores {
		n++
	}
	return int(n)
}

// RoundUp is the amount rounded up to whole blocks (what a group's own pods are sent and reserved with).
func (p Policy) RoundUp(a Amount) Amount {
	if a == (Amount{}) || p.blockMicro <= 0 {
		return a
	}
	return p.Amount(p.BlocksOf(a))
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

// parseBlock reads the block "cpu/memory", keeping the CPU in millionths of a core.
func parseBlock(s string) (Amount, int64, error) {
	amount, err := ParseAmount(s)
	if err != nil {
		return Amount{}, 0, err
	}
	cpu, _, _ := strings.Cut(s, "/")
	q, _ := resource.ParseQuantity(strings.TrimSpace(cpu))
	return amount, q.ScaledValue(resource.Micro), nil
}

// Validate checks the settings agree: the block is set, presets exist and every count divides the next larger one,
// the default is one of them, the frame and the ceiling are presets and the ceiling is not below the frame.
func (p Policy) Validate() error {
	if p.blockMicro <= 0 || p.Block.MemoryBytes <= 0 {
		return fmt.Errorf("the block must be a positive cpu/memory")
	}
	if len(p.Presets) == 0 {
		return fmt.Errorf("at least one preset is required")
	}
	if _, ok := p.Preset(p.DefaultPreset); !ok {
		return fmt.Errorf("the default preset %q is not among the presets", p.DefaultPreset)
	}
	for i := 1; i < len(p.Presets); i++ {
		prev, cur := p.Presets[i-1], p.Presets[i]
		if prev.Blocks == cur.Blocks {
			return fmt.Errorf("presets %q and %q have the same blocks", prev.ID, cur.ID)
		}
		if cur.Blocks%prev.Blocks != 0 {
			return fmt.Errorf("preset %q (%d blocks) is not a multiple of %q (%d blocks)", cur.ID, cur.Blocks, prev.ID, prev.Blocks)
		}
	}
	if _, ok := p.PresetByBlocks(p.FrameBlocks); !ok {
		return fmt.Errorf("the frame of %d blocks is not a preset", p.FrameBlocks)
	}
	if _, ok := p.PresetByBlocks(p.CeilingBlocks); !ok {
		return fmt.Errorf("the ceiling of %d blocks is not a preset", p.CeilingBlocks)
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
