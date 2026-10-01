package event

import (
	"context"
	"errors"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

type clientStore struct {
	configs map[uuid.UUID]string
	scope   vpnModel.Scope
	ref     uuid.NullUUID
}

func (s *clientStore) StoreConfig(_ context.Context, userID uuid.UUID, scope vpnModel.Scope, ref uuid.NullUUID, config string) error {
	s.configs[userID], s.scope, s.ref = config, scope, ref
	return nil
}
func (s *clientStore) GetConfig(_ context.Context, userID uuid.UUID, _ vpnModel.Scope, _ uuid.NullUUID) (string, error) {
	return s.configs[userID], nil
}

type clientInfra struct{ created []string }

func (*clientInfra) DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error {
	return nil
}
func (*clientInfra) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{Ready: true, Access: []exerciseModel.LabAccess{{Device: "web", Port: 80, Protocol: "http", URL: "https://web-abc123.challenges.example.com"}}}, nil
}
func (*clientInfra) EnsureVPNGroup(context.Context, string) error { return nil }
func (i *clientInfra) EnsureLabClient(_ context.Context, group, client string) (string, error) {
	i.created = append(i.created, group+"/"+client)
	return "private-config", nil
}
func (*clientInfra) DestroyLabGroup(context.Context, string) error { return nil }

// EnsureMemberLabClient is what formation calls for every member: the client
// appears once, in the team's group, and a second call creates nothing.
func TestEnsureMemberLabClientCreatesEachClientOnce(t *testing.T) {
	store, infra := &clientStore{configs: map[uuid.UUID]string{}}, &clientInfra{}
	uc := NewEventUseCase(Dependencies{VPN: store, Infra: infra})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for range 2 {
		if err := uc.EnsureMemberLabClient(context.Background(), eventID, teamID, userID); err != nil {
			t.Fatal(err)
		}
	}
	group, _ := labBindingModel.GroupName(eventID, teamID)
	want := group + "/" + labBindingModel.ParticipantClientName(userID)
	if len(infra.created) != 1 || infra.created[0] != want {
		t.Fatalf("created = %v, want [%s]", infra.created, want)
	}
	if store.configs[userID] != "private-config" || store.scope != vpnModel.ScopeEvent || store.ref.UUID != eventID {
		t.Fatalf("the config must be kept in the event scope: %+v", store)
	}
}

func TestEnsureMemberLabClientNeedsTheAgentAndTheStore(t *testing.T) {
	uc := NewEventUseCase(Dependencies{})
	if err := uc.EnsureMemberLabClient(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("without the agent there is no client")
	}
}

// A session never creates a client: a member without one is told so.
func TestRequireMemberLabClientDoesNotCreateAnything(t *testing.T) {
	store, infra := &clientStore{configs: map[uuid.UUID]string{}}, &clientInfra{}
	uc := NewEventUseCase(Dependencies{VPN: store, Infra: infra})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	err := uc.requireMemberLabClient(context.Background(), eventID, userID)
	if !errors.Is(err, eventStandModel.ErrStandLabClientMissing.Err()) {
		t.Fatalf("want ErrStandLabClientMissing, got %v", err)
	}
	if len(infra.created) != 0 {
		t.Fatalf("a session must not create clients: %v", infra.created)
	}
	store.configs[userID] = "stored"
	if err = uc.requireMemberLabClient(context.Background(), eventID, userID); err != nil {
		t.Fatalf("an existing client is fine: %v", err)
	}
}

type linkIssuer struct{ got []labaccess.Session }

func (i *linkIssuer) Issue(_ context.Context, s labaccess.Session, _ time.Time) (labaccess.Link, error) {
	i.got = append(i.got, s)
	return labaccess.Link{URL: s.AccessURL + "/_auth?t=x", Token: "x", ExpiresAt: s.ExpiresAt}, nil
}

// The link is built from the live web address of the requested device, for the
// team's group and the member's own client; an unknown device gets no link.
func TestIssueLabLinkUsesTheDeviceAddressGroupAndClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	issuer := &linkIssuer{}
	uc := NewEventUseCase(Dependencies{Repo: q, Infra: &clientInfra{}, LabSessions: issuer})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	binding := labBindingModel.Binding{LabGroupName: "e-x-t-y", LabName: "c-1"}

	link, err := uc.issueLabLink(context.Background(), eventID, userID, binding, "web", 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(issuer.got) != 1 || issuer.got[0].Group != "e-x-t-y" || issuer.got[0].Client != labBindingModel.ParticipantClientName(userID) || issuer.got[0].AccessURL != "https://web-abc123.challenges.example.com" || link.Token != "x" {
		t.Fatalf("session = %+v", issuer.got)
	}
	if _, err = uc.issueLabLink(context.Background(), eventID, userID, binding, "db", 5432); !errors.Is(err, eventStandModel.ErrStandLabNoWebDevice.Err()) {
		t.Fatalf("an unknown device must get no link: %v", err)
	}
	if len(issuer.got) != 1 {
		t.Fatalf("nothing may be signed for an unknown device: %+v", issuer.got)
	}
}
