package exercise_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"testing"
	"time"
)

type eventPreviewGate struct {
	fakeGate
	req           calendarUseCase.EventTestLabRequest
	until         time.Time
	releaseEvent  uuid.UUID
	releaseErr    error
	candidates    []calModel.TestLabHold
	keepCandidate bool
}

func (g *eventPreviewGate) AdmitEventTestLab(_ context.Context, req calendarUseCase.EventTestLabRequest) (calendarUseCase.TestLabRoom, error) {
	g.req = req
	if g.keepCandidate {
		g.candidates = append(g.candidates, calModel.TestLabHold{ID: req.ID, OwnerID: req.Owner, EventID: &req.EventID, GroupName: req.Group, Via: calModel.ViaEventPreview, ExpiresAt: g.until})
	}
	return calendarUseCase.TestLabRoom{Available: true, ExpiresAt: g.until}, g.err
}
func (g *eventPreviewGate) CheckEventTestLabRoom(context.Context, calendarUseCase.EventTestLabRequest) (calendarUseCase.TestLabRoom, error) {
	return calendarUseCase.TestLabRoom{Available: true}, nil
}
func (g *eventPreviewGate) EventPreviewAllocated(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}
func (g *eventPreviewGate) ReleaseEventTestLab(_ context.Context, eventID, id uuid.UUID) error {
	g.releaseEvent = eventID
	g.released = append(g.released, id)
	return g.releaseErr
}
func (g *eventPreviewGate) ExtendEventTestLab(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) (time.Time, error) {
	return g.until, g.err
}

// A preview lab started before previews ran on the author's lease keeps its event path until it ends.
func TestLegacyEventPreviewCleanupNeverTouchesAuthorsOtherGroup(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	owner, eventID, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	preview := postgres.ExerciseTestDeployment{ID: id, CreatedBy: owner, EventID: uuid.NullUUID{UUID: eventID, Valid: true}, GroupName: legacyPreviewGroup(eventID, owner), LabName: "preview", ExpiresAt: time.Now().Add(time.Hour)}
	author := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), CreatedBy: owner, GroupName: "tu-author", LabName: "author", ExpiresAt: time.Now().Add(time.Hour)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(preview, nil)
	ended := preview
	ended.ExpiresAt = time.Now().Add(-time.Second)
	q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(ended, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{preview, author}, nil).Times(2)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &eventPreviewInfra{}
	store := &fakeVPNStore{}
	gate := &eventPreviewGate{}
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store})
	u.SetTestLabGate(gate)
	require.NoError(t, u.DestroyDeployTest(context.Background(), owner, id))
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(ended, nil)
	require.NoError(t, u.CheckTestDeployRemoved(context.Background(), owner, id, 1))
	require.Equal(t, legacyPreviewGroup(eventID, owner), infra.destroyed)
	require.Empty(t, infra.deletedLabs)
	require.Equal(t, eventID, gate.releaseEvent)
	require.Equal(t, []uuid.NullUUID{{UUID: eventID, Valid: true}}, store.deleted)
}
func TestLegacyEventPreviewRenewFailsClosedWithoutEventAllocation(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	owner, eventID, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: id, CreatedBy: owner, EventID: uuid.NullUUID{UUID: eventID, Valid: true}, GroupName: legacyPreviewGroup(eventID, owner), ExpiresAt: time.Now().Add(time.Hour)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil).Times(2)
	gate := &eventPreviewGate{fakeGate: fakeGate{err: calModel.ErrNotEnoughReserved.Err()}}
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	u.SetTestLabGate(gate)
	_, err := u.ExtendTestDeploy(context.Background(), owner, id)
	require.Error(t, err)
}

type eventPreviewInfra struct{ fakeInfra }

func (i *eventPreviewInfra) LabGroupExists(context.Context, string) (bool, error) { return false, nil }
func (i *eventPreviewInfra) LabExists(context.Context, string, string) (bool, error) {
	return false, nil
}

func (i *eventPreviewInfra) GroupSizes(plan infraModel.GroupPlan) (infraModel.GroupSizes, bool) {
	return infraModel.GroupSizes{VPN: resourcesModel.Amount{CPUMillicores: 100, MemoryBytes: 100}, Gateway: resourcesModel.Amount{CPUMillicores: int64(10 * plan.InternetLabs), MemoryBytes: int64(10 * plan.InternetLabs)}}, true
}

