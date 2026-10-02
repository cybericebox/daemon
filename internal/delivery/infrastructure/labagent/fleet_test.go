package labagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

type memoryPlacements struct {
	mu       sync.Mutex
	groups   map[string]uuid.UUID
	released []string
}

func (m *memoryPlacements) Get(_ context.Context, group string) (uuid.UUID, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.groups[group]
	return id, ok, nil
}

func (m *memoryPlacements) Claim(_ context.Context, group string, agent uuid.UUID) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groups == nil {
		m.groups = map[string]uuid.UUID{}
	}
	if existing, ok := m.groups[group]; ok {
		return existing, nil
	}
	m.groups[group] = agent
	return agent, nil
}

func (m *memoryPlacements) Release(_ context.Context, group string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.groups, group)
	m.released = append(m.released, group)
	return nil
}

// pingAgent is a fakeAgent that also answers Ping.
type pingAgent struct {
	*fakeAgent
	pingErr error
}

func (p pingAgent) Ping(context.Context, *labpb.Empty, ...grpc.CallOption) (*labpb.Empty, error) {
	return &labpb.Empty{}, p.pingErr
}

type fleetFixture struct {
	fleet  *Fleet
	store  *memoryPlacements
	a, b   *fakeAgent
	am, bm *Member
}

func newFleetFixture(t *testing.T) *fleetFixture {
	t.Helper()
	f := &fleetFixture{store: &memoryPlacements{}, a: &fakeAgent{strict: true}, b: &fakeAgent{strict: true}}
	f.am = &Member{ID: uuid.Must(uuid.NewV7()), Name: "a", Priority: 10, Enabled: true, Client: newClient(f.a)}
	f.bm = &Member{ID: uuid.Must(uuid.NewV7()), Name: "b", Priority: 20, Enabled: true, Client: newClient(f.b)}
	f.am.Client.Client = pingClient{pingAgent{fakeAgent: f.a}}
	f.bm.Client.Client = pingClient{pingAgent{fakeAgent: f.b}}
	f.fleet = NewFleet(f.store, nil, f.bm, f.am) // given out of order on purpose
	return f
}

// pingClient adapts pingAgent to the labclient.Client shape.
type pingClient struct{ pingAgent }

func (pingClient) Close() error { return nil }

func TestFleetPlacesANewGroupOnTheFirstAgentByPriorityAndKeepsItThere(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	if err := f.fleet.EnsureVPNGroup(ctx, "e-1-t-1"); err != nil {
		t.Fatal(err)
	}
	if f.a.createGroup == nil || f.b.createGroup != nil {
		t.Fatalf("the group must go to the highest priority agent: a=%v b=%v", f.a.createGroup, f.b.createGroup)
	}
	if got := f.store.groups["e-1-t-1"]; got != f.am.ID {
		t.Fatalf("placement = %v, want agent a", got)
	}
	// Even when agent a gets disabled for new groups, the existing group stays on it.
	f.am.Enabled = false
	f.a.createGroup, f.a.deleted = nil, nil
	if err := f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, exerciseModel.Topology{}); err != nil {
		t.Fatal(err)
	}
	if f.a.createLabs == nil || f.b.createLabs != nil {
		t.Fatal("the group's lab must be created on the agent that holds the group")
	}
	// A new group avoids the disabled agent.
	if err := f.fleet.EnsureVPNGroup(ctx, "e-1-t-2"); err != nil {
		t.Fatal(err)
	}
	if f.store.groups["e-1-t-2"] != f.bm.ID || f.b.createGroup == nil {
		t.Fatalf("a new group must go to the enabled agent: %v", f.store.groups)
	}
}

func TestFleetSkipsAnAgentThatFailsItsHealthCheck(t *testing.T) {
	f := newFleetFixture(t)
	f.am.Client.Client = pingClient{pingAgent{fakeAgent: f.a, pingErr: errors.New("down")}}
	if err := f.fleet.EnsureVPNGroup(context.Background(), "e-1-t-1"); err != nil {
		t.Fatal(err)
	}
	if f.store.groups["e-1-t-1"] != f.bm.ID {
		t.Fatalf("placement = %v, want the healthy agent b", f.store.groups["e-1-t-1"])
	}
	f.bm.Client.Client = pingClient{pingAgent{fakeAgent: f.b, pingErr: errors.New("down")}}
	if err := f.fleet.EnsureVPNGroup(context.Background(), "e-1-t-9"); err == nil {
		t.Fatal("no healthy agent must be an error")
	}
	if err := f.fleet.Health(context.Background()); err == nil {
		t.Fatal("health is an error when every agent is down")
	}
}

