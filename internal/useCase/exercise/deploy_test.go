package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/cybericebox/daemon/pkg/secret"
)

// fakeInfra captures what the deploy path hands the infrastructure agent.
type fakeInfra struct {
	deployedTopo    exerciseModel.Topology
	meta            infraModel.LabMeta
	deviceCalls     []string
	deviceErr       error
	deployErr       error
	status          exerciseModel.LabDeployStatus
	destroyed       string
	destroyCtxErr   error
	destroyErr      error
	client          string
	ensured         int
	policyGroup     string
	policies        []labAccessModel.ClientPolicy
	policyErr       error
	handshake       time.Time
	deletedLabs     []string
	deletedClients  []string
	deleteLabErr    error
	handshakeErr    error
	handshakeClient string
}

func (f *fakeInfra) ResetDevice(_ context.Context, group, lab, device string) error {
	f.deviceCalls = append(f.deviceCalls, "reset "+group+"/"+lab+"/"+device)
	return f.deviceErr
}
func (f *fakeInfra) RescueDevice(_ context.Context, group, lab, device string, enable bool) error {
	f.deviceCalls = append(f.deviceCalls, "rescue "+group+"/"+lab+"/"+device+"/"+strconv.FormatBool(enable))
	return f.deviceErr
}
func (f *fakeInfra) DeployLab(_ context.Context, _, _ string, meta infraModel.LabMeta, topo exerciseModel.Topology) error {
	f.meta = meta
	f.deployedTopo = topo
	return f.deployErr
}
func (f *fakeInfra) LabStatus(_ context.Context, _, _ string) (exerciseModel.LabDeployStatus, error) {
	return f.status, nil
}
func (f *fakeInfra) DestroyLabGroup(ctx context.Context, group string) error {
	f.destroyed = group
	f.destroyCtxErr = ctx.Err()
	return f.destroyErr
}
func (f *fakeInfra) EnsureLabClient(_ context.Context, _, client string) (string, error) {
	f.client = client
	f.ensured++
	return f.status.VPNConfig, nil
}
func (f *fakeInfra) DeleteLab(_ context.Context, group, lab string) error {
	f.deletedLabs = append(f.deletedLabs, group+"/"+lab)
	return f.deleteLabErr
}
func (f *fakeInfra) DeleteLabClient(_ context.Context, group, client string) error {
	f.deletedClients = append(f.deletedClients, group+"/"+client)
	return nil
}
func (f *fakeInfra) LabClientHandshake(_ context.Context, _, client string) (time.Time, error) {
	f.handshakeClient = client
	return f.handshake, f.handshakeErr
}
func (f *fakeInfra) ReconcileLabGroupAccess(_ context.Context, group string, policies []labAccessModel.ClientPolicy) error {
	f.policyGroup, f.policies = group, policies
	return f.policyErr
}

// fakeVPNStore captures the config the deploy path persists.
type fakeVPNStore struct {
	userID    uuid.UUID
	scope     vpnModel.Scope
	ref       uuid.NullUUID
	plaintext string
	calls     int
	deleted   []uuid.NullUUID
	storeErr  error
}

func (f *fakeVPNStore) GetConfig(_ context.Context, userID uuid.UUID, scope vpnModel.Scope, ref uuid.NullUUID) (string, error) {
	if userID != f.userID || scope != f.scope || ref != f.ref {
		return "", nil
	}
	return f.plaintext, nil
}

func (f *fakeVPNStore) StoreConfig(_ context.Context, userID uuid.UUID, scope vpnModel.Scope, ref uuid.NullUUID, plaintext string) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	f.userID, f.scope, f.ref, f.plaintext = userID, scope, ref, plaintext
	f.calls++
	return nil
}

func (f *fakeVPNStore) DeleteConfig(_ context.Context, _ uuid.UUID, _ vpnModel.Scope, ref uuid.NullUUID) error {
	f.deleted = append(f.deleted, ref)
	return nil
}

func TestDeployTestStatus_ReturnsTheAuthorsStoredVPNConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-group", CreatedBy: userID}, nil)
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{
		Phase: "Ready", Ready: true, VPNConfig: "redacted-tester-config",
	}}
	store := &fakeVPNStore{userID: userID, scope: vpnModel.ScopeTest, ref: uuid.NullUUID{}, plaintext: "wg-author-config"}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store})
	st, err := uc.DeployTestStatus(context.Background(), userID, deployID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.VPNConfig != "wg-author-config" {
		t.Errorf("the author's own config must be returned: %q", st.VPNConfig)
	}
	if infra.ensured != 0 {
		t.Errorf("polling the status must never create a client: %d", infra.ensured)
	}
}

