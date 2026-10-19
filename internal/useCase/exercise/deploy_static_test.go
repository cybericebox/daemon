package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
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
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

func staticFixture(t *testing.T, tasks []exerciseModel.Task, existing []postgres.ExerciseTestDeployment) (*postgresMocks.MockQuerier, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(existing, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Tasks: tasks}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	return q, variantID, versionID, ownerID
}

func expectStaticCreate(q *postgresMocks.MockQuerier, got *postgres.CreateExerciseTestDeployParams) {
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		*got = p
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, LabName: p.LabName, VersionID: p.VersionID, VariantID: p.VariantID, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt, Flags: p.Flags}, nil
	})
}

func flagsByTask(t *testing.T, raw []byte) map[uuid.UUID]string {
	t.Helper()
	var flags []exerciseModel.DeployFlag
	if err := json.Unmarshal(raw, &flags); err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]string{}
	for _, f := range flags {
		out[f.TaskID] = f.Flag
	}
	return out
}

func TestDeployVariantTest_StaticRunsWithoutAgentOrResources(t *testing.T) {
	t1, t2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q, variantID, versionID, ownerID := staticFixture(t, []exerciseModel.Task{{ID: t1, Name: "Login", Flag: []string{"ICE{fixed}"}}, {ID: t2, Name: "Admin"}}, nil)
	var created postgres.CreateExerciseTestDeployParams
	expectStaticCreate(q, &created)
	gate := &fakeGate{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: nil})
	uc.SetTestLabGate(gate)
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	uc.SetClock(func() time.Time { return now })

	handle, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.GroupName != "" || created.LabName != "" || created.EventID.Valid {
		t.Fatalf("a static row has no group or lab: %+v", created)
	}
	flags := flagsByTask(t, created.Flags)
	if flags[t1] != "ICE{fixed}" || !regexp.MustCompile(`^ICE\{[0-9a-f]{40}\}$`).MatchString(flags[t2]) {
		t.Fatalf("flags = %v", flags)
	}
	names := map[uuid.UUID]string{}
	for _, f := range handle.Flags {
		names[f.TaskID] = f.Name
	}
	var stored []exerciseModel.DeployFlag
	if err := json.Unmarshal(created.Flags, &stored); err != nil {
		t.Fatal(err)
	}
	for _, f := range stored {
		if f.Name != names[f.TaskID] || (f.TaskID == t1 && f.Name != "Login") || (f.TaskID == t2 && f.Name != "Admin") {
			t.Fatalf("flag names must be the task names: %+v", stored)
		}
	}
	if !handle.Static || handle.Lab != "" || len(handle.Flags) != 2 {
		t.Fatalf("handle = %+v", handle)
	}
	if len(gate.admitted) != 1 || !gate.admitted[0].Static || gate.admitted[0].Size.CPUMillicores != 0 || gate.admitted[0].Size.MemoryBytes != 0 || gate.admitted[0].LargestDevice.CPUMillicores != 0 || gate.admitted[0].LargestDevice.MemoryBytes != 0 {
		t.Fatalf("the gate must see one zero-size request: %+v", gate.admitted)
	}
	if d := created.ExpiresAt.Sub(now.Add(2 * time.Hour)); d < -time.Second || d > time.Second {
		t.Fatalf("lease = %s", created.ExpiresAt)
	}
}

func TestDeployVariantTest_StaticCountsTowardTheLimit(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	owner := uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), GroupName: "g", LabName: "l", CreatedBy: owner, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	q, variantID, versionID, _ := staticFixture(t, nil, []postgres.ExerciseTestDeployment{row})
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, FlagConfig: config.ExerciseConfig{MaxActiveTestDeploys: 1}})
	uc.SetClock(func() time.Time { return now })

	_, err := uc.DeployVariantTest(context.Background(), owner, versionID, variantID, 0)
	if !errors.Is(err, exerciseModel.ErrTestDeployActiveExists.Err()) {
		t.Fatalf("want ErrTestDeployActiveExists, got %v", err)
	}
}

func TestDeployVariantTest_StaticIgnoresDeviceFreeTemplateFlags(t *testing.T) {
	t1 := uuid.Must(uuid.NewV7())
	q, variantID, versionID, ownerID := staticFixture(t, []exerciseModel.Task{{ID: t1, Flag: []string{"template:ICE{[0-9][0-9][0-9][0-9]}"}}}, nil)
	var created postgres.CreateExerciseTestDeployParams
	expectStaticCreate(q, &created)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0); err != nil {
		t.Fatal(err)
	}
	if f := flagsByTask(t, created.Flags)[t1]; !regexp.MustCompile(`^ICE\{[0-9]{4}\}$`).MatchString(f) {
		t.Fatalf("flag = %q", f)
	}
}

