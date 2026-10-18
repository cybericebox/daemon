package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	preview "github.com/cybericebox/daemon/internal/model/participantPreview"
	event "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"
	"strings"
	"testing"
	"time"
)

type previewLabsFake struct {
	starts, ends int
	allocated    bool
	expiry       time.Time
	startErr     error
	deploy       uuid.UUID
	destroyErr   error
}

func (f *previewLabsFake) DeployEventPreviewVariantTest(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (exerciseModel.DeployHandle, error) {
	f.starts++
	return exerciseModel.DeployHandle{Group: f.deploy.String()}, f.startErr
}
func (f *previewLabsFake) DeployTestStatus(context.Context, uuid.UUID, uuid.UUID) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{Phase: "Ready", Ready: true}, nil
}
func (f *previewLabsFake) CheckTestFlag(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) (bool, error) {
	return true, nil
}
func (f *previewLabsFake) OpenTestDeployLink(context.Context, uuid.UUID, uuid.UUID, string, int32) (labaccess.Link, error) {
	return labaccess.Link{}, nil
}
func (f *previewLabsFake) DestroyDeployTest(context.Context, uuid.UUID, uuid.UUID) error {
	f.ends++
	return f.destroyErr
}

type previewFixture struct {
	q                                                                    *postgresMocks.MockQuerier
	uc                                                                   *event.EventUseCase
	labs                                                                 *previewLabsFake
	event, user, version, exercise, variant, selection, task, hint, file uuid.UUID
	selections                                                           []byte
	infrastructureAllowed                                                bool
	variants                                                             []byte
	row                                                                  *postgres.EventPreviewRun
	defaults                                                             []byte
}