func TestDeployTestStatus_NoConfig_NoStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-group", CreatedBy: userID}, nil)
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{Phase: "Provisioning"}}
	store := &fakeVPNStore{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store})
	if _, err := uc.DeployTestStatus(context.Background(), userID, deployID); err != nil {
		t.Fatal(err)
	}
	if store.calls != 0 {
		t.Errorf("must not store when no VPN config yet: %+v", store)
	}
}

func TestDeployVariantTest_DecryptsSecretsAndDeploys(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	cipher, err := secret.New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	variantID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())
	ct, err := cipher.EncryptWithContext([]byte("FLAG{plain}"), exercise.EnvSecretContext(variantID, "web", "FLAG"))
	if err != nil {
		t.Fatal(err)
	}
	variants := []exerciseModel.Variant{{
		ID: variantID,
		Topology: exerciseModel.Topology{
			Devices: []exerciseModel.Device{{
				ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx",
				EnvVars: []exerciseModel.EnvVar{
					{Name: "FLAG", Value: ct, Secret: true},
					{Name: "MODE", Value: "prod"},
				},
			}},
		},
	}}
	vb, _ := json.Marshal(variants)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: vb}, nil)

	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Infra: infra})

	ownerID := uuid.Must(uuid.NewV7())
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, VersionID: p.VersionID, VariantID: p.VariantID, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt}, nil
	})
	handle, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if handle.Group == "" || !strings.HasPrefix(handle.Lab, "l-") {
		t.Fatalf("handle wrong: %+v", handle)
	}

	if infra.meta.Labels[infraModel.LabelKind] != infraModel.KindTest || infra.meta.Labels[infraModel.LabelVersion] != versionID.String() || infra.meta.DeployGroup != "" {
		t.Errorf("test lab meta = %+v", infra.meta)
	}
	env := infra.deployedTopo.Devices[0].EnvVars
	if env[0].Value != "FLAG{plain}" {
		t.Errorf("secret env not decrypted for the agent: %q", env[0].Value)
	}
	if env[1].Value != "prod" {
		t.Errorf("non-secret env should pass through: %q", env[1].Value)
	}
}

func TestDeployVariantTest_RejectsInvalidForwardingPortBeforeLease(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	swID, hostID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, marshalErr := json.Marshal([]exerciseModel.Variant{{
		ID: variantID,
		Topology: exerciseModel.Topology{
			Devices: []exerciseModel.Device{
				{ID: swID, Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
				{ID: hostID, Name: "host", Type: exerciseModel.DeviceTypeContainer, Image: "nginx",
					Interfaces: []exerciseModel.Interface{{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeNone}}}},
			},
			Connections: []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{
				{Kind: exerciseModel.EndpointDevice, DeviceID: swID, Interface: "GigabitEthernet0/49"},
				{Kind: exerciseModel.EndpointDevice, DeviceID: hostID, Interface: "eth0"},
			}}},
		},
	}})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	_, deployErr := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID)
	if !errors.Is(deployErr, exerciseModel.ErrForwardingPortInvalid.Err()) {
		t.Fatalf("want invalid port error, got %v", deployErr)
	}
	if infra.deployedTopo.Devices != nil {
		t.Fatal("infrastructure received invalid topology")
	}
}

func TestDeployVariantTest_RemovesPartiallyCreatedGroupOnFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	group := ""
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		group = p.GroupName
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &fakeInfra{deployErr: errors.New("lab creation failed")}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := uc.DeployVariantTest(ctx, ownerID, versionID, variantID); err == nil {
		t.Fatal("deploy failure must be returned")
	}
	if group == "" || infra.destroyed != group || infra.destroyCtxErr != nil {
		t.Fatalf("partial group not cleaned with a live context: group=%q destroyed=%q contextErr=%v", group, infra.destroyed, infra.destroyCtxErr)
	}
}

func TestDeployVariantTest_KeepsLeaseIfPartialGroupCleanupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	group := ""
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		group = p.GroupName
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	infra := &fakeInfra{deployErr: errors.New("lab creation failed"), destroyErr: errors.New("cleanup failed")}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})

	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err == nil {
		t.Fatal("deploy failure must be returned")
	}
	if group == "" || infra.destroyed != group {
		t.Fatalf("partial group cleanup was not attempted: group=%q destroyed=%q", group, infra.destroyed)
	}
}

