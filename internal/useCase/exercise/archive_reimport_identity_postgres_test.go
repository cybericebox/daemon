package exercise_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// An import into an existing exercise keeps the identity of what it matches, replaces the draft, answers the dry run
// and the apply with the same report, and changes nothing before it is applied.
func TestReimportKeepsIdentityReportsUsageAndDryRunWritesNothing(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := userModel.NewIncompleteUser(
		uuid.Must(uuid.NewV7()),
		"identity-import@test.test",
		importJobT0,
	)
	user.Role = rbac.RoleSuperAdmin
	_, err := userRepo.New(db.Queries).Create(ctx, user)
	require.NoError(t, err)
	repo := exerciseRepo.New(db.Queries)
	entity, err := exerciseModel.NewExercise("Identity target", "", nil, user.ID, importJobT0)
	require.NoError(t, err)
	entity, err = repo.Create(ctx, entity)
	require.NoError(t, err)

	variantID, webID, oldID, serverID, dbID := tid(1), tid(10), tid(11), tid(50), tid(51)
	stored := exerciseModel.ExerciseVersion{
		Name: entity.Name,
		Variants: []exerciseModel.Variant{
			mvariantExt(
				variantID,
				[]exerciseModel.Task{
					{ID: webID, Key: "web", Name: "Web task"},
					{ID: oldID, Key: "old", Name: "Old task"},
				},
				exerciseModel.Device{ID: serverID, Name: "srv", Type: exerciseModel.DeviceTypeNode},
				exerciseModel.Device{ID: dbID, Name: "db", Type: exerciseModel.DeviceTypeNode},
			),
		},
	}
	_, err = repo.UpsertDraft(
		ctx,
		entity.ID,
		uuid.Must(uuid.NewV7()),
		stored,
		importJobT0,
		uuid.NullUUID{},
	)
	require.NoError(t, err)
	published, err := repo.Publish(ctx, entity.ID, importJobT0)
	require.NoError(t, err)
	// A working draft that carries the same identities, as after the first edit of a published task.
	_, err = repo.UpsertDraft(
		ctx,
		entity.ID,
		uuid.Must(uuid.NewV7()),
		stored,
		importJobT0,
		uuid.NullUUID{},
	)
	require.NoError(t, err)
	event, err := eventModel.NewEvent(
		"identityev",
		"Identity event",
		importJobT0,
		importJobT0.Add(24*3600*1e9),
		user.ID,
		importJobT0,
	)
	require.NoError(t, err)
	event, err = eventRepo.New(db.Queries).Create(ctx, event)
	require.NoError(t, err)
	_, err = db.Queries.CreateEventExercise(
		ctx,
		postgres.CreateEventExerciseParams{
			ID:                uuid.Must(uuid.NewV7()),
			EventID:           event.ID,
			ExerciseID:        entity.ID,
			ExerciseVersionID: published.ID,
			Revision:          1,
			CreatedAt:         importJobT0,
		},
	)
	require.NoError(t, err)

	document := `{"schema":"cib-exercise/1.0","exercise":{"id":"` + entity.ID.String() + `","name":"Identity target"},"versions":[{"variants":[{"id":"` + variantID.String() + `",
		"tasks":[{"id":"` + webID.String() + `","key":"front","name":"Web task renamed"},{"name":"Brand new task","key":"fresh"}],
		"topology":{"devices":[{"name":"srv","type":"node"},{"name":"database","type":"node"}]}}]}]}`
	archive, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(document)})
	require.NoError(t, err)
	sources := newPostgresSources(t, db)
	sources.stage(archiveUpID, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := exercise.NewExerciseUseCase(
		exercise.Dependencies{
			Repo:  db.Queries,
			Media: postgresArchiveMedia{newFakeMedia(), db.Queries},
			CatalogUoW: postgres.NewUnitOfWorker[exercise.CatalogRepository](
				postgres.NewUoWFactory(db.Pool),
			),
		},
	)
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: user.ID, Role: rbac.RoleSuperAdmin}
	in := exercise.PreviewArchiveInput{
		UploadID: archiveUpID,
		Targets:  []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: entity.ID}},
	}

	draftBefore, err := repo.GetDraft(ctx, entity.ID)
	require.NoError(t, err)
	preview, err := uc.PreviewArchiveImport(ctx, actor, in)
	require.NoError(t, err)
	draftAfterDryRun, err := repo.GetDraft(ctx, entity.ID)
	require.NoError(t, err)
	require.Equal(t, draftBefore, draftAfterDryRun, "the dry run changes nothing")

	require.Len(t, preview.Items, 1)
	report := preview.Items[0].Report
	require.Len(t, report.Diff.Variants, 1)
	variant := report.Diff.Variants[0]
	require.Equal(t, exerciseModel.ImportVariantKept, variant.Status)
	require.Equal(t, exerciseModel.ImportMatchID, variant.MatchedBy)
	require.Equal(t, []string{"Web task renamed"}, names(variant.Tasks.Kept))
	require.Equal(t, "Web task", variant.Tasks.Kept[0].PreviousName)
	require.Equal(t, []string{"Brand new task"}, names(variant.Tasks.Added))
	require.Equal(t, []string{"Old task"}, names(variant.Tasks.Removed))
	require.Equal(t, []string{"srv"}, names(variant.Devices.Kept))
	require.Equal(t, []string{"database"}, names(variant.Devices.Added))
	require.Equal(t, []string{"db"}, names(variant.Devices.Removed))
	require.NotEmpty(t, report.Issues, "a task without answer or description cannot be published")
	require.False(t, report.Publishable)
	for _, issue := range report.Issues {
		require.NotZero(t, issue.Code)
	}
	require.Len(t, report.Usage.Events, 1)
	used := report.Usage.Events[0]
	require.Equal(t, event.ID, used.EventID)
	require.True(t, used.VersionPublished)
	require.Equal(t, []string{"Old task"}, usageNames(used.Removed))
	require.Equal(t, []string{"Brand new task"}, usageNames(used.Added))
	require.False(t, used.Blocked, "no answers are recorded")
	codes := map[string]bool{}
	for _, warning := range report.Warnings {
		codes[warning.Code] = true
	}
	require.True(t, codes[exerciseModel.ImportWarnUsedByEvents])
	require.False(t, codes[exerciseModel.ImportWarnNoExerciseID])

	result, err := uc.CommitArchiveImport(
		ctx,
		actor,
		exercise.CommitArchiveInput{
			PreviewArchiveInput: in,
			ConfirmedUpdates: []exercise.ConfirmedImportUpdate{
				{ItemKey: ".", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, result.Updated)
	require.Len(t, result.Results, 1)
	require.NotNil(t, result.Results[0].Report)
	applied, err := json.Marshal(result.Results[0].Report.Diff)
	require.NoError(t, err)
	shown, err := json.Marshal(report.Diff)
	require.NoError(t, err)
	require.JSONEq(
		t,
		string(shown),
		string(applied),
		"the apply returns the report the dry run showed",
	)
	require.Equal(t, report.Publishable, result.Results[0].Report.Publishable)

	draft, err := repo.GetDraft(ctx, entity.ID)
	require.NoError(t, err)
	require.Len(t, draft.Variants, 1)
	got := draft.Variants[0]
	require.Equal(t, variantID, got.ID)
	require.Len(t, got.Tasks, 2, "the draft is the document: the old task is gone")
	require.Equal(t, webID, got.Tasks[0].ID, "the matched task keeps its stored id")
	require.Equal(t, "web", got.Tasks[0].Key, "and its stored key")
	require.Equal(t, "Web task renamed", got.Tasks[0].Name)
	require.NotEqual(t, oldID, got.Tasks[1].ID)
	require.Equal(t, "Brand new task", got.Tasks[1].Name)
	require.Len(t, got.Topology.Devices, 2)
	require.Equal(
		t,
		serverID,
		got.Topology.Devices[0].ID,
		"the device with the same name keeps its id",
	)
	require.NotEqual(t, dbID, got.Topology.Devices[1].ID, "a renamed device is a new one")
	require.Equal(t, "database", got.Topology.Devices[1].Name)
	identity, err := repo.GetByID(ctx, entity.ID)
	require.NoError(t, err)
	require.Equal(
		t,
		published.ID,
		identity.PublishedVersionID.UUID,
		"the published version is untouched",
	)
}

func usageNames(tasks []exerciseModel.ImportUsageTask) []string {
	out := []string{}
	for _, task := range tasks {
		out = append(out, task.Name)
	}
	return out
}

func names(items []exerciseModel.ImportItem) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Name)
	}
	return out
}

