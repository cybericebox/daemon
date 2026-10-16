package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/gofrs/uuid"
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
	q, variantID, versionID, ownerID := staticFixture(t, []exerciseModel.Task{{ID: t1, Flag: []string{"ICE{fixed}"}}, {ID: t2}}, nil)
	var created postgres.CreateExerciseTestDeployParams
	expectStaticCreate(q, &created)
	gate := &fakeGate{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: nil})
	uc.SetTestLabGate(gate)
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	uc.SetClock(func() time.Time { return now })

	handle, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID)
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
	if !handle.Static || handle.Lab != "" || handle.VPNClient != "" || len(handle.Flags) != 2 {
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

	_, err := uc.DeployVariantTest(context.Background(), owner, versionID, variantID)
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
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err != nil {
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
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err != nil {
		t.Fatal(err)
	}
	if infra.deployCalls != 0 || len(infra.policies) != 0 || infra.ensured != 0 || infra.policyGroup != "" {
		t.Fatalf("the agent must not be touched: %+v", infra)
	}
}

func TestDeployVariantTest_EventPreviewStaticStillNoLab(t *testing.T) {
	q, variantID, versionID, ownerID := staticFixture(t, nil, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	_, err := uc.DeployEventPreviewVariantTest(context.Background(), uuid.Must(uuid.NewV7()), ownerID, versionID, variantID)
	if !errors.Is(err, exerciseModel.ErrTestDeployNoLab.Err()) {
		t.Fatalf("want ErrTestDeployNoLab, got %v", err)
	}
}

func TestDeployVariantTest_LabVariantWithNilInfra(t *testing.T) {
	q, variantID, versionID, ownerID := gateFixture(t)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: nil})
	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("want ErrInfrastructureUnavailable, got %v", err)
	}
}