func newPreviewFixture(t *testing.T, dynamic bool) *previewFixture {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	f := &previewFixture{q: q, labs: &previewLabsFake{deploy: uuid.Must(uuid.NewV7()), allocated: true}}
	ids := []*uuid.UUID{&f.event, &f.user, &f.version, &f.exercise, &f.variant, &f.task, &f.hint, &f.file}
	for _, id := range ids {
		*id = uuid.Must(uuid.NewV7())
	}
	f.selection = uuid.NewV5(uuid.NamespaceURL, "cybericebox:participant-preview:"+f.version.String()+":"+f.variant.String())
	topo := exerciseModel.Topology{}
	if dynamic {
		topo.Devices = []exerciseModel.Device{{Name: "target"}}
	}
	f.variants, _ = json.Marshal([]exerciseModel.Variant{{ID: f.variant, Tasks: []exerciseModel.Task{{ID: f.task, Name: "Task", Description: json.RawMessage(`{"root":{"children":[]}}`), Flag: []string{"ICE{secret}"}, Hints: []exerciseModel.Hint{{ID: f.hint, Text: "hidden-hint", Level: exerciseModel.HintLevelSteps}}, Attachments: []exerciseModel.AttachmentRef{{FileID: f.file, Name: "readme"}}}}, Topology: topo}})
	now := time.Now()
	f.selections, _ = json.Marshal([]preview.Selection{{ID: f.selection, ExerciseVersionID: f.version, VariantID: f.variant}})
	f.infrastructureAllowed = true
	f.defaults = []byte(`{"GroupName":"Preview","Selections":[]}`)
	q.EXPECT().GetPlatformSettingByKey(gomock.Any(), preview.SettingKey).DoAndReturn(func(context.Context, string) (postgres.AppSetting, error) {
		return postgres.AppSetting{Value: f.defaults}, nil
	}).AnyTimes()
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: f.event, UserID: f.user}).Return(postgres.EventManager{EventID: f.event, UserID: f.user, Role: 0}, nil).AnyTimes()
	q.EXPECT().GetEventPreviewSettings(gomock.Any(), f.event).DoAndReturn(func(context.Context, uuid.UUID) (postgres.GetEventPreviewSettingsRow, error) {
		return postgres.GetEventPreviewSettingsRow{Selections: f.selections}, nil
	}).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), f.version).DoAndReturn(func(context.Context, uuid.UUID) (postgres.ExerciseVersion, error) {
		return postgres.ExerciseVersion{ID: f.version, ExerciseID: f.exercise, Status: "published", PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: f.variants}, nil
	}).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), f.exercise).Return(postgres.Exercise{ID: f.exercise, Name: "Catalog"}, nil).AnyTimes()
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), postgres.IsExerciseAvailableToEventParams{EventID: f.event, ExerciseID: f.exercise}).Return(true, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), f.event).DoAndReturn(func(context.Context, uuid.UUID) (postgres.Event, error) {
		return postgres.Event{ID: f.event, InfrastructureAllowed: f.infrastructureAllowed}, nil
	}).AnyTimes()
	q.EXPECT().LockEventPreviewOwner(gomock.Any(), f.event.String()+":"+f.user.String()).Return(nil).AnyTimes()
	q.EXPECT().GetEventPreviewRun(gomock.Any(), postgres.GetEventPreviewRunParams{EventID: f.event, UserID: f.user, SelectionID: f.selection}).DoAndReturn(func(context.Context, postgres.GetEventPreviewRunParams) (postgres.EventPreviewRun, error) {
		if f.row == nil {
			return postgres.EventPreviewRun{}, pgx.ErrNoRows
		}
		return *f.row, nil
	}).AnyTimes()
	q.EXPECT().UpsertEventPreviewRun(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpsertEventPreviewRunParams) error {
		f.row = &postgres.EventPreviewRun{ID: p.ID, EventID: p.EventID, UserID: p.UserID, SelectionID: p.SelectionID, DeployID: p.DeployID, State: p.State, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt}
		return nil
	}).AnyTimes()
	f.uc = event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, PreviewLabs: f.labs})
	return f
}
func TestPreviewStaticNeverDeploysAndOwnProgress(t *testing.T) {
	f := newPreviewFixture(t, false)
	ctx := context.Background()
	result, err := f.uc.SubmitPreviewFlag(ctx, f.event, f.user, f.selection, f.task, "wrong")
	if err != nil || result.Correct {
		t.Fatalf("wrong: %+v %v", result, err)
	}
	result, err = f.uc.SubmitPreviewFlag(ctx, f.event, f.user, f.selection, f.task, "ICE{secret}")
	if err != nil || !result.Correct || !result.FirstSolve {
		t.Fatalf("correct: %+v %v", result, err)
	}
	result, err = f.uc.SubmitPreviewFlag(ctx, f.event, f.user, f.selection, f.task, "ICE{secret}")
	if err != nil || result.FirstSolve {
		t.Fatal("duplicate solve")
	}
	board, err := f.uc.PreviewBoard(ctx, f.event, f.user)
	if err != nil {
		t.Fatal(err)
	}
	b := board.Challenges[0]
	if b.PreviewAttempts != 3 || b.PreviewCorrectAttempts != 2 || b.SolvedAt == nil {
		t.Fatalf("progress %+v", b)
	}
	if f.labs.starts != 0 {
		t.Fatal("static consumed lab")
	}
	data, _ := json.Marshal(board)
	if strings.Contains(string(data), "ICE{secret}") || strings.Contains(string(data), "hidden-hint") {
		t.Fatal("secret projection")
	}
	hint, err := f.uc.UnlockPreviewHint(ctx, f.event, f.user, f.selection, f.task, f.hint)
	if err != nil || hint.Content == nil || *hint.Content != "hidden-hint" {
		t.Fatalf("hint %+v %v", hint, err)
	}
	solves, err := f.uc.PreviewSolves(ctx, f.event, f.user, f.selection, f.task)
	if err != nil || solves.Total != 1 || !solves.Items[0].Own {
		t.Fatalf("solves %+v %v", solves, err)
	}
}
func TestPreviewDynamicStartIsIdempotentAndQuotaErrorsPropagate(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "idempotent", true: "quota"}[refuse], func(t *testing.T) {
			f := newPreviewFixture(t, true)
			ctx := context.Background()
			quota := errors.New("quota refused")
			if refuse {
				f.labs.startErr = quota
			}
			first, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection)
			if refuse {
				if !errors.Is(err, quota) || f.row != nil {
					t.Fatalf("quota %v", err)
				}
				return
			}
			second, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection)
			if err != nil || first.ID != second.ID || f.labs.starts != 1 {
				t.Fatalf("duplicate %+v %+v %v", first, second, err)
			}
		})
	}
}
func TestPreviewDynamicSubmitRequiresExplicitStart(t *testing.T) {
	f := newPreviewFixture(t, true)
	_, err := f.uc.SubmitPreviewFlag(context.Background(), f.event, f.user, f.selection, f.task, "ICE{secret}")
	if err == nil || f.labs.starts != 0 {
		t.Fatal("implicit dynamic deployment")
	}
}
func TestPreviewRejectsUnselectedTaskHintAndFile(t *testing.T) {
	f := newPreviewFixture(t, false)
	ctx := context.Background()
	other := uuid.Must(uuid.NewV7())
	if _, err := f.uc.StartPreviewRun(ctx, f.event, f.user, other); err == nil {
		t.Fatal("unknown selection accepted")
	}
	if _, err := f.uc.SubmitPreviewFlag(ctx, f.event, f.user, f.selection, other, "ICE{secret}"); err == nil {
		t.Fatal("foreign task accepted")
	}
	if _, err := f.uc.UnlockPreviewHint(ctx, f.event, f.user, f.selection, f.task, other); err == nil {
		t.Fatal("foreign hint accepted")
	}
	if _, _, err := f.uc.StreamPreviewFile(ctx, f.event, f.user, f.selection, f.task, other); err == nil {
		t.Fatal("foreign file accepted")
	}
}
func TestPreviewForbidsViewerAndOtherEvent(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: user}).Return(postgres.EventManager{Role: 2}, nil)
	if _, err := uc.PreviewBoard(context.Background(), eventID, user); err == nil {
		t.Fatal("viewer accepted")
	}
	other := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: other, UserID: user}).Return(postgres.EventManager{}, pgx.ErrNoRows)
	if _, err := uc.StartPreviewRun(context.Background(), other, user, uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("other event accepted")
	}
}
func TestPreviewExpiredDynamicRunRejectedOnStatus(t *testing.T) {
	f := newPreviewFixture(t, true)
	_, err := f.uc.StartPreviewRun(context.Background(), f.event, f.user, f.selection)
	if err != nil {
		t.Fatal(err)
	}
	f.row.ExpiresAt = time.Now().Add(-time.Second)
	if _, err = f.uc.PreviewLabStatus(context.Background(), f.event, f.user, f.selection); err == nil {
		t.Fatal("expired status accepted")
	}
}
func TestPreviewGlobalRejectsPrivateAndUnpublishedCatalogVersions(t *testing.T) {
	for _, tc := range []struct {
		name, status  string
		scope, access int16
	}{{"private", "published", 0, 1}, {"event", "published", 1, 0}, {"draft", "draft", 0, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			uc := event.NewEventUseCase(event.Dependencies{Repo: q})
			version, exercise, variant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			data, _ := json.Marshal([]exerciseModel.Variant{{ID: variant}})
			q.EXPECT().GetExerciseVersionByID(gomock.Any(), version).Return(postgres.ExerciseVersion{ID: version, ExerciseID: exercise, Status: tc.status, PublishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, Variants: data}, nil)
			q.EXPECT().GetExerciseByID(gomock.Any(), exercise).Return(postgres.Exercise{ID: exercise, Scope: tc.scope, AccessLevel: tc.access}, nil)
			if _, err := uc.PutPreviewDefaults(context.Background(), event.PreviewSettingsInput{Selections: []event.PreviewSelection{{ExerciseVersionID: version, VariantID: variant}}}); err == nil {
				t.Fatal("unsafe default accepted")
			}
		})
	}
}
func TestPreviewInheritanceAndExplicitEmptyDiffer(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ctx := context.Background()
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{Role: 0}, nil).AnyTimes()
	q.EXPECT().GetEventPreviewSettings(gomock.Any(), eventID).Return(postgres.GetEventPreviewSettingsRow{}, pgx.ErrNoRows)
	q.EXPECT().GetPlatformSettingByKey(gomock.Any(), preview.SettingKey).Return(postgres.AppSetting{}, pgx.ErrNoRows).AnyTimes()
	inherited, err := uc.GetPreviewSettings(ctx, eventID, user)
	if err != nil || !inherited.Inherit {
		t.Fatalf("inherit %+v %v", inherited, err)
	}
	q.EXPECT().GetEventPreviewSettings(gomock.Any(), eventID).Return(postgres.GetEventPreviewSettingsRow{Selections: []byte(`[]`)}, nil)
	empty, err := uc.GetPreviewSettings(ctx, eventID, user)
	if err != nil || empty.Inherit || len(empty.EffectiveSelections) != 0 {
		t.Fatalf("empty %+v %v", empty, err)
	}
}

