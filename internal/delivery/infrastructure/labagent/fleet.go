package labagent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// Member is one agent of the fleet: its registry identity, whether new groups may be placed on it,
// and its client. Priority orders the agents (smaller first).
type Member struct {
	ID       uuid.UUID
	Name     string
	Priority int
	// Enabled agents receive new groups; a disabled one only serves the groups it already holds,
	// so they can be read and torn down.
	Enabled bool
	Client  *Client
	// Tenant is the agent's tenant name (its certificate CN) and the issuer of the lab access tokens;
	// AccessKeyID and AccessKey are the key that signs them. Empty for a member that cannot sign
	// (no key configured): its labs then get no web links.
	Tenant      string
	AccessKeyID string
	AccessKey   ed25519.PrivateKey
	// Features is what the agent last reported it offers the platform; shared by the copies of the member.
	Features *FeatureCell
}

// PlacementStore remembers which agent holds a lab group. A group lives on exactly one agent for
// its whole life: the whole team's group, labs and VPN clients come from the same cluster.
type PlacementStore interface {
	// Get returns the agent of the group; found is false for a group that is not placed yet.
	Get(ctx context.Context, group string) (agent uuid.UUID, found bool, err error)
	// Claim places the group on agent unless it is already placed, and returns the agent that holds
	// it afterwards (the winner when two callers race).
	Claim(ctx context.Context, group string, agent uuid.UUID) (uuid.UUID, error)
	// Release forgets the group after it was torn down.
	Release(ctx context.Context, group string) error
}

// Picker chooses the agent for a group that is not placed yet, among the enabled members ordered by
// priority. It must not return a member it did not get.
type Picker func(ctx context.Context, group string, members []*Member) (*Member, error)

// FirstByPriority places on the first agent of the priority order that answers its health check.
func FirstByPriority(ctx context.Context, _ string, members []*Member) (*Member, error) {
	var lastErr error
	for _, m := range members {
		if lastErr = m.Client.Health(ctx); lastErr == nil {
			return m, nil
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("no healthy infrastructure agent: %w", lastErr)
	}
	return nil, infraModel.ErrInfrastructureUnavailable.Err()
}

// errNoPlacement marks a group the fleet does not know: it was never created.
var errNoPlacement = errors.New("lab group has no agent")

// Fleet is the infrastructure port over several agents: it finds the agent of a lab group and
// forwards the call to it, so the use cases address groups only and never agents. With one
// member it needs no placement store.
type Fleet struct {
	store  PlacementStore
	picker Picker

	mu sync.RWMutex
	// prewarmSkipLogged remembers the agents whose cache-off skip was logged already.
	prewarmSkipLogged sync.Map
	// limitWarned remembers the agents whose proxy limits already caused a warning about the configured TTLs.
	limitWarned sync.Map
	members     []*Member // ordered by priority, then name
	// policy is the platform's device resources settings (the frame an agent must meet, the presets).
	policy resourcesModel.Policy
}

// NewFleet builds a fleet. store may be nil only while the fleet has at most one member.
func NewFleet(store PlacementStore, picker Picker, members ...*Member) *Fleet {
	if picker == nil {
		picker = FirstByPriority
	}
	f := &Fleet{store: store, picker: picker, policy: resourcesModel.DefaultPolicy()}
	f.Replace(members)
	return f
}

// Replace swaps the members (a changed agent list). Clients of members that are gone are the
// caller's to close.
func (f *Fleet) Replace(members []*Member) {
	sorted := append([]*Member(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].Name < sorted[j].Name
	})
	f.mu.Lock()
	f.members = sorted
	f.mu.Unlock()
}

// Members returns the current members in priority order.
func (f *Fleet) Members() []*Member {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]*Member(nil), f.members...)
}

