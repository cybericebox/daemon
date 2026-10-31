package labagent

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"google.golang.org/protobuf/reflect/protoreflect"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// ProfileCapacity is what one agent reports about a node profile.
type ProfileCapacity struct {
	// AvailableNow: a running, schedulable node can run it.
	AvailableNow bool
	// ScaleUpKnown and AvailableAfterScaleUp: the agent knows which profiles its autoscaler node groups can add and
	// says whether one can add a node for this profile.
	ScaleUpKnown          bool
	AvailableAfterScaleUp bool
	// Overhead is added to every pod of the profile.
	Overhead resourcesModel.Amount
}

// Runnable reports whether a lab with the profile is accepted, now or after the autoscaler adds a node.
func (c ProfileCapacity) Runnable() bool {
	return c.AvailableNow || (c.ScaleUpKnown && c.AvailableAfterScaleUp)
}

// ProfileCell holds the last node profile report of an agent. Copies of a Member share it. A nil cell holds
// nothing.
type ProfileCell struct {
	mu       sync.RWMutex
	reported bool
	at       time.Time
	v        map[exerciseModel.NodeProfile]ProfileCapacity
}

func NewProfileCell() *ProfileCell { return &ProfileCell{} }

// Set stores a report (a profile absent from it is not offered).
func (c *ProfileCell) Set(v map[exerciseModel.NodeProfile]ProfileCapacity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reported, c.v = true, v
}

// SetAt stores a report received at the time unless the cell holds a newer one; it tells whether it was stored.
func (c *ProfileCell) SetAt(v map[exerciseModel.NodeProfile]ProfileCapacity, at time.Time) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reported && c.at.After(at) {
		return false
	}
	c.reported, c.at, c.v = true, at, v
	return true
}