func (f *previewLabsFake) CanStartEventPreview(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	return f.startErr == nil, nil
}

type previewFailSave struct{}

func (previewFailSave) Save() error    { return errors.New("commit failed") }
func (previewFailSave) Restore() error { return nil }

type previewFailWorker struct{ repo event.IRepository }

func (w previewFailWorker) UnitOfWork(ctx context.Context) (context.Context, event.IRepository, postgres.UoW, error) {
	return ctx, w.repo, previewFailSave{}, nil
}
func TestPreviewRollbackCleansCreatedLaboratory(t *testing.T) {
	f := newPreviewFixture(t, true)
	f.uc = event.NewEventUseCase(event.Dependencies{Repo: f.q, UoW: previewFailWorker{repo: f.q}, PreviewLabs: f.labs})
	if _, err := f.uc.StartPreviewRun(context.Background(), f.event, f.user, f.selection); err == nil {
		t.Fatal("commit failure hidden")
	}
	if f.labs.starts != 1 || f.labs.ends != 1 {
		t.Fatalf("untracked deploy: starts=%d ends=%d", f.labs.starts, f.labs.ends)
	}
}
func TestPreviewResetCleansRemovedSelectionsAndOnlyOwnedRows(t *testing.T) {
	f := newPreviewFixture(t, true)
	ctx := context.Background()
	if _, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection); err != nil {
		t.Fatal(err)
	}
	removed := uuid.Must(uuid.NewV7())
	f.row.SelectionID = removed
	f.q.EXPECT().ListEventPreviewRuns(gomock.Any(), postgres.ListEventPreviewRunsParams{EventID: f.event, UserID: f.user}).Return([]postgres.EventPreviewRun{*f.row}, nil)
	f.q.EXPECT().DeleteEventPreviewRun(gomock.Any(), postgres.DeleteEventPreviewRunParams{EventID: f.event, UserID: f.user, SelectionID: removed}).Return(nil)
	if err := f.uc.EndPreviewRuns(ctx, f.event, f.user); err != nil {
		t.Fatal(err)
	}
	if f.labs.ends != 1 {
		t.Fatal("removed selection laboratory not cleaned")
	}
}
func TestPreviewStaticVariantSurvivesDynamicSiblingAndDisabledEventInfrastructure(t *testing.T) {
	f := newPreviewFixture(t, false)
	var variants []exerciseModel.Variant
	_ = json.Unmarshal(f.variants, &variants)
	variants = append([]exerciseModel.Variant{{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "dynamic"}}}}}, variants...)
	f.variants, _ = json.Marshal(variants)
	f.infrastructureAllowed = false
	result, err := f.uc.SubmitPreviewFlag(context.Background(), f.event, f.user, f.selection, f.task, "ICE{secret}")
	if err != nil || !result.Correct || f.labs.starts != 0 {
		t.Fatalf("static blocked %+v %v", result, err)
	}
}