func TestFleetRoutesReadsAndTeardownByPlacement(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	f.store.groups = map[string]uuid.UUID{"e-1-t-1": f.bm.ID}
	f.b.groups = []*labpb.LabGroup{{Name: "e-1-t-1", Status: &labpb.LabGroupStatus{Namespace: "ns"}}}
	f.b.labs = []*labpb.Lab{{Name: "c-1", Status: &labpb.LabStatus{Phase: "Ready", Ready: true}}}
	got, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
	if err != nil || !got.Ready {
		t.Fatalf("status = %+v, err = %v", got, err)
	}
	if err = f.fleet.DestroyLabGroup(ctx, "e-1-t-1"); err != nil {
		t.Fatal(err)
	}
	if f.b.deleted["group"] == nil || f.a.deleted != nil {
		t.Fatal("the group must be deleted on its own agent only")
	}
	if len(f.store.released) != 1 || f.store.released[0] != "e-1-t-1" {
		t.Fatalf("released = %v", f.store.released)
	}
	// A group that never existed has nothing to tear down, suspend or resume-less read.
	if err = f.fleet.DestroyLabGroup(ctx, "e-9-t-9"); err != nil {
		t.Fatalf("destroy of an unknown group: %v", err)
	}
	if err = f.fleet.DeleteLab(ctx, "e-9-t-9", "c-1"); err != nil {
		t.Fatalf("delete of a lab of an unknown group: %v", err)
	}
	if err = f.fleet.SetLabGroupSuspended(ctx, "e-9-t-9", true); err != nil {
		t.Fatalf("suspending an unknown group: %v", err)
	}
	if _, err = f.fleet.LabStatus(ctx, "e-9-t-9", "c-1"); err == nil {
		t.Fatal("the status of an unknown group is an error")
	}
	if err = f.fleet.ResetDevice(ctx, "e-9-t-9", "c-1", "web"); !errors.Is(err, infraModel.ErrDeviceNotFound.Err()) {
		t.Fatalf("reset in an unknown group = %v", err)
	}
}

func TestFleetOfOneAgentNeedsNoPlacementStore(t *testing.T) {
	a := &fakeAgent{groups: readyGroup()}
	m := &Member{ID: uuid.Must(uuid.NewV7()), Name: "env", Enabled: true, Client: newClient(a)}
	fleet := NewFleet(nil, nil, m)
	if err := fleet.DeployLab(context.Background(), "tu-1", "l-1", infraModel.LabMeta{}, exerciseModel.Topology{}); err != nil {
		t.Fatal(err)
	}
	if a.createLabs == nil {
		t.Fatal("the only agent serves every group")
	}
	if err := fleet.DestroyLabGroup(context.Background(), "tu-1"); err != nil {
		t.Fatal(err)
	}
	if err := NewFleet(nil, nil).EnsureVPNGroup(context.Background(), "g"); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("an empty fleet is unavailable: %v", err)
	}
}

func TestFleetPrewarmMergesTheLeastAdvancedStatePerImage(t *testing.T) {
	f := newFleetFixture(t)
	f.a.prewarmOut = &labpb.PrewarmImagesResult{Images: []*labpb.PrewarmImageStatus{
		{Image: "x", State: labpb.PrewarmState_PREWARM_STATE_DONE}, {Image: "y", State: labpb.PrewarmState_PREWARM_STATE_DONE},
	}}
	f.b.prewarmOut = &labpb.PrewarmImagesResult{Images: []*labpb.PrewarmImageStatus{
		{Image: "x", State: labpb.PrewarmState_PREWARM_STATE_WARMING}, {Image: "y", State: labpb.PrewarmState_PREWARM_STATE_DONE},
	}}
	got, err := f.fleet.PrewarmImages(context.Background(), []string{"x", "y"})
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
	byImage := map[string]string{got[0].Image: got[0].State, got[1].Image: got[1].State}
	if byImage["x"] != infraModel.PrewarmWarming || byImage["y"] != infraModel.PrewarmDone {
		t.Fatalf("merged = %v", byImage)
	}
	f.a.callErr = errors.New("cache off")
	if got, err = f.fleet.PrewarmImages(context.Background(), []string{"x"}); err != nil || len(got) == 0 {
		t.Fatalf("one failing agent must not hide the other: %v %v", got, err)
	}
}