type eventPreviewPresenceInfra struct {
	eventPreviewInfra
	exists bool
}

func (i *eventPreviewPresenceInfra) LabExists(context.Context, string, string) (bool, error) {
	return i.exists, nil
}
func (i *eventPreviewPresenceInfra) LabGroupExists(context.Context, string) (bool, error) {
	return i.exists, nil
}

func TestStaticEventPreviewEligibilityNeedsNoInfrastructureOrCalendar(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	eventID, version, variant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	encoded, _ := json.Marshal([]exerciseModel.Variant{{ID: variant}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), version).Return(postgres.ExerciseVersion{ID: version, Variants: encoded}, nil)
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	allowed, err := u.CanStartEventPreview(context.Background(), eventID, version, variant)
	require.NoError(t, err)
	require.True(t, allowed)
}

func (g *eventPreviewGate) EventPreviewCleanupCandidates(context.Context) ([]calModel.TestLabHold, error) {
	return g.candidates, nil
}
func TestLegacyEventPreviewReleaseFailureRetainsDeploymentForRetry(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	owner, eventID, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: id, CreatedBy: owner, EventID: uuid.NullUUID{UUID: eventID, Valid: true}, GroupName: legacyPreviewGroup(eventID, owner), ExpiresAt: time.Now().Add(-time.Hour)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil).Times(2)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil).Times(2)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	gate := &eventPreviewGate{releaseErr: errors.New("calendar unavailable")}
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &eventPreviewInfra{}})
	u.SetTestLabGate(gate)
	require.Error(t, u.EndExpiredTestDeploy(context.Background(), owner, id))
	gate.releaseErr = nil
	require.NoError(t, u.EndExpiredTestDeploy(context.Background(), owner, id))
	require.Len(t, gate.released, 2)
}

type previewRecoveryInfra struct {
	eventPreviewInfra
	groupExists, labExists bool
	nativeErr              error
	cleanupCtxErr          error
}

func (i *previewRecoveryInfra) LabGroupExists(context.Context, string) (bool, error) {
	return i.groupExists, nil
}
func (i *previewRecoveryInfra) LabExists(context.Context, string, string) (bool, error) {
	return i.labExists, nil
}
func (i *previewRecoveryInfra) DestroyLabGroup(ctx context.Context, group string) error {
	i.destroyed = group
	i.cleanupCtxErr = ctx.Err()
	if i.nativeErr != nil {
		return i.nativeErr
	}
	i.groupExists = false
	return nil
}
func (i *previewRecoveryInfra) DeleteLab(ctx context.Context, group, lab string) error {
	i.deletedLabs = append(i.deletedLabs, group+"/"+lab)
	i.cleanupCtxErr = ctx.Err()
	if i.nativeErr != nil {
		return i.nativeErr
	}
	i.labExists = false
	return nil
}
func TestEventPreviewOrphanHoldCleanupProtectsLiveSiblingAndRetries(t *testing.T) {
	for _, sibling := range []bool{false, true} {
		t.Run(fmt.Sprint(sibling), func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			eventID, owner, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			groupHash := sha256.Sum256(append(eventID.Bytes(), owner.Bytes()...))
			group := fmt.Sprintf("tp-%x", groupHash[:16])
			hold := calModel.TestLabHold{ID: id, OwnerID: owner, EventID: &eventID, GroupName: group, Via: calModel.ViaEventPreview, ExpiresAt: time.Now().Add(time.Hour)}
			gate := &eventPreviewGate{candidates: []calModel.TestLabHold{hold}}
			infra := &previewRecoveryInfra{groupExists: true, labExists: true, nativeErr: errors.New("temporary native failure")}
			u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
			u.SetTestLabGate(gate)
			q.EXPECT().ListExpiredExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).Times(3)
			q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{}, pgx.ErrNoRows).Times(3)
			rows := []postgres.ExerciseTestDeployment{{ID: uuid.Must(uuid.NewV7()), CreatedBy: owner, GroupName: "tu-other-scope", ExpiresAt: time.Now().Add(time.Hour)}}
			if sibling {
				rows = append(rows, postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), CreatedBy: owner, GroupName: group, LabName: "live-sibling", ExpiresAt: time.Now().Add(time.Hour)})
			}
			q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return(rows, nil).Times(3)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.Error(t, u.CleanupExpiredTestDeploys(ctx))
			require.Empty(t, gate.released)
			require.NoError(t, infra.cleanupCtxErr, "recovery ignores the failed request cancellation and uses a fresh bounded context")
			infra.nativeErr = nil
			gate.releaseErr = errors.New("calendar still unavailable")
			require.Error(t, u.CleanupExpiredTestDeploys(context.Background()))
			gate.releaseErr = nil
			require.NoError(t, u.CleanupExpiredTestDeploys(context.Background()))
			require.Len(t, gate.released, 2)
			if sibling {
				require.Empty(t, infra.destroyed)
				require.True(t, infra.groupExists)
				require.Len(t, infra.deletedLabs, 2)
				require.NotContains(t, infra.deletedLabs[0], "live-sibling")
			} else {
				require.Equal(t, group, infra.destroyed)
				require.Empty(t, infra.deletedLabs)
			}
		})
	}
}