func (f *previewLabsFake) EventPreviewAllocated(context.Context, uuid.UUID) (bool, error) {
	return f.allocated, nil
}
func TestPreviewNoEventAllocationOmitsDynamicButKeepsStatic(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		t.Run(map[bool]string{true: "dynamic", false: "static"}[dynamic], func(t *testing.T) {
			f := newPreviewFixture(t, dynamic)
			f.labs.allocated = false
			board, err := f.uc.PreviewBoard(context.Background(), f.event, f.user)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if dynamic {
				want = 0
			}
			if len(board.Challenges) != want {
				t.Fatalf("challenges %d want %d", len(board.Challenges), want)
			}
			if !dynamic && !board.Challenges[0].PreviewCanStart {
				t.Fatal("static blocked by missing allocation")
			}
		})
	}
}

func (f *previewLabsFake) ListTestDeploys(context.Context, uuid.UUID, uuid.UUID) ([]exerciseModel.TestDeploy, error) {
	expires := f.expiry
	if expires.IsZero() {
		expires = time.Now().Add(time.Hour)
	}
	return []exerciseModel.TestDeploy{{ID: f.deploy, ExpiresAt: expires}}, nil
}
func TestPreviewRunExpiryMatchesEarlierLaboratoryReservationEnd(t *testing.T) {
	f := newPreviewFixture(t, true)
	f.labs.expiry = time.Now().Add(20 * time.Minute)
	run, err := f.uc.StartPreviewRun(context.Background(), f.event, f.user, f.selection)
	if err != nil || !run.ExpiresAt.Equal(f.labs.expiry) {
		t.Fatalf("lease %+v %v", run, err)
	}
}
func TestPreviewRemovingSelectionBlocksOperationsButAllowsCleanup(t *testing.T) {
	f := newPreviewFixture(t, true)
	ctx := context.Background()
	if _, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection); err != nil {
		t.Fatal(err)
	}
	f.selections = []byte(`[]`)
	if _, err := f.uc.PreviewLabStatus(ctx, f.event, f.user, f.selection); err == nil {
		t.Fatal("removed lab accessible")
	}
	if _, err := f.uc.SubmitPreviewFlag(ctx, f.event, f.user, f.selection, f.task, "ICE{secret}"); err == nil {
		t.Fatal("removed flag accepted")
	}
	if _, err := f.uc.UnlockPreviewHint(ctx, f.event, f.user, f.selection, f.task, f.hint); err == nil {
		t.Fatal("removed hint accepted")
	}
	if _, _, err := f.uc.StreamPreviewFile(ctx, f.event, f.user, f.selection, f.task, f.file); err == nil {
		t.Fatal("removed file accessible")
	}
	if _, err := f.uc.PreviewProxyLink(ctx, f.event, f.user, f.selection, "target", 80); err == nil {
		t.Fatal("removed proxy accessible")
	}
	f.q.EXPECT().DeleteEventPreviewRun(gomock.Any(), postgres.DeleteEventPreviewRunParams{EventID: f.event, UserID: f.user, SelectionID: f.selection}).Return(nil)
	if err := f.uc.EndPreviewRun(ctx, f.event, f.user, f.selection); err != nil || f.labs.ends != 1 {
		t.Fatalf("removed cleanup %v", err)
	}
}
func TestPreviewOtherManagerCannotAccessOwnersRun(t *testing.T) {
	f := newPreviewFixture(t, true)
	ctx := context.Background()
	if _, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection); err != nil {
		t.Fatal(err)
	}
	other := uuid.Must(uuid.NewV7())
	f.q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: f.event, UserID: other}).Return(postgres.EventManager{Role: 0}, nil)
	f.q.EXPECT().GetEventPreviewRun(gomock.Any(), postgres.GetEventPreviewRunParams{EventID: f.event, UserID: other, SelectionID: f.selection}).Return(postgres.EventPreviewRun{}, pgx.ErrNoRows)
	if _, err := f.uc.PreviewLabStatus(ctx, f.event, other, f.selection); err == nil {
		t.Fatal("other manager borrowed run")
	}
}
func TestPreviewDisabledInfrastructureRefusesDynamicStartBeforeDeployment(t *testing.T) {
	f := newPreviewFixture(t, true)
	f.infrastructureAllowed = false
	if _, err := f.uc.StartPreviewRun(context.Background(), f.event, f.user, f.selection); err == nil || f.labs.starts != 0 {
		t.Fatalf("disabled dynamic start %v", err)
	}
}
func TestPreviewGroupNameIsIndependentOfSelectionInheritance(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	ctx := context.Background()
	eventID, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{Role: 0}, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID}, nil).AnyTimes()
	defaultName := "Platform exercises"
	q.EXPECT().GetPlatformSettingByKey(gomock.Any(), preview.SettingKey).DoAndReturn(func(context.Context, string) (postgres.AppSetting, error) {
		value, _ := json.Marshal(preview.Defaults{GroupName: defaultName, Selections: []preview.Selection{}})
		return postgres.AppSetting{Value: value}, nil
	}).AnyTimes()
	var stored *postgres.GetEventPreviewSettingsRow
	q.EXPECT().GetEventPreviewSettings(gomock.Any(), eventID).DoAndReturn(func(context.Context, uuid.UUID) (postgres.GetEventPreviewSettingsRow, error) {
		if stored == nil {
			return postgres.GetEventPreviewSettingsRow{}, pgx.ErrNoRows
		}
		return *stored, nil
	}).AnyTimes()
	q.EXPECT().UpsertEventPreviewSettings(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpsertEventPreviewSettingsParams) error {
		stored = &postgres.GetEventPreviewSettingsRow{Selections: p.Selections, GroupName: p.GroupName}
		return nil
	}).AnyTimes()
	ownName := "  My group  "
	view, err := uc.PutPreviewSettings(ctx, eventID, user, event.PreviewSettingsInput{Inherit: true, GroupName: &ownName})
	if err != nil || !view.Inherit || view.GroupName != "My group" || view.GroupNameOverride == nil || stored.Selections != nil {
		t.Fatalf("name-only override %+v %v", view, err)
	}
	defaultName = "Renamed platform group"
	view, err = uc.GetPreviewSettings(ctx, eventID, user)
	if err != nil || view.GroupName != "My group" {
		t.Fatalf("default overwrote rename %+v %v", view, err)
	}
	view, err = uc.PutPreviewSettings(ctx, eventID, user, event.PreviewSettingsInput{Inherit: true, GroupName: nil})
	if err != nil || view.GroupName != defaultName || view.GroupNameOverride != nil {
		t.Fatalf("restore name inheritance %+v %v", view, err)
	}
	view, err = uc.PutPreviewSettings(ctx, eventID, user, event.PreviewSettingsInput{Inherit: false, Selections: []event.PreviewSelection{}, GroupName: &ownName})
	if err != nil || view.Inherit || string(stored.Selections) != "[]" || view.GroupName != "My group" {
		t.Fatalf("explicit empty named group %+v %v", view, err)
	}
}
func TestPreviewGroupNameValidation(t *testing.T) {
	for _, name := range []string{"", "  ", "line\nbreak", strings.Repeat("я", 101)} {
		t.Run(name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			uc := event.NewEventUseCase(event.Dependencies{Repo: q})
			if _, err := uc.PutPreviewDefaults(context.Background(), event.PreviewSettingsInput{GroupName: &name}); err == nil {
				t.Fatal("invalid name persisted")
			}
		})
	}
}
func TestPreviewLegacyDefaultsArrayStillInherits(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	q.EXPECT().GetPlatformSettingByKey(gomock.Any(), preview.SettingKey).Return(postgres.AppSetting{Value: []byte(`[]`)}, nil)
	result, err := uc.GetPreviewDefaults(context.Background())
	if err != nil || len(result.EffectiveSelections) != 0 || result.GroupName != "" {
		t.Fatalf("legacy %+v %v", result, err)
	}
}