func TestResolveDeployedTopology_UsesPinnedVariantIndexAndDecryptsSecrets(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, err := secret.New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	variantID := uuid.Must(uuid.NewV7())
	ct, err := cipher.EncryptWithContext([]byte("FLAG{pinned}"), exercise.EnvSecretContext(variantID, "web", "FLAG"))
	if err != nil {
		t.Fatal(err)
	}
	versionID := uuid.Must(uuid.NewV7())
	variants, err := json.Marshal([]exerciseModel.Variant{{Index: 0}, {ID: variantID, Index: 7, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", EnvVars: []exerciseModel.EnvVar{{Name: "FLAG", Value: ct, Secret: true}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher})
	topo, err := uc.ResolveDeployedTopology(context.Background(), versionID, 7)
	if err != nil || topo.Devices[0].EnvVars[0].Value != "FLAG{pinned}" {
		t.Fatalf("topology=%+v err=%v", topo, err)
	}
}

func TestDeploy_NilInfra_Unavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: nil})

	if uc.InfrastructureAvailable() {
		t.Error("infrastructure must report unavailable when no agent is wired")
	}
	want := infraModel.ErrInfrastructureUnavailable.Err()
	if _, err := uc.DeployVariantTest(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())); !errors.Is(err, want) {
		t.Errorf("deploy: want unavailable, got %v", err)
	}
	if _, err := uc.DeployTestStatus(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())); !errors.Is(err, want) {
		t.Errorf("status: want unavailable, got %v", err)
	}
	if err := uc.DestroyDeployTest(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())); !errors.Is(err, want) {
		t.Errorf("destroy: want unavailable, got %v", err)
	}
}

func TestExtendTestDeploy_CapsLeaseAtOriginalEightHourWindow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	createdAt := time.Now().Add(-7*time.Hour - 30*time.Minute)
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{
		ID: deployID, CreatedBy: userID, CreatedAt: createdAt, ExpiresAt: time.Now().Add(time.Minute),
	}, nil)
	q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ExtendOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
			if arg.ExpiresAt.After(createdAt.Add(8 * time.Hour)) {
				t.Fatalf("lease extends beyond absolute maximum: %s", arg.ExpiresAt)
			}
			return postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, CreatedAt: createdAt, ExpiresAt: arg.ExpiresAt}, nil
		})
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	if _, err := uc.ExtendTestDeploy(context.Background(), userID, deployID); err != nil {
		t.Fatalf("extend: %v", err)
	}
}

func TestCleanupExpiredTestDeploys_DestroysGroupThenDeletesLease(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListExpiredExerciseTestDeploys(gomock.Any(), gomock.Any()).Return([]postgres.ExerciseTestDeployment{{
		ID: id, GroupName: "t-expired", CreatedBy: owner,
	}}, nil)
	row := postgres.ExerciseTestDeployment{ID: id, GroupName: "t-expired", LabName: "l-expired", CreatedBy: owner}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	if err := uc.CleanupExpiredTestDeploys(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if infra.destroyed != "t-expired" {
		t.Fatalf("wrong destroyed group: %q", infra.destroyed)
	}
}

type fakeSessions struct {
	got   labaccess.Session
	calls int
}

func (f *fakeSessions) Issue(_ context.Context, s labaccess.Session, _ time.Time) (labaccess.Link, error) {
	f.got = s
	f.calls++
	return labaccess.Link{URL: s.AccessURL + "/_auth?t=signed", Token: "signed", ExpiresAt: s.ExpiresAt}, nil
}

func TestDeployVariantTest_AllowsTheAuthorInTheGroupPolicy(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})

	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err != nil {
		t.Fatal(err)
	}
	if len(infra.policies) != 1 || infra.policies[0].Name != labBindingModel.ParticipantClientName(ownerID) || len(infra.policies[0].AllowedLabs) != 1 || !strings.HasPrefix(infra.policies[0].AllowedLabs[0], "l-") || infra.policyGroup == "" {
		t.Fatalf("the proxy and the VPN both need an allow for the author: %+v", infra)
	}
}

func TestDeployVariantTest_StaticVariantHasNoLab(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	_, err := uc.DeployVariantTest(context.Background(), uuid.Must(uuid.NewV7()), versionID, variantID)
	if !errors.Is(err, exerciseModel.ErrTestDeployNoLab.Err()) {
		t.Fatalf("want ErrTestDeployNoLab, got %v", err)
	}
}