func TestDeployVariantTest_StaticWithAgentDownStillWorks(t *testing.T) {
	q, variantID, versionID, ownerID := staticFixture(t, []exerciseModel.Task{{ID: uuid.Must(uuid.NewV7())}}, nil)
	var created postgres.CreateExerciseTestDeployParams
	expectStaticCreate(q, &created)
	infra := &fakeInfra{deployErr: errors.New("agent down"), policyErr: errors.New("agent down")}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0); err != nil {
		t.Fatal(err)
	}
	if infra.deployCalls != 0 || len(infra.policies) != 0 || infra.ensured != 0 || infra.policyGroup != "" {
		t.Fatalf("the agent must not be touched: %+v", infra)
	}
}

func TestDeployVariantTest_EventPreviewStaticStillNoLab(t *testing.T) {
	q, variantID, versionID, ownerID := staticFixture(t, nil, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	_, err := uc.DeployEventPreviewVariantTest(context.Background(), uuid.Must(uuid.NewV7()), ownerID, versionID, variantID, 0)
	if !errors.Is(err, exerciseModel.ErrTestDeployNoLab.Err()) {
		t.Fatalf("want ErrTestDeployNoLab, got %v", err)
	}
}

func TestDeployVariantTest_LabVariantWithNilInfra(t *testing.T) {
	q, variantID, versionID, ownerID := gateFixture(t)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: nil})
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("want ErrInfrastructureUnavailable, got %v", err)
	}
}

func TestSameTestGroup_IgnoresStaticRows(t *testing.T) {
	rows := []exerciseModel.TestDeploy{
		{ID: uuid.Must(uuid.NewV7())},
		{ID: uuid.Must(uuid.NewV7()), GroupName: "tu-a", LabName: "l-1"},
		{ID: uuid.Must(uuid.NewV7()), GroupName: "tu-a", LabName: "l-2"},
	}
	if !rows[0].IsStatic() {
		t.Fatal("the first row must be static")
	}
	if got := exercise.SameTestGroup(rows, "tu-a"); len(got) != 2 {
		t.Fatalf("want the 2 labs of the group, got %d", len(got))
	}
	if got := exercise.SameTestGroup(rows, ""); len(got) != 0 {
		t.Fatalf("a static row belongs to no group, got %d", len(got))
	}
}

func staticRow(ownerID uuid.UUID, expires time.Time) postgres.ExerciseTestDeployment {
	return postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), CreatedBy: ownerID, ExpiresAt: expires}
}

func TestDeployVariantTest_StaticRowsDoNotMakeOrBlockTheGroup(t *testing.T) {
	for name, expires := range map[string]time.Time{"active": time.Now().Add(time.Hour), "expired": time.Now().Add(-time.Hour)} {
		t.Run(name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return([]postgres.ExerciseTestDeployment{staticRow(ownerID, expires)}, nil).AnyTimes()
			variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
			q.EXPECT().GetExerciseVersionByID(gomock.Any(), gomock.Any()).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil).AnyTimes()
			var created postgres.CreateExerciseTestDeployParams
			q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
				created = p
				return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, LabName: p.LabName, CreatedBy: p.CreatedBy}, nil
			})
			infra := &fakeInfra{}
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: &fakeVPNStore{}, FlagConfig: config.ExerciseConfig{MaxActiveTestDeploys: 2}})
			if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0); err != nil {
				if errors.Is(err, exerciseModel.ErrTestDeployGroupBusy.Err()) {
					t.Fatal("a static row must not block the group")
				}
				t.Fatal(err)
			}
			if infra.deployCalls != 1 || infra.ensured != 1 {
				t.Fatalf("the lab is the first of the group: deploys=%d ensured=%d", infra.deployCalls, infra.ensured)
			}
			if len(infra.policies) != 1 || len(infra.policies[0].AllowedLabs) != 1 || infra.policies[0].AllowedLabs[0] != created.LabName || created.LabName == "" {
				t.Fatalf("the policy lists exactly the new lab: %+v", infra.policies)
			}
		})
	}
}