func TestPreviewExpiredEngineLeaseDoesNotBlockRestartOrReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{true: "reset", false: "restart"}[reset], func(t *testing.T) {
			f := newPreviewFixture(t, true)
			ctx := context.Background()
			first, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection)
			if err != nil {
				t.Fatal(err)
			}
			f.row.ExpiresAt = time.Now().Add(-time.Second)
			f.labs.destroyErr = exerciseModel.ErrTestDeployNotFound.Err()
			if reset {
				f.q.EXPECT().ListEventPreviewRuns(gomock.Any(), postgres.ListEventPreviewRunsParams{EventID: f.event, UserID: f.user}).Return([]postgres.EventPreviewRun{*f.row}, nil)
				f.q.EXPECT().DeleteEventPreviewRun(gomock.Any(), postgres.DeleteEventPreviewRunParams{EventID: f.event, UserID: f.user, SelectionID: f.selection}).Return(nil)
				if err = f.uc.EndPreviewRuns(ctx, f.event, f.user); err != nil {
					t.Fatalf("expired reset %v", err)
				}
			} else {
				second, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection)
				if err != nil || second.ID == first.ID || f.labs.starts != 2 {
					t.Fatalf("expired restart %+v %v", second, err)
				}
			}
		})
	}
}

func TestPreviewSettingsExposePlatformDefaultsSeparatelyFromCurrentSelection(t *testing.T) {
	f := newPreviewFixture(t, false)
	version, exercise, variant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	f.defaults, _ = json.Marshal(preview.Defaults{GroupName: "Platform group", Selections: []preview.Selection{{ExerciseVersionID: version, VariantID: variant}}})
	data, _ := json.Marshal([]exerciseModel.Variant{{ID: variant, Index: 7}})
	f.q.EXPECT().GetExerciseVersionByID(gomock.Any(), version).Return(postgres.ExerciseVersion{ID: version, ExerciseID: exercise, Status: "published", PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: data}, nil).AnyTimes()
	f.q.EXPECT().GetExerciseByID(gomock.Any(), exercise).Return(postgres.Exercise{ID: exercise, Name: "Default exercise"}, nil).AnyTimes()
	f.q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), postgres.IsExerciseAvailableToEventParams{EventID: f.event, ExerciseID: exercise}).Return(true, nil).AnyTimes()
	view, err := f.uc.GetPreviewSettings(context.Background(), f.event, f.user)
	if err != nil || view.Inherit || len(view.EffectiveSelections) != 1 || view.EffectiveSelections[0].ExerciseVersionID != f.version || len(view.DefaultSelections) != 1 || view.DefaultSelections[0].ExerciseVersionID != version || view.DefaultGroupName != "Platform group" || view.DefaultSelections[0].VariantPosition != 0 || view.DefaultSelections[0].VariantIndex != 7 {
		t.Fatalf("restore preview %+v %v", view, err)
	}
}