func TestFleetFindsAGroupThatPredatesThePlacements(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	// Agent a knows nothing of the group, agent b holds it (created while b was the only agent).
	f.b.groups = []*labpb.LabGroup{{Name: "e-1-t-1", Status: &labpb.LabGroupStatus{Namespace: "ns"}}}
	f.b.labs = []*labpb.Lab{{Name: "c-1", Status: &labpb.LabStatus{Phase: "Ready", Ready: true}}}
	got, err := f.fleet.LabStatus(ctx, "e-1-t-1", "c-1")
	if err != nil || !got.Ready {
		t.Fatalf("status = %+v, err = %v", got, err)
	}
	if f.store.groups["e-1-t-1"] != f.bm.ID {
		t.Fatalf("the located group must be recorded: %v", f.store.groups)
	}
	// An error of an agent while locating is not a "not found".
	f2 := newFleetFixture(t)
	if _, err = f2.fleet.LabStatus(ctx, "e-9-t-9", "c-1"); err == nil || errors.Is(err, errNoPlacement) == false {
		t.Fatalf("a group no agent knows is unplaced: %v", err)
	}
}

func TestFleetPlacesNewGroupsWhileAnAgentIsDownButNeverGuessesOnReads(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	f.a.listErr = errors.New("agent a unreachable")
	f.am.Client.Client = pingClient{pingAgent{fakeAgent: f.a, pingErr: errors.New("agent a unreachable")}}
	if err := f.fleet.EnsureVPNGroup(ctx, "e-2-t-2"); err != nil {
		t.Fatalf("a new group goes to a reachable agent: %v", err)
	}
	if f.store.groups["e-2-t-2"] != f.bm.ID {
		t.Fatalf("placement = %v", f.store.groups)
	}
	if _, err := f.fleet.LabStatus(ctx, "e-3-t-3", "c-1"); err == nil || errors.Is(err, errNoPlacement) {
		t.Fatalf("a read must not conclude the group is absent while an agent cannot be asked: %v", err)
	}
}

func TestSessionIssuerSignsWithTheKeyOfTheAgentThatHoldsTheGroup(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	_, keyA, _ := ed25519.GenerateKey(rand.Reader)
	_, keyB, _ := ed25519.GenerateKey(rand.Reader)
	f.am.Tenant, f.am.AccessKeyID, f.am.AccessKey = "tenant-a", "ka", keyA
	f.bm.Tenant, f.bm.AccessKeyID, f.bm.AccessKey = "tenant-b", "kb", keyB
	f.fleet.Replace([]*Member{f.am, f.bm})
	f.store.groups = map[string]uuid.UUID{"e-1-t-1": f.bm.ID, "e-1-t-2": f.am.ID}
	issuer, err := labaccess.New(labaccess.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sessions := SessionIssuer{Fleet: f.fleet, Issuer: issuer}
	now := time.Now()
	for group, want := range map[string]struct {
		tenant, kid string
		key         ed25519.PrivateKey
	}{"e-1-t-1": {"tenant-b", "kb", keyB}, "e-1-t-2": {"tenant-a", "ka", keyA}} {
		link, issueErr := sessions.Issue(ctx, labaccess.Session{Group: group, Client: "p-1", AccessURL: "https://web-abc.labs.example.com/"}, now)
		if issueErr != nil {
			t.Fatalf("%s: %v", group, issueErr)
		}
		var claims jwt.RegisteredClaims
		token, parseErr := jwt.ParseWithClaims(link.Token, &claims, func(*jwt.Token) (any, error) { return want.key.Public(), nil }, jwt.WithIssuer(want.tenant), jwt.WithAudience(labaccess.Audience))
		if parseErr != nil || token.Header["kid"] != want.kid {
			t.Fatalf("%s: token does not verify with the holder's key: %v kid=%v", group, parseErr, token.Header["kid"])
		}
	}
	// A member without a key gives no link; an unknown group is an error.
	f.bm.AccessKey = nil
	if _, err = sessions.Issue(ctx, labaccess.Session{Group: "e-1-t-1", Client: "p-1", AccessURL: "https://web-abc.labs.example.com/"}, now); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("no key = %v", err)
	}
	if _, err = sessions.Issue(ctx, labaccess.Session{Group: "e-9-t-9", Client: "p-1", AccessURL: "https://web-abc.labs.example.com/"}, now); err == nil {
		t.Fatal("an unknown group has no agent")
	}
}