func TestDeployVariantTest_PolicyFailureCleansTheGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	group := ""
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		group = p.GroupName
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &fakeInfra{policyErr: errors.New("policy failed")}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err == nil {
		t.Fatal("a deploy the author cannot reach must fail")
	}
	if infra.destroyed != group {
		t.Fatalf("group not destroyed: %q", infra.destroyed)
	}
}

func TestDeployVariantTest_CreatesTheAuthorsClientAtDeployAndKeepsItsConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{VPNConfig: "wg-author-config"}}
	store := &fakeVPNStore{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store})
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err != nil {
		t.Fatal(err)
	}
	if infra.client != labBindingModel.ParticipantClientName(ownerID) || infra.ensured != 1 {
		t.Fatalf("the client must exist from the deploy and match the policy client name: %q x%d", infra.client, infra.ensured)
	}
	if store.calls != 1 || store.userID != ownerID || store.scope != vpnModel.ScopeTest || store.plaintext != "wg-author-config" {
		t.Fatalf("the config is kept encrypted for the author: %+v", store)
	}
	if store.ref.Valid {
		t.Fatalf("the config is kept per author, not per deploy: %+v", store)
	}
}

func TestDeployTestStatus_NeverHandsOutThePlaceholderConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-group", CreatedBy: userID}, nil).Times(2)
	placeholder := "[Interface]\nPrivateKey = __PRIVATE_KEY__\n"
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{Phase: "Ready", Ready: true, VPNConfig: placeholder}}

	// Nothing stored for this deploy: the agent's placeholder config is dropped.
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: &fakeVPNStore{}})
	st, err := uc.DeployTestStatus(context.Background(), userID, deployID)
	if err != nil || st.VPNConfig != "" {
		t.Fatalf("placeholder must not reach the author: %q %v", st.VPNConfig, err)
	}
	// Another deploy's stored config is not this deploy's config.
	other := &fakeVPNStore{userID: userID, scope: vpnModel.ScopeTest, ref: uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}, plaintext: "wg-other"}
	uc = exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: other})
	if st, err = uc.DeployTestStatus(context.Background(), userID, deployID); err != nil || st.VPNConfig != "" {
		t.Fatalf("a config of another deploy must not be returned: %q %v", st.VPNConfig, err)
	}
}

func deployFixture(t *testing.T, infra *fakeInfra, store *fakeVPNStore, cleanup bool) (*exercise.ExerciseUseCase, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	if cleanup {
		q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	}
	return exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store}), ownerID, versionID, variantID
}

func TestDeployVariantTest_FailsAndCleansUpWhenTheConfigCannotBeKept(t *testing.T) {
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{VPNConfig: "wg-author-config"}}
	store := &fakeVPNStore{storeErr: errors.New("db down")}
	uc, owner, version, variant := deployFixture(t, infra, store, true)
	if _, err := uc.DeployVariantTest(context.Background(), owner, version, variant); err == nil {
		t.Fatal("a config that cannot be kept must fail the deploy")
	}
	if infra.destroyed == "" || len(store.deleted) != 1 {
		t.Fatalf("the half-made deploy is removed: destroyed=%q deleted=%v", infra.destroyed, store.deleted)
	}
}

func TestDeployVariantTest_RefusesAConfigWithoutThePrivateKey(t *testing.T) {
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{VPNConfig: "PrivateKey = __PRIVATE_KEY__"}}
	store := &fakeVPNStore{}
	uc, owner, version, variant := deployFixture(t, infra, store, true)
	if _, err := uc.DeployVariantTest(context.Background(), owner, version, variant); err == nil {
		t.Fatal("a placeholder config must fail the deploy")
	}
	if store.calls != 0 {
		t.Fatalf("a placeholder config is never stored: %+v", store)
	}
}

func TestDestroyDeployTest_DropsTheStoredConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: deployID, GroupName: "tu-group", LabName: "l-one", CreatedBy: userID}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), userID).Return([]postgres.ExerciseTestDeployment{row}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	store := &fakeVPNStore{}
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store})
	if err := uc.DestroyDeployTest(context.Background(), userID, deployID); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "tu-group" || len(infra.deletedLabs) != 0 {
		t.Fatalf("the last lab takes the whole group down: destroyed=%q labs=%v", infra.destroyed, infra.deletedLabs)
	}
	if len(store.deleted) != 1 || store.deleted[0].Valid {
		t.Fatalf("the author's config is removed with the group: %v", store.deleted)
	}
}