// The keyset query returns rows strictly before the cursor, so the first page
// must start from the "after everything" sentinel, not the zero value.
func TestPreviewGlobalCatalogStartsFromSentinelCursor(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	version, exercise, variant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	data, _ := json.Marshal([]exerciseModel.Variant{{ID: variant}})
	published := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.EXPECT().ListExercisesCursor(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.ListExercisesCursorParams) ([]postgres.ListExercisesCursorRow, error) {
		if p.Archived == "only" || !p.CursorCreatedAt.After(time.Date(9000, 1, 1, 0, 0, 0, 0, time.UTC)) || p.CursorID.String() != "ffffffff-ffff-ffff-ffff-ffffffffffff" {
			return nil, nil
		}
		return []postgres.ListExercisesCursorRow{{Exercise: postgres.Exercise{ID: exercise, Name: "Catalog", PublishedVersionID: uuid.NullUUID{UUID: version, Valid: true}}}}, nil
	}).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), version).Return(postgres.ExerciseVersion{ID: version, ExerciseID: exercise, Status: "published", PublishedAt: pgtype.Timestamptz{Time: published, Valid: true}, Variants: data}, nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), exercise).Return(postgres.Exercise{ID: exercise, Name: "Catalog"}, nil).AnyTimes()
	items, err := uc.ListPreviewCatalog(context.Background(), uuid.Nil, uuid.Must(uuid.NewV7()), "")
	if err != nil || len(items) != 1 {
		t.Fatalf("catalog = %v, err = %v", items, err)
	}
}