func persistentTopology() exerciseModel.Topology {
	return exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "db", Type: exerciseModel.DeviceTypeContainer, Image: "pg",
		Persistence: &exerciseModel.DevicePersistence{Enabled: true, Debounce: "5s"},
	}}}
}

func TestFleetRefusesPersistenceTheAgentDoesNotOffer(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	f.am.Features = NewFeatureCell(&infraModel.AgentFeatures{})
	err := f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, persistentTopology())
	if !errors.Is(err, infraModel.ErrDevicePersistenceUnavailable.Err()) {
		t.Fatalf("a topology that needs persistence on an agent without it = %v", err)
	}
	if f.a.createLabs != nil {
		t.Fatal("nothing is created for a refused topology")
	}
	// The same agent takes a topology without persistence, and one that asks once the agent offers it.
	if err = f.fleet.DeployLab(ctx, "e-1-t-1", "c-2", infraModel.LabMeta{}, exerciseModel.Topology{}); err != nil {
		t.Fatal(err)
	}
	f.am.Features.Set(infraModel.AgentFeatures{Persistence: infraModel.PersistenceFeature{Available: true}})
	if err = f.fleet.DeployLab(ctx, "e-1-t-1", "c-3", infraModel.LabMeta{}, persistentTopology()); err != nil {
		t.Fatal(err)
	}
	// An agent that has not reported yet is not second-guessed: it refuses on its own if it must.
	other := newFleetFixture(t)
	if err = other.fleet.DeployLab(ctx, "e-2-t-1", "c-1", infraModel.LabMeta{}, persistentTopology()); err != nil {
		t.Fatalf("no report yet: %v", err)
	}
}

func TestFleetPersistenceAvailableFollowsTheEnabledAgentsReports(t *testing.T) {
	f := newFleetFixture(t)
	if f.fleet.PersistenceAvailable() {
		t.Fatal("agents that have not reported offer nothing")
	}
	offered := &infraModel.AgentFeatures{Persistence: infraModel.PersistenceFeature{Available: true}}
	f.bm.Features = NewFeatureCell(offered)
	if !f.fleet.PersistenceAvailable() {
		t.Fatal("one agent offering it is enough")
	}
	f.bm.Enabled = false
	if f.fleet.PersistenceAvailable() {
		t.Fatal("a disabled agent takes no new groups, so it does not count")
	}
}