func TestDestroyDeployTest_KeepsTheGroupWhileAnotherLabRuns(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID, otherID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: deployID, GroupName: "tu-group", LabName: "l-one", CreatedBy: userID}
	other := postgres.ExerciseTestDeployment{ID: otherID, GroupName: "tu-group", LabName: "l-two", CreatedBy: userID}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), userID).Return([]postgres.ExerciseTestDeployment{row, other}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	store := &fakeVPNStore{}
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store})
	if err := uc.DestroyDeployTest(context.Background(), userID, deployID); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "" || len(infra.deletedLabs) != 1 || infra.deletedLabs[0] != "tu-group/l-one" {
		t.Fatalf("only this lab goes: destroyed=%q labs=%v", infra.destroyed, infra.deletedLabs)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("the author's config stays for the other lab: %v", store.deleted)
	}
	if len(infra.policies) != 1 || len(infra.policies[0].AllowedLabs) != 1 || infra.policies[0].AllowedLabs[0] != "l-two" {
		t.Fatalf("the group policy keeps only the remaining lab: %+v", infra.policies)
	}
}

func TestDeployVariantTest_SecondLabJoinsTheAuthorsGroupAndReusesItsClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	running := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), GroupName: "tu-" + ownerID.String(), LabName: "l-first", CreatedBy: ownerID, ExpiresAt: time.Now().Add(time.Hour)}
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return([]postgres.ExerciseTestDeployment{running}, nil).AnyTimes()
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), gomock.Any()).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil).AnyTimes()
	var created postgres.CreateExerciseTestDeployParams
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		created = p
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	infra := &fakeInfra{}
	store := &fakeVPNStore{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store, FlagConfig: config.ExerciseConfig{MaxActiveTestDeploys: 2}})
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err != nil {
		t.Fatal(err)
	}
	if created.GroupName != running.GroupName || created.LabName == running.LabName || !strings.HasPrefix(created.LabName, "l-") {
		t.Fatalf("the lab joins the same group under its own name: %+v", created)
	}
	if infra.ensured != 0 || store.calls != 0 || len(infra.deletedClients) != 0 {
		t.Fatalf("the client and the config are made once, with the first lab: ensured=%d store=%d", infra.ensured, store.calls)
	}
	if len(infra.policies) != 1 || len(infra.policies[0].AllowedLabs) != 2 || infra.policies[0].AllowedLabs[0] != "l-first" || infra.policies[0].AllowedLabs[1] != created.LabName {
		t.Fatalf("the policy lists both labs: %+v", infra.policies)
	}
}

func openSessionFixture(t *testing.T, ready bool, expires time.Time) (*exercise.ExerciseUseCase, *fakeSessions, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-" + deployID.String(), CreatedBy: userID, ExpiresAt: expires}, nil)
	sessions := &fakeSessions{}
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{Ready: ready, Access: []exerciseModel.LabAccess{{Device: "web", Port: 80, Protocol: "http", URL: "https://web-abc123.challenges.example.com"}}}}
	return exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, Sessions: sessions}), sessions, userID, deployID
}

func TestOpenTestDeployLink_IssuesTheAuthorsLinkUntilTheLeaseEnds(t *testing.T) {
	expires := time.Now().Add(90 * time.Minute)
	uc, sessions, userID, deployID := openSessionFixture(t, true, expires)
	link, err := uc.OpenTestDeployLink(context.Background(), userID, deployID, "web", 80)
	if err != nil {
		t.Fatal(err)
	}
	if !link.ExpiresAt.Equal(expires) || sessions.got.Client != labBindingModel.ParticipantClientName(userID) || sessions.got.Group != "t-"+deployID.String() || sessions.got.AccessURL != "https://web-abc123.challenges.example.com" {
		t.Fatalf("link: %+v session: %+v", link, sessions.got)
	}
}