func tid(n byte) uuid.UUID {
	return uuid.FromBytesOrNil([]byte{0, 0, 0, 0, 0, 0, 0x70, 0, 0x80, 0, 0, 0, 0, 0, 0, n})
}

func mvariantExt(
	id uuid.UUID,
	tasks []exerciseModel.Task,
	devices ...exerciseModel.Device,
) exerciseModel.Variant {
	return exerciseModel.Variant{
		ID:       id,
		Tasks:    tasks,
		Topology: exerciseModel.Topology{Devices: devices},
	}
}

// A document without an exercise id is never matched to an existing exercise (not by name either): the preview
// suggests a new exercise and says so; the apply creates one and returns the same warning.
func TestReimportDocumentWithoutExerciseIDIsNewWithWarning(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := userModel.NewIncompleteUser(
		uuid.Must(uuid.NewV7()),
		"noid-import@test.test",
		importJobT0,
	)
	user.Role = rbac.RoleSuperAdmin
	_, err := userRepo.New(db.Queries).Create(ctx, user)
	require.NoError(t, err)
	repo := exerciseRepo.New(db.Queries)
	entity, err := exerciseModel.NewExercise(
		"Same name as the document",
		"",
		nil,
		user.ID,
		importJobT0,
	)
	require.NoError(t, err)
	_, err = repo.Create(ctx, entity)
	require.NoError(t, err)

	archive, err := exercise.BuildArchiveV1(
		map[string][]byte{
			"exercise.json": []byte(
				`{"schema":"cib-exercise/1.0","exercise":{"name":"Same name as the document"},"versions":[{"variants":[]}]}`,
			),
		},
	)
	require.NoError(t, err)
	sources := newPostgresSources(t, db)
	sources.stage(archiveUpID, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := exercise.NewExerciseUseCase(
		exercise.Dependencies{
			Repo:  db.Queries,
			Media: postgresArchiveMedia{newFakeMedia(), db.Queries},
			CatalogUoW: postgres.NewUnitOfWorker[exercise.CatalogRepository](
				postgres.NewUoWFactory(db.Pool),
			),
		},
	)
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: user.ID, Role: rbac.RoleSuperAdmin}
	in := exercise.PreviewArchiveInput{UploadID: archiveUpID}

	preview, err := uc.PreviewArchiveImport(ctx, actor, in)
	require.NoError(t, err)
	require.Equal(t, "new", preview.Items[0].Target.Mode)
	require.Nil(t, preview.Items[0].TargetExercise)
	require.Contains(
		t,
		warningCodes(preview.Items[0].Report.Warnings),
		exerciseModel.ImportWarnNoExerciseID,
	)

	result, err := uc.CommitArchiveImport(
		ctx,
		actor,
		exercise.CommitArchiveInput{PreviewArchiveInput: in},
	)
	require.NoError(t, err)
	require.Equal(t, 1, result.Created)
	require.Equal(t, "new", result.Results[0].Mode)
	require.Contains(
		t,
		warningCodes(result.Results[0].Report.Warnings),
		exerciseModel.ImportWarnNoExerciseID,
	)
	require.NotEqual(t, entity.ID, result.Results[0].ExerciseID)
}

func warningCodes(warnings []exerciseModel.ImportWarning) []string {
	out := []string{}
	for _, warning := range warnings {
		out = append(out, warning.Code)
	}
	return out
}