func TestFeaturesOfConvertsTheAgentReport(t *testing.T) {
	got := FeaturesOf(&labpb.FeaturesResponse{
		StatePersistence: &labpb.StatePersistenceFeature{Available: true, DefaultDebounceMs: 5000, WriteQuotaBytes: 10, MaxFileSizeBytes: 20, ExcludedPaths: []string{"/proc"}},
		ImageCache:       &labpb.ImageCacheFeature{Enabled: true, Registries: []string{"docker.io"}},
		Scheduler:        &labpb.SchedulerFeature{Enabled: true, MaxPods: 4},
		Endpoints:        &labpb.EndpointsFeature{LabsDomain: "labs.example.test", VpnEndpoint: "vpn.example.test:51820"},
		Certificate:      &labpb.CertificateFeature{NotAfterUnix: 99, IssuedTtlSeconds: 100},
		Proxy:            &labpb.ProxyFeature{AccessTokenMaxTtlSeconds: 300, SessionMaxTtlSeconds: 86400},
		Limits: &labpb.LimitsFeature{
			Device: &labpb.DeviceLimits{MaxCpuMillicores: 500, MaxMemoryBytes: 512 << 20, DefaultCpuMillicores: 100, DefaultMemoryBytes: 256 << 20},
			Lab:    &labpb.LabLimits{MaxDevices: 10},
			Group:  &labpb.GroupLimits{MaxLabs: 5, MaxCpuMillicores: 2000, MaxMemoryBytes: 2 << 30},
			Tenant: &labpb.TenantLimits{MaxLabs: 7},
		},
	})
	want := infraModel.AgentFeatures{
		Persistence: infraModel.PersistenceFeature{Available: true, DefaultDebounce: 5000, WriteQuotaBytes: 10, MaxFileSizeBytes: 20, ExcludedPaths: []string{"/proc"}},
		ImageCache:  infraModel.ImageCacheFeature{Enabled: true, Registries: []string{"docker.io"}},
		Scheduler:   infraModel.SchedulerFeature{Enabled: true, MaxPods: 4},
		Endpoints:   infraModel.EndpointsFeature{LabsDomain: "labs.example.test", VPNEndpoint: "vpn.example.test:51820"},
		Certificate: infraModel.CertificateFeature{NotAfterUnix: 99, IssuedTTLSeconds: 100},
		Proxy:       infraModel.ProxyFeature{AccessTokenMaxTTLSeconds: 300, SessionMaxTTLSeconds: 86400},
		Limits: infraModel.LimitsFeature{
			DeviceMaxCPUMillicores: 500, DeviceMaxMemoryBytes: 512 << 20, DeviceDefaultCPUMillicores: 100, DeviceDefaultMemoryBytes: 256 << 20,
			LabMaxDevices: 10, GroupMaxLabs: 5, GroupMaxCPUMillicores: 2000, GroupMaxMemoryBytes: 2 << 30, TenantMaxLabs: 7,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if FeaturesOf(nil).Persistence.Available {
		t.Fatal("an empty report offers nothing")
	}
}

func TestPrewarmSkipsAnAgentWhoseImageCacheIsOff(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	f.a.prewarmOut = &labpb.PrewarmImagesResult{Images: []*labpb.PrewarmImageStatus{{Image: "nginx", State: labpb.PrewarmState_PREWARM_STATE_DONE}}}
	f.b.prewarmOut = f.a.prewarmOut
	f.am.Features = NewFeatureCell(&infraModel.AgentFeatures{})
	f.bm.Features = NewFeatureCell(&infraModel.AgentFeatures{})
	got, err := f.fleet.PrewarmImages(ctx, []string{"nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if f.a.prewarm != nil || f.b.prewarm != nil {
		t.Fatal("an agent without an image cache is not asked to warm anything")
	}
	if len(got) != 1 || got[0].Image != "nginx" || got[0].State != infraModel.PrewarmSkipped {
		t.Fatalf("every image reads as skipped: %+v", got)
	}
	// One agent with a cache is asked, and its answer stands; the other is left out.
	f.bm.Features.Set(infraModel.AgentFeatures{ImageCache: infraModel.ImageCacheFeature{Enabled: true}})
	got, err = f.fleet.PrewarmImages(ctx, []string{"nginx"})
	if err != nil || f.a.prewarm != nil || f.b.prewarm == nil || len(got) != 1 || got[0].State != infraModel.PrewarmDone {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func heavyTopology(cpu string) exerciseModel.Topology {
	return exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "img", Resources: &exerciseModel.DeviceResources{CPULimit: cpu},
	}}}
}

func limited(maxCPU int64) *FeatureCell {
	return NewFeatureCell(&infraModel.AgentFeatures{Limits: infraModel.LimitsFeature{DeviceMaxCPUMillicores: maxCPU}})
}

func TestPlacementTakesOnlyAgentsWhoseLimitsHoldTheWholeNeed(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	f.am.Features, f.bm.Features = limited(500), limited(4000) // a is first by priority but too small
	need := infraModel.PlacementNeed{Labs: []infraModel.Demand{infraModel.DemandOf(heavyTopology("2"))}}
	if err := f.fleet.EnsureVPNGroup(infraModel.WithPlacementNeed(ctx, need), "e-1-t-1"); err != nil {
		t.Fatal(err)
	}
	if f.store.groups["e-1-t-1"] != f.bm.ID {
		t.Fatalf("the group must go to the agent that can run the task: %v", f.store.groups)
	}
	// Nobody fits: a clear error, nothing is created or placed.
	f.bm.Features = limited(1000)
	err := f.fleet.EnsureVPNGroup(infraModel.WithPlacementNeed(ctx, need), "e-1-t-2")
	if !errors.Is(err, infraModel.ErrNoAgentFitsTask.Err()) {
		t.Fatalf("no agent fits = %v", err)
	}
	if _, placed := f.store.groups["e-1-t-2"]; placed {
		t.Fatal("a refused group is not placed")
	}
	// An agent that has not reported is never filtered out.
	f.am.Features = nil
	if err = f.fleet.EnsureVPNGroup(infraModel.WithPlacementNeed(ctx, need), "e-1-t-3"); err != nil || f.store.groups["e-1-t-3"] != f.am.ID {
		t.Fatalf("unreported agent: %v %v", err, f.store.groups)
	}
}

func TestDeployLabChecksItsOwnTopologyAgainstThePlacedAgent(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	f.am.Features, f.bm.Features = limited(500), limited(4000)
	// A new group is placed by the lab it is created for.
	if err := f.fleet.DeployLab(ctx, "e-1-t-1", "c-1", infraModel.LabMeta{}, heavyTopology("2")); err != nil {
		t.Fatal(err)
	}
	if f.store.groups["e-1-t-1"] != f.bm.ID {
		t.Fatalf("placed on %v", f.store.groups["e-1-t-1"])
	}
	// A group stays where it is: a later lab that does not fit there is refused, not moved.
	f.bm.Features = limited(1000)
	if err := f.fleet.DeployLab(ctx, "e-1-t-1", "c-2", infraModel.LabMeta{}, heavyTopology("2")); !errors.Is(err, infraModel.ErrNoAgentFitsTask.Err()) {
		t.Fatalf("a lab over the placed agent's limit = %v", err)
	}
}

func TestTopologyFitListsTheAgentsThatCannotAndTheWidestLimits(t *testing.T) {
	f := newFleetFixture(t)
	f.am.Features, f.bm.Features = limited(500), limited(4000)
	fit := f.fleet.TopologyFit(heavyTopology("2"))
	if !fit.FitsAny || len(fit.Warnings) != 1 || fit.Warnings[0].Agent != "a" || fit.Warnings[0].Resource != infraModel.FitCPU || fit.Warnings[0].Max != 500 {
		t.Fatalf("fit = %+v", fit)
	}
	f.bm.Features = limited(1000)
	if fit = f.fleet.TopologyFit(heavyTopology("2")); fit.FitsAny || len(fit.Warnings) != 2 {
		t.Fatalf("none can: %+v", fit)
	}
	f.bm.Features = nil
	if fit = f.fleet.TopologyFit(heavyTopology("2")); !fit.FitsAny {
		t.Fatal("an agent that has not reported counts as able")
	}
	f.am.Features, f.bm.Features = limited(500), limited(4000)
	limits, known := f.fleet.DeviceLimits()
	if !known || limits.DeviceMaxCPUMillicores != 4000 {
		t.Fatalf("the editor shows the widest cap: %+v %v", limits, known)
	}
	f.bm.Features = limited(0) // no limit is the widest
	if limits, _ = f.fleet.DeviceLimits(); limits.DeviceMaxCPUMillicores != 0 {
		t.Fatalf("an agent without a cap lifts it: %+v", limits)
	}
	f.am.Features, f.bm.Features = nil, nil
	if _, known = f.fleet.DeviceLimits(); known {
		t.Fatal("nothing reported yet")
	}
}

func TestPlacementAddsTheLabsOfTheGroupUp(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	capped := func(groupCPU int64) *FeatureCell {
		return NewFeatureCell(&infraModel.AgentFeatures{Limits: infraModel.LimitsFeature{GroupMaxCPUMillicores: groupCPU}})
	}
	f.am.Features, f.bm.Features = capped(1000), capped(4000)
	lab := infraModel.DemandOf(heavyTopology("800m"))
	need := infraModel.PlacementNeed{Labs: []infraModel.Demand{lab, lab, lab}} // 2400m together
	if err := f.fleet.EnsureVPNGroup(infraModel.WithPlacementNeed(ctx, need), "e-1-t-1"); err != nil {
		t.Fatal(err)
	}
	if f.store.groups["e-1-t-1"] != f.bm.ID {
		t.Fatalf("each lab fits a (800m) but the group of three does not: %v", f.store.groups)
	}
}