func TestOpenTestDeployLink_RefusesNotReadyExpiredAndUnknownDevice(t *testing.T) {
	uc, sessions, userID, deployID := openSessionFixture(t, false, time.Now().Add(time.Hour))
	if _, err := uc.OpenTestDeployLink(context.Background(), userID, deployID, "web", 80); !errors.Is(err, exerciseModel.ErrTestDeployNotReady.Err()) {
		t.Fatalf("want not ready, got %v", err)
	}
	uc, _, userID, deployID = openSessionFixture(t, true, time.Now().Add(-time.Minute))
	if _, err := uc.OpenTestDeployLink(context.Background(), userID, deployID, "web", 80); !errors.Is(err, exerciseModel.ErrTestDeployNotFound.Err()) {
		t.Fatalf("want not found, got %v", err)
	}
	uc, sessions2, userID, deployID := openSessionFixture(t, true, time.Now().Add(time.Hour))
	if _, err := uc.OpenTestDeployLink(context.Background(), userID, deployID, "db", 5432); !errors.Is(err, exerciseModel.ErrTestDeployNoWebDevice.Err()) {
		t.Fatalf("want no web device, got %v", err)
	}
	if sessions.calls != 0 || sessions2.calls != 0 {
		t.Fatal("nothing may be issued")
	}
}

func TestOpenTestDeployLink_WithoutIssuerIsUnavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: postgresMocks.NewMockQuerier(ctrl), Infra: &fakeInfra{}})
	if _, err := uc.OpenTestDeployLink(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "web", 80); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("got %v", err)
	}
}

func TestDeployVariantTest_InjectsResolvedTaskFlagsIntoLinkedDevices(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	deviceID := uuid.Must(uuid.NewV7())
	link := uuid.NullUUID{UUID: deviceID, Valid: true}
	variants, _ := json.Marshal([]exerciseModel.Variant{{
		ID: variantID,
		Tasks: []exerciseModel.Task{
			{ID: uuid.Must(uuid.NewV7()), Name: "fixed", Flag: []string{"ICE{fixed}"}, LinkedDeviceID: link, DeviceFlagVar: "FLAG_FIXED"},
			{ID: uuid.Must(uuid.NewV7()), Name: "random", LinkedDeviceID: link, DeviceFlagVar: "FLAG_RANDOM"},
			{ID: uuid.Must(uuid.NewV7()), Name: "template", Flag: []string{"template:ICE{room-[A-C]\\d}"}, LinkedDeviceID: link, DeviceFlagVar: "FLAG_TEMPLATE"},
			{ID: uuid.Must(uuid.NewV7()), Name: "unlinked", Flag: []string{"ICE{other}"}},
		},
		Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: deviceID, Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx", EnvVars: []exerciseModel.EnvVar{{Name: "MODE", Value: "prod"}}}}},
	}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	var storedFlags []byte
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		storedFlags = p.Flags
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})

	handle, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if !strings.Contains(string(storedFlags), "ICE{fixed}") {
		t.Errorf("the flags must be stored with the deploy so a reopened one shows them: %s", storedFlags)
	}
	if len(handle.Flags) != 3 {
		t.Fatalf("flags = %+v, want one per linked task", handle.Flags)
	}
	env := map[string]string{}
	for _, e := range infra.deployedTopo.Devices[0].EnvVars {
		env[e.Name] = e.Value
	}
	if env["MODE"] != "prod" || env["FLAG_FIXED"] != "ICE{fixed}" {
		t.Fatalf("env = %v", env)
	}
	if !strings.HasPrefix(env["FLAG_RANDOM"], "ICE{") || !strings.HasPrefix(env["FLAG_TEMPLATE"], "ICE{room-") {
		t.Fatalf("random or template flag wrong: %v", env)
	}
	want := map[string]string{"fixed": env["FLAG_FIXED"], "random": env["FLAG_RANDOM"], "template": env["FLAG_TEMPLATE"]}
	for _, f := range handle.Flags {
		if want[f.Name] != f.Flag {
			t.Errorf("reported flag of %q = %q, injected %q", f.Name, f.Flag, want[f.Name])
		}
	}
}

func TestListTestDeploys_ScopesToTheExerciseAndDropsExpired(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, exerciseID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	live := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), ExpiresAt: time.Now().Add(time.Hour)}
	dead := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), ExpiresAt: time.Now().Add(-time.Minute)}
	q.EXPECT().ListOwnedExerciseTestDeploysForExercise(gomock.Any(), postgres.ListOwnedExerciseTestDeploysForExerciseParams{CreatedBy: owner, ExerciseID: exerciseID}).
		Return([]postgres.ExerciseTestDeployment{live, dead}, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), gomock.Any()).Return(postgres.ExerciseVersion{ExerciseID: exerciseID}, nil).AnyTimes()
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	got, err := uc.ListTestDeploys(context.Background(), owner, exerciseID)
	if err != nil || len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got[0].ExerciseID != exerciseID {
		t.Fatalf("a listed deploy names its exercise: %+v", got[0])
	}
}