func TestDestroyDeployTest_LabNextToStaticDestroysTheGroup(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: deployID, GroupName: "tu-group", LabName: "l-one", CreatedBy: userID}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), userID).Return([]postgres.ExerciseTestDeployment{row, staticRow(userID, time.Now().Add(time.Hour))}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: &fakeVPNStore{}})
	if err := uc.DestroyDeployTest(context.Background(), userID, deployID); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "tu-group" || len(infra.deletedLabs) != 0 {
		t.Fatalf("the static row is not another lab: destroyed=%q labs=%v", infra.destroyed, infra.deletedLabs)
	}
}

func staticFlagsJSON(t *testing.T, flags ...exerciseModel.DeployFlag) []byte {
	t.Helper()
	raw, err := json.Marshal(flags)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDeployTestStatus_StaticIsReadyAtOnceWithoutAgent(t *testing.T) {
	t1 := uuid.Must(uuid.NewV7())
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for name, tc := range map[string]struct {
		expires time.Time
		expired bool
	}{"live": {time.Now().Add(time.Hour), false}, "expired": {time.Now().Add(-time.Hour), true}} {
		t.Run(name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{
				ID: deployID, CreatedBy: userID, ExpiresAt: tc.expires, Solved: []byte(`["` + t1.String() + `"]`),
			}, nil)
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: nil})
			st, err := uc.DeployTestStatus(context.Background(), userID, deployID)
			if err != nil {
				t.Fatal(err)
			}
			if !st.Ready || st.Phase != "" || st.Removing || st.VPNConfig != "" || len(st.Access) != 0 || st.Expired != tc.expired {
				t.Fatalf("status = %+v", st)
			}
			if len(st.SolvedTasks) != 1 || st.SolvedTasks[0] != t1 || !st.ExpiresAt.Equal(tc.expires) {
				t.Fatalf("status = %+v", st)
			}
		})
	}
}

func TestDeployTestStatus_LabRowWithNilInfra_Unavailable(t *testing.T) {
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{
		ID: deployID, CreatedBy: userID, GroupName: "g", LabName: "l", ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: nil})
	if _, err := uc.DeployTestStatus(context.Background(), userID, deployID); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("want ErrInfrastructureUnavailable, got %v", err)
	}
}

func TestCheckTestFlag_Static(t *testing.T) {
	t1 := uuid.Must(uuid.NewV7())
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := func(expires time.Time) postgres.ExerciseTestDeployment {
		return postgres.ExerciseTestDeployment{
			ID: deployID, CreatedBy: userID, ExpiresAt: expires,
			Flags: staticFlagsJSON(t, exerciseModel.DeployFlag{TaskID: t1, Name: "Login", Flag: "ICE{abc}"}),
		}
	}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})

	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row(time.Now().Add(time.Hour)), nil).Times(2)
	q.EXPECT().MarkExerciseTestDeploySolved(gomock.Any(), gomock.Any()).Return(row(time.Now().Add(time.Hour)), nil).Times(1)
	if ok, err := uc.CheckTestFlag(context.Background(), userID, deployID, t1, "ICE{abc}"); err != nil || !ok {
		t.Fatalf("right flag: ok=%v err=%v", ok, err)
	}
	if ok, err := uc.CheckTestFlag(context.Background(), userID, deployID, t1, "ice{abc}"); err != nil || ok {
		t.Fatalf("wrong flag: ok=%v err=%v", ok, err)
	}

	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row(time.Now().Add(-time.Minute)), nil)
	if _, err := uc.CheckTestFlag(context.Background(), userID, deployID, t1, "ICE{abc}"); !errors.Is(err, exerciseModel.ErrTestDeployNotFound.Err()) {
		t.Fatalf("expired: want not found, got %v", err)
	}
}

func TestExtendTestDeploy_StaticCappedAtMax(t *testing.T) {
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC)
	createdAt := now.Add(-7*time.Hour - 30*time.Minute)
	row := postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, CreatedAt: createdAt, ExpiresAt: now.Add(time.Minute)}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ExtendOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
			if arg.ExpiresAt.After(createdAt.Add(8 * time.Hour)) {
				t.Fatalf("lease beyond the maximum: %s", arg.ExpiresAt)
			}
			row.ExpiresAt = arg.ExpiresAt
			return row, nil
		})
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	uc.SetTestLabGate(&fakeGate{})
	uc.SetClock(func() time.Time { return now })
	got, err := uc.ExtendTestDeploy(context.Background(), userID, deployID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt.After(createdAt.Add(8 * time.Hour)) {
		t.Fatalf("expiry = %s", got.ExpiresAt)
	}
}