// Every matching task is listed: eligible first, the rest with the reason they cannot be chosen.
func TestPreviewGlobalCatalogListsIneligibleWithReasons(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	okEx, draftEx, restrictedEx, archivedEx, emptyEx := id(), id(), id(), id(), id()
	okVer, restrictedVer, emptyVer := id(), id(), id()
	variant := id()
	published := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := func(v uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: v, Valid: true} }
	q.EXPECT().ListExercisesCursor(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.ListExercisesCursorParams) ([]postgres.ListExercisesCursorRow, error) {
		if p.Archived == "only" {
			return []postgres.ListExercisesCursorRow{{Exercise: postgres.Exercise{ID: archivedEx, Name: "Archived", PublishedVersionID: valid(id())}}}, nil
		}
		return []postgres.ListExercisesCursorRow{
			{Exercise: postgres.Exercise{ID: draftEx, Name: "Draft"}},
			{Exercise: postgres.Exercise{ID: restrictedEx, Name: "Restricted", AccessLevel: 1, PublishedVersionID: valid(restrictedVer)}},
			{Exercise: postgres.Exercise{ID: emptyEx, Name: "Empty", PublishedVersionID: valid(emptyVer)}},
			{Exercise: postgres.Exercise{ID: okEx, Name: "Ok", PublishedVersionID: valid(okVer)}},
		}, nil
	}).AnyTimes()
	data, _ := json.Marshal([]exerciseModel.Variant{{ID: variant}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), okVer).Return(postgres.ExerciseVersion{ID: okVer, ExerciseID: okEx, Status: "published", PublishedAt: pgtype.Timestamptz{Time: published, Valid: true}, Variants: data}, nil).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), emptyVer).Return(postgres.ExerciseVersion{ID: emptyVer, ExerciseID: emptyEx, Status: "published", PublishedAt: pgtype.Timestamptz{Time: published, Valid: true}, Variants: []byte("[]")}, nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), okEx).Return(postgres.Exercise{ID: okEx, Name: "Ok"}, nil).AnyTimes()
	items, err := uc.ListPreviewCatalog(context.Background(), uuid.Nil, id(), "")
	if err != nil || len(items) != 5 {
		t.Fatalf("catalog = %v, err = %v", items, err)
	}
	want := []struct {
		name, reason string
		eligible     bool
	}{{"Ok", "", true}, {"Draft", event.PreviewReasonNotPublished, false}, {"Restricted", event.PreviewReasonRestrictedAccess, false}, {"Empty", event.PreviewReasonNoPreviewVariant, false}, {"Archived", event.PreviewReasonArchived, false}}
	got := map[string]event.PreviewCatalogItem{}
	for _, it := range items {
		got[it.Name] = it
	}
	for i, w := range want {
		it := got[w.name]
		if it.Eligible != w.eligible || it.Reason != w.reason || (i == 0 && len(it.Variants) != 1) {
			t.Fatalf("%s = %+v, want eligible=%v reason=%q", w.name, it, w.eligible, w.reason)
		}
	}
	if !items[0].Eligible || items[1].Eligible {
		t.Fatalf("eligible items must come first: %+v", items)
	}
}