func (f *Fleet) member(id uuid.UUID) *Member {
	for _, m := range f.Members() {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// route finds the agent of an existing group. A fleet of one agent needs no lookup.
func (f *Fleet) route(ctx context.Context, group string) (*Client, error) {
	m, err := f.routeWith(ctx, group, false)
	if err != nil {
		return nil, err
	}
	return m.Client, nil
}

// memberOf finds the member that holds an existing group.
func (f *Fleet) memberOf(ctx context.Context, group string) (*Member, error) {
	return f.routeWith(ctx, group, false)
}

// routeWith is route; forCreate lets the search for an unrecorded group ignore an agent that does
// not answer (a new group may be placed while an agent is down), while a read must not conclude that
// a group is absent because an agent could not be asked.
func (f *Fleet) routeWith(ctx context.Context, group string, forCreate bool) (*Member, error) {
	members := f.Members()
	switch {
	case len(members) == 0:
		return nil, infraModel.ErrInfrastructureUnavailable.Err()
	case len(members) == 1:
		return members[0], nil
	case f.store == nil:
		return nil, errors.New("lab group placement store is not configured")
	}
	id, found, err := f.store.Get(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("lab group placement: %w", err)
	}
	if !found {
		return f.locate(ctx, group, forCreate)
	}
	m := f.member(id)
	if m == nil {
		return nil, fmt.Errorf("lab group %q is placed on agent %s that is not in the fleet", group, id)
	}
	return m, nil
}

// locate finds a group that has no placement record by asking the agents: a group created while the
// fleet had one agent was never recorded. The agent that knows it is recorded now. A group no agent
// knows was never created.
func (f *Fleet) locate(ctx context.Context, group string, forCreate bool) (*Member, error) {
	var unreachable error
	for _, m := range f.Members() {
		if _, err := m.Client.getGroup(ctx, group); err != nil {
			if !errors.Is(err, errNotFound) && unreachable == nil {
				unreachable = err
			}
			continue
		}
		winner, err := f.store.Claim(ctx, group, m.ID)
		if err != nil {
			return nil, fmt.Errorf("place lab group: %w", err)
		}
		if winner == m.ID {
			return m, nil
		}
		if held := f.member(winner); held != nil {
			return held, nil
		}
		return nil, fmt.Errorf("lab group %q is placed on agent %s that is not in the fleet", group, winner)
	}
	if unreachable != nil && !forCreate {
		return nil, unreachable
	}
	return nil, fmt.Errorf("lab group %q: %w", group, errNoPlacement)
}

// routeForCreate is route for a call that creates the group: a group that is not placed yet is
// placed now by the picker.
func (f *Fleet) routeForCreate(ctx context.Context, group string) (*Client, error) {
	m, err := f.memberForCreate(ctx, group)
	if err != nil {
		return nil, err
	}
	return m.Client, nil
}

// memberForCreate finds the agent of a group, placing the group when it has none yet.
func (f *Fleet) memberForCreate(ctx context.Context, group string) (*Member, error) {
	m, err := f.routeWith(ctx, group, true)
	if err == nil {
		return m, nil
	}
	if !errors.Is(err, errNoPlacement) {
		return nil, err
	}
	// Only an agent that meets the platform requirements is used, and among them only one whose device
	// maxima hold what the group will run: a team lives on one agent.
	enabled := f.eligible()
	if len(enabled) == 0 {
		return nil, infraModel.ErrInfrastructureUnavailable.Err()
	}
	if need := infraModel.PlacementNeedFrom(ctx); need.Known() {
		var fitting []*Member
		var worst *infraModel.FitViolation
		for _, m := range enabled {
			v := f.fitOf(m, need)
			if v == nil {
				fitting = append(fitting, m)
			} else if worst == nil || v.Max > worst.Max {
				worst = v
			}
		}
		if len(fitting) == 0 {
			return nil, noAgentFits(worst)
		}
		enabled = fitting
	}
	picked, err := f.picker(ctx, group, enabled)
	if err != nil {
		return nil, err
	}
	winner, err := f.store.Claim(ctx, group, picked.ID)
	if err != nil {
		return nil, fmt.Errorf("place lab group: %w", err)
	}
	if winner == picked.ID {
		return picked, nil
	}
	held := f.member(winner)
	if held == nil {
		return nil, fmt.Errorf("lab group %q is placed on agent %s that is not in the fleet", group, winner)
	}
	return held, nil
}

// Available is true while the fleet has at least one agent; with none, infrastructure is not
// configured.
func (f *Fleet) Available() bool { return len(f.Members()) > 0 }

// Health is nil while at least one agent answers.
func (f *Fleet) Health(ctx context.Context) error {
	members := f.Members()
	if len(members) == 0 {
		return infraModel.ErrInfrastructureUnavailable.Err()
	}
	var last error
	for _, m := range members {
		if last = m.Client.Health(ctx); last == nil {
			return nil
		}
	}
	return last
}

func (f *Fleet) DeployLab(ctx context.Context, group, lab string, meta infraModel.LabMeta, topo exerciseModel.Topology) error {
	// The need is read from the presets; then the agent gets explicit resources: requests equal limits, the
	// size of the preset.
	policy := f.Policy()
	need := labNeed(policy, topo)
	topo = policy.Explicitly(topo)
	ctx = infraModel.WithPlacementNeed(ctx, withLab(infraModel.PlacementNeedFrom(ctx), need))
	m, err := f.memberForCreate(ctx, group)
	if err != nil {
		return err
	}
	// A group that lives on an agent stays there: a lab that passes its maxima is refused here, with the
	// same error the placement gives.
	if v := f.fitOf(m, need); v != nil {
		return noAgentFits(v)
	}
	// The agent says what it offers: a topology that needs more is refused here, before anything is created.
	if feat := m.Features.Get(); feat != nil && !feat.Persistence.Available && wantsPersistence(topo) {
		return infraModel.ErrDevicePersistenceUnavailable.Err()
	}
	return m.Client.DeployLab(f.withSizes(ctx, m), group, lab, meta, topo)
}

func (f *Fleet) EnsureVPNGroup(ctx context.Context, group string) error {
	m, err := f.memberForCreate(ctx, group)
	if err != nil {
		return err
	}
	return m.Client.EnsureVPNGroup(f.withSizes(ctx, m), group)
}

func (f *Fleet) LabStatus(ctx context.Context, group, lab string) (exerciseModel.LabDeployStatus, error) {
	c, err := f.route(ctx, group)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, err
	}
	return c.LabStatus(ctx, group, lab)
}

func (f *Fleet) EnsureLabClient(ctx context.Context, group, client string) (string, error) {
	c, err := f.route(ctx, group)
	if err != nil {
		return "", err
	}
	return c.EnsureLabClient(ctx, group, client)
}

// DestroyLabGroup tears the group down on its agent and forgets its placement. A group that was
// never placed is already gone.
func (f *Fleet) DestroyLabGroup(ctx context.Context, group string) error {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = c.DestroyLabGroup(ctx, group); err != nil {
		return err
	}
	if f.store != nil && len(f.Members()) > 1 {
		return f.store.Release(ctx, group)
	}
	return nil
}

func (f *Fleet) DeleteLab(ctx context.Context, group, lab string) error {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.DeleteLab(ctx, group, lab)
}

func (f *Fleet) DeleteLabClient(ctx context.Context, group, client string) error {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.DeleteLabClient(ctx, group, client)
}

func (f *Fleet) ReconcileLabGroupAccess(ctx context.Context, group string, policies []labAccessModel.ClientPolicy) error {
	c, err := f.route(ctx, group)
	if err != nil {
		return err
	}
	return c.ReconcileLabGroupAccess(ctx, group, policies)
}

func (f *Fleet) LabClientHandshake(ctx context.Context, group, client string) (time.Time, error) {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return c.LabClientHandshake(ctx, group, client)
}

func (f *Fleet) GetVPNClientSubnet(ctx context.Context, group string) (string, error) {
	c, err := f.route(ctx, group)
	if err != nil {
		return "", err
	}
	return c.GetVPNClientSubnet(ctx, group)
}

// SetLabGroupSuspended suspends or resumes a group; a group that was never placed has nothing to
// suspend (the same as a group the agent does not know).
func (f *Fleet) SetLabGroupSuspended(ctx context.Context, group string, suspended bool) error {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) && suspended {
		return nil
	}
	if err != nil {
		return err
	}
	return c.SetLabGroupSuspended(ctx, group, suspended)
}