type previewCommitFailureUnit struct {
	err    error
	cancel context.CancelFunc
}

func (u *previewCommitFailureUnit) Save() error {
	err := u.err
	u.err = nil
	if err != nil && u.cancel != nil {
		u.cancel()
	}
	return err
}
func (u *previewCommitFailureUnit) Restore() error { return nil }

type previewCommitFailureWorker struct {
	q    testDeployRepo.Queries
	unit *previewCommitFailureUnit
}

func (w previewCommitFailureWorker) UnitOfWork(ctx context.Context) (context.Context, testDeployRepo.Queries, postgres.UoW, error) {
	return ctx, w.q, w.unit, nil
}
func TestEventPreviewOrphanSweepLeavesCommittedDeploymentAlone(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	eventID, owner, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	hash := sha256.Sum256(append(eventID.Bytes(), owner.Bytes()...))
	group := fmt.Sprintf("tp-%x", hash[:16])
	gate := &eventPreviewGate{candidates: []calModel.TestLabHold{{ID: id, OwnerID: owner, EventID: &eventID, GroupName: group, Via: calModel.ViaEventPreview, ExpiresAt: time.Now().Add(time.Hour)}}}
	infra := &previewRecoveryInfra{groupExists: true, labExists: true}
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	u.SetTestLabGate(gate)
	q.EXPECT().ListExpiredExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: id, CreatedBy: owner, EventID: uuid.NullUUID{UUID: eventID, Valid: true}, GroupName: group, ExpiresAt: time.Now().Add(time.Hour)}, nil)
	require.NoError(t, u.CleanupExpiredTestDeploys(context.Background()))
	require.Empty(t, infra.destroyed)
	require.Empty(t, infra.deletedLabs)
	require.Empty(t, gate.released)
}

func legacyPreviewGroup(eventID, owner uuid.UUID) string {
	hash := sha256.Sum256(append(eventID.Bytes(), owner.Bytes()...))
	return fmt.Sprintf("tp-%x", hash[:16])
}

// A preview lab is the starting staff member's test run: a short lease of the platform's test capacity in their
// own test group, with their own VPN config; the event's reservation is never asked.
func TestEventPreviewDeployTakesTheStartersTestLease(t *testing.T) {
	q, variantID, versionID, owner := gateFixture(t)
	eventID := uuid.Must(uuid.NewV7())
	gate := &eventPreviewGate{}
	infra := &eventPreviewInfra{fakeInfra: fakeInfra{status: exerciseModel.LabDeployStatus{VPNConfig: "author-config"}}}
	vpn := &fakeVPNStore{}
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: vpn})
	u.SetTestLabGate(gate)
	var written postgres.CreateExerciseTestDeployParams
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		written = p
		return postgres.ExerciseTestDeployment{ID: p.ID, EventID: p.EventID, GroupName: p.GroupName, CreatedBy: p.CreatedBy, ExpiresAt: p.ExpiresAt}, nil
	})
	before := time.Now()
	_, err := u.DeployEventPreviewVariantTest(context.Background(), eventID, owner, versionID, variantID, 30*time.Minute)
	require.NoError(t, err)
	require.Len(t, gate.admitted, 1)
	require.Equal(t, owner, gate.admitted[0].Owner)
	require.Equal(t, 30*time.Minute, gate.admitted[0].Lease)
	require.Equal(t, written.ID, gate.admitted[0].ID)
	require.True(t, written.EventID.Valid)
	require.Equal(t, eventID, written.EventID.UUID)
	require.Equal(t, "tu-"+owner.String(), written.GroupName)
	require.WithinDuration(t, before.Add(30*time.Minute), written.ExpiresAt, 5*time.Second)
	require.False(t, vpn.ref.Valid, "the preview uses the author's own VPN config")
	require.Equal(t, uuid.Nil, gate.releaseEvent)
}