func TestDeployVariantTest_RefusesASecondActiveLabOfTheSameUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner := uuid.Must(uuid.NewV7())
	live := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), ExpiresAt: time.Now().Add(time.Hour)}
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{live}, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), gomock.Any()).Return(postgres.ExerciseVersion{}, errors.New("gone")).AnyTimes()
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	_, err := uc.DeployVariantTest(context.Background(), owner, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, exerciseModel.ErrTestDeployActiveExists.Err()) {
		t.Fatalf("a second lab must be refused: %v", err)
	}
	if infra.deployedTopo.Devices != nil || infra.destroyed != "" {
		t.Fatal("nothing may be deployed")
	}
}

func TestCheckTestFlag_ExactCaseSensitiveAuthorOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, deployID, taskID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	stored, _ := json.Marshal([]exerciseModel.DeployFlag{{TaskID: taskID, Name: "Login", Flag: "ICE{Abc}"}})
	row := postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: owner, ExpiresAt: time.Now().Add(time.Hour), Flags: stored}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()
	q.EXPECT().MarkExerciseTestDeploySolved(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{}, nil).AnyTimes()
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})

	for flag, want := range map[string]bool{"ICE{Abc}": true, "ICE{abc}": false, "ICE{Abc} ": false, "": false} {
		got, err := uc.CheckTestFlag(context.Background(), owner, deployID, taskID, flag)
		if err != nil || got != want {
			t.Errorf("flag %q = %v, %v, want %v", flag, got, err, want)
		}
	}
	if got, err := uc.CheckTestFlag(context.Background(), owner, deployID, uuid.Must(uuid.NewV7()), "ICE{Abc}"); err != nil || got {
		t.Errorf("unknown task must be incorrect: %v, %v", got, err)
	}

	row.ExpiresAt = time.Now().Add(-time.Minute)
	q2 := postgresMocks.NewMockQuerier(ctrl)
	q2.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	uc2 := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q2})
	if _, err := uc2.CheckTestFlag(context.Background(), owner, deployID, taskID, "ICE{Abc}"); !errors.Is(err, exerciseModel.ErrTestDeployNotFound.Err()) {
		t.Errorf("expired deploy: %v", err)
	}
}

func TestDeployVariantTest_LimitIsConfigurable(t *testing.T) {
	live := func() postgres.ExerciseTestDeployment {
		return postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), ExpiresAt: time.Now().Add(time.Hour)}
	}
	for _, tc := range []struct {
		name    string
		limit   int
		active  int
		refused bool
	}{
		{"default is one", 0, 1, true},
		{"two allowed, one running: passes the check", 2, 1, false},
		{"two allowed, two running: refused", 2, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			owner := uuid.Must(uuid.NewV7())
			rows := make([]postgres.ExerciseTestDeployment, tc.active)
			for i := range rows {
				rows[i] = live()
			}
			q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return(rows, nil)
			q.EXPECT().GetExerciseVersionByID(gomock.Any(), gomock.Any()).Return(postgres.ExerciseVersion{}, errors.New("gone")).AnyTimes()
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}, FlagConfig: config.ExerciseConfig{MaxActiveTestDeploys: tc.limit}})
			_, err := uc.DeployVariantTest(context.Background(), owner, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
			if got := errors.Is(err, exerciseModel.ErrTestDeployActiveExists.Err()); got != tc.refused {
				t.Fatalf("refused=%v, want %v (err %v)", got, tc.refused, err)
			}
		})
	}
}

func TestDeployTestStatus_VPNConnectedOnlyAfterARecentHandshakeOfTheAuthorsClient(t *testing.T) {
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name      string
		handshake time.Time
		err       error
		connected bool
		hasTime   bool
	}{
		{"recent", time.Now().Add(-time.Minute), nil, true, true},
		{"stale", time.Now().Add(-10 * time.Minute), nil, false, true},
		{"never", time.Time{}, nil, false, false},
		{"stats failure does not break polling", time.Time{}, errors.New("agent down"), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-group", CreatedBy: userID}, nil)
			infra := &fakeInfra{status: exerciseModel.LabDeployStatus{Phase: "Ready"}, handshake: tc.handshake, handshakeErr: tc.err}
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
			st, err := uc.DeployTestStatus(context.Background(), userID, deployID)
			if err != nil {
				t.Fatal(err)
			}
			if st.VPNConnected != tc.connected || st.VPNLastHandshake.IsZero() == tc.hasTime {
				t.Errorf("connected=%v last=%v", st.VPNConnected, st.VPNLastHandshake)
			}
			if infra.handshakeClient != labBindingModel.ParticipantClientName(userID) {
				t.Errorf("must ask about the author's own client: %q", infra.handshakeClient)
			}
		})
	}
}