// Event scope: a task whose infrastructure cannot start now stays listed with the no_capacity reason.
func TestPreviewEventCatalogMarksNoCapacity(t *testing.T) {
	f := newPreviewFixture(t, true)
	f.labs.startErr = errors.New("no capacity")
	f.q.EXPECT().ListEventCatalog(gomock.Any(), gomock.Any()).Return([]postgres.ListEventCatalogRow{{ID: f.exercise, Name: "Catalog", PublishedVersionID: f.version}}, nil)
	f.q.EXPECT().ListExercisesCursor(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	items, err := f.uc.ListPreviewCatalog(context.Background(), f.event, f.user, "")
	if err != nil || len(items) != 1 || items[0].Eligible || items[0].Reason != event.PreviewReasonNoCapacity {
		t.Fatalf("items = %+v, err = %v", items, err)
	}
	f.labs.startErr = nil
	f.q.EXPECT().ListEventCatalog(gomock.Any(), gomock.Any()).Return([]postgres.ListEventCatalogRow{{ID: f.exercise, Name: "Catalog", PublishedVersionID: f.version}}, nil)
	items, err = f.uc.ListPreviewCatalog(context.Background(), f.event, f.user, "")
	if err != nil || len(items) != 1 || !items[0].Eligible || items[0].Reason != "" {
		t.Fatalf("items = %+v, err = %v", items, err)
	}
}