// Without free test capacity the launch is refused with the calendar's clear «no free resources» error and
// nothing is created.
func TestEventPreviewDeployWithoutRoomSaysNoFreeResources(t *testing.T) {
	q, variantID, versionID, owner := gateFixture(t)
	noRoom := calModel.ErrNoTestLabRoom.Err()
	gate := &eventPreviewGate{fakeGate: fakeGate{err: noRoom}}
	infra := &fakeInfra{}
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	u.SetTestLabGate(gate)
	_, err := u.DeployEventPreviewVariantTest(context.Background(), uuid.Must(uuid.NewV7()), owner, versionID, variantID, 0)
	require.ErrorIs(t, err, noRoom)
	require.Zero(t, infra.deployCalls)
}

// The preview lease ends like a studio test run: the lab is removed and the run's room is released.
func TestEventPreviewLeaseEndRemovesTheLabAndReleasesTheRun(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	owner, eventID, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: id, CreatedBy: owner, EventID: uuid.NullUUID{UUID: eventID, Valid: true}, GroupName: "tu-" + owner.String(), LabName: "preview", ExpiresAt: time.Now().Add(-time.Minute)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil).AnyTimes()
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	gate := &eventPreviewGate{}
	infra := &eventPreviewInfra{}
	u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: &fakeVPNStore{}})
	u.SetTestLabGate(gate)
	require.NoError(t, u.EndExpiredTestDeploy(context.Background(), owner, id))
	require.Equal(t, []uuid.UUID{id}, gate.released)
	require.Equal(t, uuid.Nil, gate.releaseEvent, "no event hold is touched")
}

// At the test-lab cap neither kind of lab replaces the other: a preview next to a running studio lab (and a studio
// lab next to a running preview) is refused with an error that names the running lab, and nothing is removed.
func TestTestLabCapNamesTheRunningLabAndRemovesNothing(t *testing.T) {
	for _, previewRunning := range []bool{false, true} {
		t.Run(fmt.Sprintf("preview running %v", previewRunning), func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			owner, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			variantID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			device := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}
			variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{device}}}})
			q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil).AnyTimes()
			runningVersion, runningExercise := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().GetExerciseVersionByID(gomock.Any(), runningVersion).Return(postgres.ExerciseVersion{ID: runningVersion, ExerciseID: runningExercise}, nil).AnyTimes()
			q.EXPECT().GetExerciseByID(gomock.Any(), runningExercise).Return(postgres.Exercise{ID: runningExercise, Name: "Running lab"}, nil).AnyTimes()
			running := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), CreatedBy: owner, GroupName: "tu-" + owner.String(), LabName: "running", VersionID: runningVersion, ExpiresAt: time.Now().Add(time.Hour)}
			if previewRunning {
				running.EventID = uuid.NullUUID{UUID: eventID, Valid: true}
			}
			q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{running}, nil).AnyTimes()
			infra := &eventPreviewInfra{}
			gate := &eventPreviewGate{}
			u := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
			u.SetTestLabGate(gate)
			var err error
			if previewRunning {
				_, err = u.DeployVariantTest(context.Background(), owner, versionID, variantID, 0)
			} else {
				_, err = u.DeployEventPreviewVariantTest(context.Background(), eventID, owner, versionID, variantID, 0)
			}
			require.ErrorIs(t, err, exerciseModel.ErrTestDeployActiveExists.Err())
			var public interface{ PublicContext() map[string]any }
			require.True(t, errors.As(err, &public))
			labs, ok := public.PublicContext()["running"].([]map[string]any)
			require.True(t, ok, "%+v", public.PublicContext())
			require.Len(t, labs, 1)
			require.Equal(t, running.ID.String(), labs[0]["ID"])
			require.Equal(t, "Running lab", labs[0]["Name"])
			_, hasEvent := labs[0]["EventID"]
			require.Equal(t, previewRunning, hasEvent)
			require.Zero(t, infra.deployCalls)
			require.Empty(t, infra.destroyed)
			require.Empty(t, infra.deletedLabs)
			require.Empty(t, gate.released)
		})
	}
}