func TestOpenTestDeployLink_And_DeviceActions_RefuseStatic(t *testing.T) {
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{
		ID: deployID, CreatedBy: userID, ExpiresAt: time.Now().Add(time.Hour),
	}, nil).Times(3)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, Sessions: &fakeSessions{}})
	want := exerciseModel.ErrTestDeployStatic.Err()

	if _, err := uc.OpenTestDeployLink(context.Background(), userID, deployID, "web", 80); !errors.Is(err, want) {
		t.Fatalf("open link: want ErrTestDeployStatic, got %v", err)
	}
	if err := uc.ResetTestDeployDevice(context.Background(), userID, deployID, "web"); !errors.Is(err, want) {
		t.Fatalf("reset: want ErrTestDeployStatic, got %v", err)
	}
	if err := uc.RescueTestDeployDevice(context.Background(), userID, deployID, "web", true); !errors.Is(err, want) {
		t.Fatalf("rescue: want ErrTestDeployStatic, got %v", err)
	}
	if len(infra.deviceCalls) != 0 {
		t.Fatalf("the agent must not be called: %v", infra.deviceCalls)
	}
}

func TestDestroyDeployTest_StaticDeletesTheRowAtOnce(t *testing.T) {
	for name, withInfra := range map[string]bool{"with agent": true, "no infra": false} {
		t.Run(name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			row := postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, ExpiresAt: time.Now().Add(time.Hour)}
			q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
			q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(1)
			q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Times(0)
			q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Times(0)
			infra := &fakeInfra{}
			deps := exercise.Dependencies{Repo: q}
			if withInfra {
				deps.Infra, deps.VPNStore = infra, &fakeVPNStore{}
			}
			uc := exercise.NewExerciseUseCase(deps)
			if err := uc.DestroyDeployTest(context.Background(), userID, deployID); err != nil {
				t.Fatal(err)
			}
			if infra.destroyed != "" || len(infra.deletedLabs) != 0 || len(infra.policies) != 0 {
				t.Fatalf("the agent must not be called: %+v", infra)
			}
		})
	}
}

func TestEndExpiredTestDeploy_StaticRemovedWithoutAgent(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	run := func(t *testing.T, row *postgres.ExerciseTestDeployment, deletes int) error {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		if row == nil {
			q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{}, pgx.ErrNoRows)
		} else {
			q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(*row, nil)
		}
		q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(deletes)
		q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Times(0)
		uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
		uc.SetClock(func() time.Time { return now })
		return uc.EndExpiredTestDeploy(context.Background(), userID, deployID)
	}
	t.Run("expired", func(t *testing.T) {
		if err := run(t, &postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, ExpiresAt: now.Add(-time.Minute)}, 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("extended", func(t *testing.T) {
		if err := run(t, &postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, ExpiresAt: now.Add(time.Hour)}, 0); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if err := run(t, nil, 0); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCleanupExpiredTestDeploys_StaticAndLabWithNilInfra(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	userID := uuid.Must(uuid.NewV7())
	static := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), CreatedBy: userID, ExpiresAt: time.Now().Add(-time.Hour)}
	lab := postgres.ExerciseTestDeployment{ID: uuid.Must(uuid.NewV7()), CreatedBy: userID, GroupName: "tu-x", LabName: "l-1", ExpiresAt: time.Now().Add(-time.Hour)}
	q.EXPECT().ListExpiredExerciseTestDeploys(gomock.Any(), gomock.Any()).Return([]postgres.ExerciseTestDeployment{static, lab}, nil)
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a postgres.GetOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		if a.ID == static.ID {
			return static, nil
		}
		return lab, nil
	}).Times(2)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(1)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	err := uc.CleanupExpiredTestDeploys(context.Background())
	if err == nil || !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("want the lab item to report unavailable infrastructure, got %v", err)
	}
}

func TestEndTestLab_LabRowWhoseVariantHasNoDevicesStillTearsDownTheLab(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, GroupName: "tu-x", LabName: "l-1", ExpiresAt: time.Now().Add(-time.Minute)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), userID).Return([]postgres.ExerciseTestDeployment{row}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: &fakeVPNStore{}})
	if err := uc.EndExpiredTestDeploy(context.Background(), userID, deployID); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "tu-x" {
		t.Fatalf("the lab must be torn down, destroyed=%q", infra.destroyed)
	}
}

func TestCheckTestDeployRemoved_StaticDropsTheRow(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, ExpiresAt: now.Add(-time.Minute)}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(1)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Times(0)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	uc.SetClock(func() time.Time { return now })
	if err := uc.CheckTestDeployRemoved(context.Background(), userID, deployID, 1); err != nil {
		t.Fatal(err)
	}
}