// Get reports what the agent said about the profile: known is false before the first report, then a profile the
// agent does not list is not runnable.
func (c *ProfileCell) Get(
	profile exerciseModel.NodeProfile,
) (capacity ProfileCapacity, known bool) {
	if c == nil {
		return ProfileCapacity{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.v[profile], c.reported
}

// ProfilesOf reads the node profiles of a capacity report. An agent that sends none is an older one: the container
// is available, nothing else is offered. The field is read by name, so this builds against a laboratory SDK older
// than the field (it then reads as an older agent); with the pinned release that has it, use GetNodeProfiles.
func ProfilesOf(capacity *labpb.CapacityResponse) map[exerciseModel.NodeProfile]ProfileCapacity {
	out := map[exerciseModel.NodeProfile]ProfileCapacity{} //nolint:exhaustive // filled from the report
	message := capacity.ProtoReflect()
	if field := message.Descriptor().Fields().ByName("node_profiles"); field != nil {
		list := message.Get(field).List()
		for i := range list.Len() {
			item := list.Get(i).Message()
			fields := item.Descriptor().Fields()
			get := func(name string) protoreflect.Value { return item.Get(fields.ByName(protoreflect.Name(name))) }
			profile := exerciseModel.NodeProfile(get("profile").String())
			if !profile.Valid() {
				continue
			}
			out[profile] = ProfileCapacity{
				AvailableNow:          get("available_now").Bool(),
				ScaleUpKnown:          get("scale_up_known").Bool(),
				AvailableAfterScaleUp: get("available_after_scale_up").Bool(),
				Overhead: resourcesModel.Amount{
					CPUMillicores: get("overhead_cpu_millicores").Int(),
					MemoryBytes:   get("overhead_memory_bytes").Int(),
				},
			}
		}
	}
	if len(out) == 0 {
		out[exerciseModel.NodeProfileContainer] = ProfileCapacity{AvailableNow: true}
	}
	return out
}

// RunnableNodeProfiles says, per profile, whether an enabled agent of the fleet can run it now.
func (f *Fleet) RunnableNodeProfiles(context.Context) map[exerciseModel.NodeProfile]bool {
	out := make(map[exerciseModel.NodeProfile]bool, len(exerciseModel.NodeProfiles))
	for _, m := range f.Members() {
		if !m.Enabled {
			continue
		}
		for _, profile := range exerciseModel.NodeProfiles {
			if capacity, known := m.Profiles.Get(profile); known && capacity.Runnable() {
				out[profile] = true
			}
		}
	}
	return out
}

// requireProfiles refuses a topology that asks for a node profile the lab cannot get: the VM profile is not run yet,
// and another one needs an agent that reports it runnable. An agent that has not reported yet is not refused here:
// it answers for itself when the lab is created.
func requireProfiles(m *Member, topo exerciseModel.Topology) error {
	for _, d := range topo.Devices {
		profile := d.EffectiveProfile()
		switch profile { //nolint:exhaustive // the other profiles take the default
		case "", exerciseModel.NodeProfileContainer:
		case exerciseModel.NodeProfileVM:
			return exerciseModel.ErrNodeProfileVMUnsupported.WithPublicContext("device", d.Name).
				WithPublicContext("profile", string(profile)).
				Err()
		default:
			if capacity, known := m.Profiles.Get(profile); known && !capacity.Runnable() {
				return exerciseModel.ErrNodeProfileUnavailable.WithPublicContext("device", d.Name).
					WithPublicContext("profile", string(profile)).
					Err()
			}
		}
	}
	return nil
}

// requireProfilesSomewhere is the same refusal before an agent is chosen: it stands when no enabled agent that has
// reported can run a profile of the topology.
func (f *Fleet) requireProfilesSomewhere(topo exerciseModel.Topology) error {
	var members []*Member
	for _, m := range f.Members() {
		if m.Enabled {
			members = append(members, m)
		}
	}
	for _, d := range topo.Devices {
		profile := d.EffectiveProfile()
		if profile == "" || profile == exerciseModel.NodeProfileContainer {
			continue
		}
		if profile == exerciseModel.NodeProfileVM {
			return exerciseModel.ErrNodeProfileVMUnsupported.WithPublicContext("device", d.Name).
				WithPublicContext("profile", string(profile)).
				Err()
		}
		runnable := false
		for _, m := range members {
			capacity, known := m.Profiles.Get(profile)
			if !known || capacity.Runnable() {
				runnable = true
			}
		}
		if len(members) > 0 && !runnable {
			return exerciseModel.ErrNodeProfileUnavailable.WithPublicContext("device", d.Name).
				WithPublicContext("profile", string(profile)).
				Err()
		}
	}
	return nil
}

// profileRunners keeps the members that can run every profile of the set (an agent that has not reported is kept: it
// answers for itself). When none is left the error names the first profile no member can run.
func profileRunners(members []*Member, profiles exerciseModel.NodeProfileSet) ([]*Member, error) {
	var kept []*Member
	for _, m := range members {
		if canRunAll(m, profiles) {
			kept = append(kept, m)
		}
	}
	if len(kept) > 0 || profiles == 0 {
		return kept, nil
	}
	for _, profile := range profiles.List() {
		if profile == exerciseModel.NodeProfileVM {
			return nil, exerciseModel.ErrNodeProfileVMUnsupported.WithPublicContext("profile", string(profile)).
				Err()
		}
		if !slices.ContainsFunc(
			members,
			func(m *Member) bool { return canRunAll(m, exerciseModel.NodeProfileSet(0).Add(profile)) },
		) {
			return nil, exerciseModel.ErrNodeProfileUnavailable.WithPublicContext("profile", string(profile)).
				Err()
		}
	}
	// Every profile runs somewhere, but no single agent runs them all.
	first := profiles.List()[0]
	return nil, exerciseModel.ErrNodeProfileUnavailable.WithPublicContext("profile", string(first)).
		Err()
}

func canRunAll(m *Member, profiles exerciseModel.NodeProfileSet) bool {
	for _, profile := range profiles.List() {
		switch profile { //nolint:exhaustive // the other profiles take the default
		case exerciseModel.NodeProfileContainer:
		case exerciseModel.NodeProfileVM:
			return false
		default:
			if capacity, known := m.Profiles.Get(profile); known && !capacity.Runnable() {
				return false
			}
		}
	}
	return true
}

type storedProfile struct {
	Profile               string `json:"profile"`
	AvailableNow          bool   `json:"available_now"`
	ScaleUpKnown          bool   `json:"scale_up_known"`
	AvailableAfterScaleUp bool   `json:"available_after_scale_up"`
	OverheadCPUMillicores int64  `json:"overhead_cpu_millicores"`
	OverheadMemoryBytes   int64  `json:"overhead_memory_bytes"`
}

// EncodeProfiles is the stored form of a report.
func EncodeProfiles(v map[exerciseModel.NodeProfile]ProfileCapacity) []byte {
	items := make([]storedProfile, 0, len(v))
	for _, profile := range exerciseModel.NodeProfiles {
		if c, ok := v[profile]; ok {
			items = append(items, storedProfile{
				Profile: string(
					profile,
				),
				AvailableNow:          c.AvailableNow,
				ScaleUpKnown:          c.ScaleUpKnown,
				AvailableAfterScaleUp: c.AvailableAfterScaleUp,
				OverheadCPUMillicores: c.Overhead.CPUMillicores,
				OverheadMemoryBytes:   c.Overhead.MemoryBytes,
			})
		}
	}
	data, _ := json.Marshal(items)
	return data
}

// DecodeProfiles reads a stored report; an unreadable one is an error and a profile this build does not know is dropped.
func DecodeProfiles(data []byte) (map[exerciseModel.NodeProfile]ProfileCapacity, error) {
	var items []storedProfile
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	out := make(map[exerciseModel.NodeProfile]ProfileCapacity, len(items))
	for _, item := range items {
		profile := exerciseModel.NodeProfile(item.Profile)
		if !profile.Valid() {
			continue
		}
		out[profile] = ProfileCapacity{
			AvailableNow:          item.AvailableNow,
			ScaleUpKnown:          item.ScaleUpKnown,
			AvailableAfterScaleUp: item.AvailableAfterScaleUp,
			Overhead: resourcesModel.Amount{
				CPUMillicores: item.OverheadCPUMillicores,
				MemoryBytes:   item.OverheadMemoryBytes,
			},
		}
	}
	return out, nil
}

// ProfileCells holds the profile cell of every agent by id, so a member that is rebuilt keeps its report, and
// applies the reports read from the database (this replica may not be the one that monitors). onChange runs after a
// report is taken, for what depends on it (the overhead of the plans).
type ProfileCells struct {
	mu       sync.Mutex
	cells    map[uuid.UUID]*ProfileCell
	onChange func(agent uuid.UUID, v map[exerciseModel.NodeProfile]ProfileCapacity)
}

func NewProfileCells(
	onChange func(agent uuid.UUID, v map[exerciseModel.NodeProfile]ProfileCapacity),
) *ProfileCells {
	return &ProfileCells{cells: map[uuid.UUID]*ProfileCell{}, onChange: onChange}
}

// Cell is the cell of the agent; a nil registry gives a fresh one.
func (r *ProfileCells) Cell(agent uuid.UUID) *ProfileCell {
	if r == nil {
		return NewProfileCell()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cells[agent] == nil {
		r.cells[agent] = NewProfileCell()
	}
	return r.cells[agent]
}

// Update takes a report received at the time unless the agent's cell has a newer one.
func (r *ProfileCells) Update(
	agent uuid.UUID,
	v map[exerciseModel.NodeProfile]ProfileCapacity,
	at time.Time,
) {
	if r == nil || !r.Cell(agent).SetAt(v, at) {
		return
	}
	if r.onChange != nil {
		r.onChange(agent, v)
	}
}

// ApplyStored takes the stored reports (a malformed one is skipped).
func (r *ProfileCells) ApplyStored(reports []infraModel.AgentProfileReport) {
	for _, report := range reports {
		if v, err := DecodeProfiles(report.Data); err == nil {
			r.Update(report.AgentID, v, report.ReceivedAt)
		}
	}
}