func TestCheckTestFlag_RemembersACorrectAnswerOnce(t *testing.T) {
	userID, deployID, taskID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	flags := []byte(`[{"task_id":"` + taskID.String() + `","name":"Login","flag":"ICE{ok}"}]`)
	for _, tc := range []struct {
		name   string
		flag   string
		solved string
		marks  int
		want   bool
	}{
		{"correct is stored", "ICE{ok}", `[]`, 1, true},
		{"already stored is not written again", "ICE{ok}", `["` + taskID.String() + `"]`, 0, true},
		{"wrong is never stored", "ICE{no}", `[]`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, Flags: flags, Solved: []byte(tc.solved), ExpiresAt: time.Now().Add(time.Hour)}, nil)
			q.EXPECT().MarkExerciseTestDeploySolved(gomock.Any(), postgres.MarkExerciseTestDeploySolvedParams{ID: deployID, CreatedBy: userID, TaskID: taskID.String()}).Return(postgres.ExerciseTestDeployment{}, nil).Times(tc.marks)
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
			got, err := uc.CheckTestFlag(context.Background(), userID, deployID, taskID, tc.flag)
			if err != nil || got != tc.want {
				t.Fatalf("got %v err %v", got, err)
			}
		})
	}
}

func TestDeployTestStatus_ReturnsTheSolvedTasks(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID, taskID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-group", CreatedBy: userID, Solved: []byte(`["` + taskID.String() + `"]`)}, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{status: exerciseModel.LabDeployStatus{Phase: "Ready"}}})
	st, err := uc.DeployTestStatus(context.Background(), userID, deployID)
	if err != nil || len(st.SolvedTasks) != 1 || st.SolvedTasks[0] != taskID {
		t.Fatalf("solved = %v err %v", st.SolvedTasks, err)
	}
}

func TestTestDeployDeviceActions_RunOnTheOwnedLab(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-group", LabName: "l-1", CreatedBy: userID}, nil).Times(2)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	if err := uc.ResetTestDeployDevice(context.Background(), userID, deployID, "web"); err != nil {
		t.Fatal(err)
	}
	if err := uc.RescueTestDeployDevice(context.Background(), userID, deployID, "web", true); err != nil {
		t.Fatal(err)
	}
	if len(infra.deviceCalls) != 2 || infra.deviceCalls[0] != "reset t-group/l-1/web" || infra.deviceCalls[1] != "rescue t-group/l-1/web/true" {
		t.Fatalf("calls = %v", infra.deviceCalls)
	}
}

func TestTestDeployDeviceActions_OnlyForTheOwnerAndALiveLease(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})

	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{}, pgx.ErrNoRows)
	if err := uc.ResetTestDeployDevice(context.Background(), userID, deployID, "web"); !errors.Is(err, exerciseModel.ErrTestDeployNotFound.Err()) {
		t.Fatalf("someone else's lab: err = %v, want not found", err)
	}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, GroupName: "t-group", CreatedBy: userID, ExpiresAt: time.Now().Add(-time.Minute)}, nil)
	if err := uc.RescueTestDeployDevice(context.Background(), userID, deployID, "web", true); !errors.Is(err, exerciseModel.ErrTestDeployNotFound.Err()) {
		t.Fatalf("expired lease: err = %v, want not found", err)
	}
	if len(infra.deviceCalls) != 0 {
		t.Fatalf("the agent must not be called: %v", infra.deviceCalls)
	}
}

// A secret is bound to its variant and variable: the same ciphertext pasted into another variant fails to open.
func TestResolveDeployedTopology_ASecretSealedForAnotherVariantDoesNotOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	sealedFor := uuid.Must(uuid.NewV7())
	ct, _ := cipher.EncryptWithContext([]byte("FLAG{moved}"), exercise.EnvSecretContext(sealedFor, "web", "FLAG"))
	versionID := uuid.Must(uuid.NewV7())
	otherVariant := uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: otherVariant, Index: 3, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", EnvVars: []exerciseModel.EnvVar{{Name: "FLAG", Value: ct, Secret: true}}}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher})
	if _, err := uc.ResolveDeployedTopology(context.Background(), versionID, 3); err == nil {
		t.Fatal("a secret copied under another variant must not decrypt")
	}
}
