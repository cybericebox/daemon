package labagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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