func (f *Fleet) SetLabGroupVPNDisabled(ctx context.Context, group string, disabled bool) error {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) && disabled {
		return nil
	}
	if err != nil {
		return err
	}
	return c.SetLabGroupVPNDisabled(ctx, group, disabled)
}

func (f *Fleet) ResetDevice(ctx context.Context, group, lab, device string) error {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) {
		return infraModel.ErrDeviceNotFound.WithError(err).Err()
	}
	if err != nil {
		return err
	}
	return c.ResetDevice(ctx, group, lab, device)
}

func (f *Fleet) RescueDevice(ctx context.Context, group, lab, device string, enable bool) error {
	c, err := f.route(ctx, group)
	if errors.Is(err, errNoPlacement) {
		return infraModel.ErrDeviceNotFound.WithError(err).Err()
	}
	if err != nil {
		return err
	}
	return c.RescueDevice(ctx, group, lab, device, enable)
}

// PrewarmImages asks every enabled agent's image cache to fetch the images: whichever agent a team
// is placed on needs them. An image is reported with the least advanced state among the agents.
func (f *Fleet) PrewarmImages(ctx context.Context, images []string) ([]infraModel.ImagePrewarm, error) {
	var merged []infraModel.ImagePrewarm
	var firstErr error
	answered, skipped := 0, 0
	for _, m := range f.Members() {
		if !m.Enabled {
			continue
		}
		// The agent says its image cache is off: nodes pull directly, so there is nothing to warm.
		if feat := m.Features.Get(); feat != nil && !feat.ImageCache.Enabled {
			skipped++
			if _, logged := f.prewarmSkipLogged.LoadOrStore(m.ID, true); !logged {
				log.Info().Str("agent", m.Name).Msg("Image prewarm skipped: the agent reports its image cache is off")
			}
			continue
		}
		got, err := m.Client.PrewarmImages(ctx, images)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("agent %s: %w", m.Name, err)
			}
			continue
		}
		answered++
		merged = mergePrewarm(merged, got)
	}
	if answered == 0 && firstErr != nil {
		return nil, firstErr
	}
	if answered == 0 && skipped > 0 {
		for _, image := range images {
			merged = append(merged, infraModel.ImagePrewarm{Image: image, State: infraModel.PrewarmSkipped})
		}
	}
	return merged, nil
}

// prewarmRank orders the states from the least to the most advanced; failed counts as the worst.
var prewarmRank = map[string]int{
	infraModel.PrewarmFailed: 0, infraModel.PrewarmQueued: 1, infraModel.PrewarmWarming: 2,
	infraModel.PrewarmDone: 3, infraModel.PrewarmSkipped: 4,
}

func mergePrewarm(into, more []infraModel.ImagePrewarm) []infraModel.ImagePrewarm {
	if into == nil {
		return append([]infraModel.ImagePrewarm(nil), more...)
	}
	index := make(map[string]int, len(into))
	for i, image := range into {
		index[image.Image] = i
	}
	for _, image := range more {
		i, seen := index[image.Image]
		if !seen {
			into = append(into, image)
			continue
		}
		if prewarmRank[image.State] < prewarmRank[into[i].State] {
			into[i] = image
		}
	}
	return into
}
