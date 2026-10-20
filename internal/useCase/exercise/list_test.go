package exercise_test

import (
	"context"
	"slices"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// TestListExercisesFor_AdminGetsStatusAndAccessEvents: the page carries the
// SQL-derived status, the owner event reference, and the selected events of
// "selected" catalog exercises in ONE batched query.
func TestListExercisesFor_AdminGetsStatusAndAccessEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	eventA, eventB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	selected := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), AccessLevel: 1}
	open := postgres.Exercise{ID: uuid.Must(uuid.NewV7())}
	owned := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Scope: 1, OwnerEventID: uuid.NullUUID{UUID: eventA, Valid: true}}

	q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListExercisesPageParams) ([]postgres.ListExercisesPageRow, error) {
			if !slices.Equal(arg.Statuses, []string{"changed"}) || arg.Archived != "exclude" || arg.ViewerID.Valid || len(arg.EventIds) != 2 {
				t.Fatalf("page params: %+v", arg)
			}
			return []postgres.ListExercisesPageRow{{Exercise: selected, Status: "changed"}, {Exercise: open, Status: "changed"}, {Exercise: owned, Status: "changed"}}, nil
		})
	q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).Return(int64(3), nil)
	q.EXPECT().ListExerciseCardExtras(gomock.Any(), gomock.Any()).Return([]postgres.ListExerciseCardExtrasRow{{ID: owned.ID, OwnerEventName: "Event A"}}, nil)
	q.EXPECT().ListUserEventMemberships(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListVisibleVariantDevices(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListExercisesEventAccess(gomock.Any(), []uuid.UUID{selected.ID}).Return([]postgres.ListExercisesEventAccessRow{
		{ExerciseID: selected.ID, EventID: eventA, EventName: "Event A"}, {ExerciseID: selected.ID, EventID: eventB, EventName: "Event B"},
	}, nil)

	res, err := uc.ListExercisesFor(context.Background(), exercise.Actor{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleSuperAdmin},
		exercise.ExercisesFilter{Page: 1, PageSize: 20, Status: "changed", EventIDs: []uuid.UUID{eventA, eventB}})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Exercises
	if got[0].Status != "changed" || len(got[0].AccessEvents) != 2 || got[0].AccessEvents[1].Name != "Event B" || len(got[0].AccessEventIDs) != 2 {
		t.Fatalf("selected exercise: %+v", got[0])
	}
	if len(got[1].AccessEvents) != 0 || got[1].OwnerEvent != nil {
		t.Fatalf("open catalog exercise: %+v", got[1])
	}
	if got[2].OwnerEvent == nil || got[2].OwnerEvent.ID != eventA || got[2].OwnerEvent.Name != "Event A" {
		t.Fatalf("owned exercise: %+v", got[2])
	}
}

// TestListExercisesFor_ManagerSeesNoCatalogWorkingCopy: a manager lists as a
// viewer, never learns the selected events, and a catalog row they read
// published-only does not reveal that a working copy exists.
func TestListExercisesFor_ManagerSeesNoCatalogWorkingCopy(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	managerID, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	some := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	catalog := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), AccessLevel: 1, DraftVersionID: some, PublishedVersionID: some}
	owned := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Scope: 1, OwnerEventID: uuid.NullUUID{UUID: eventID, Valid: true}, DraftVersionID: some}

	q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListExercisesPageParams) ([]postgres.ListExercisesPageRow, error) {
			if !arg.ViewerID.Valid || arg.ViewerID.UUID != managerID {
				t.Fatalf("a manager lists as a viewer: %+v", arg)
			}
			return []postgres.ListExercisesPageRow{{Exercise: catalog, Status: "published"}, {Exercise: owned, Status: "draft_only"}}, nil
		})
	q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).Return(int64(2), nil)
	q.EXPECT().ListExerciseCardExtras(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListVisibleVariantDevices(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListUserEventMemberships(gomock.Any(), managerID).Return([]postgres.ListUserEventMembershipsRow{{EventID: eventID, Role: 1}}, nil)

	res, err := uc.ListExercisesFor(context.Background(), exercise.Actor{UserID: managerID, Role: rbac.RoleUser}, exercise.ExercisesFilter{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Exercises[0]; c.HasDraft || c.Status != "published" || len(c.AccessEvents) != 0 {
		t.Fatalf("catalog row for a manager: %+v", c)
	}
	if o := res.Exercises[1]; !o.HasDraft || o.Status != "draft_only" {
		t.Fatalf("own event row: %+v", o)
	}
}

func TestListExercises_StatusArchivedListsArchived(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListExercisesPageParams) ([]postgres.ListExercisesPageRow, error) {
			if !slices.Equal(arg.Statuses, []string{"archived"}) || arg.Archived != "only" {
				t.Fatalf("status=archived implies archived=only: %+v", arg)
			}
			return nil, nil
		})
	q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	if _, err := uc.ListExercises(context.Background(), exercise.ExercisesFilter{Page: 1, Status: "archived"}); err != nil {
		t.Fatal(err)
	}

	q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListExercisesPageParams) ([]postgres.ListExercisesPageRow, error) {
			if len(arg.Statuses) != 0 || arg.Archived != "exclude" {
				t.Fatalf("an unknown status is ignored: %+v", arg)
			}
			return nil, nil
		})
	q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	if _, err := uc.ListExercises(context.Background(), exercise.ExercisesFilter{Page: 1, Status: "draft"}); err != nil {
		t.Fatal(err)
	}
}

func TestListExerciseTags_TopTagsAndViewer(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	managerID := uuid.Must(uuid.NewV7())

	q.EXPECT().ListExerciseTags(gomock.Any(), postgres.ListExerciseTagsParams{Prefix: "", LimitVal: 50}).
		Return([]postgres.ListExerciseTagsRow{{Tag: "web", ExerciseCount: 4}}, nil)
	items, err := uc.ListExerciseTags(context.Background(), exercise.Actor{Role: rbac.RoleSuperAdmin}, "  ", 0)
	if err != nil || len(items) != 1 || items[0].Tag != "web" || items[0].Count != 4 {
		t.Fatalf("an empty prefix returns the most used tags: %+v err=%v", items, err)
	}

	q.EXPECT().ListExerciseTags(gomock.Any(), postgres.ListExerciseTagsParams{Prefix: "cr", LimitVal: 200,
		ViewerID: uuid.NullUUID{UUID: managerID, Valid: true}}).Return(nil, nil)
	if _, err = uc.ListExerciseTags(context.Background(), exercise.Actor{UserID: managerID, Role: rbac.RoleUser}, "Cr", 1000); err != nil {
		t.Fatal(err)
	}
}

// Every list item carries the total resources of its published version (min and max over the variants).
func TestListExercisesFor_ItemsCarryTheTotalResourcesOfThePublishedVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	published := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	with := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), PublishedVersionID: published}
	without := postgres.Exercise{ID: uuid.Must(uuid.NewV7())}

	q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).Return([]postgres.ListExercisesPageRow{{Exercise: with, Status: "published"}, {Exercise: without, Status: "draft_only"}}, nil)
	q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).Return(int64(2), nil)
	q.EXPECT().ListExerciseCardExtras(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListUserEventMemberships(gomock.Any(), gomock.Any()).Return(nil, nil)
	small, large := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	q.EXPECT().ListVisibleVariantDevices(gomock.Any(), gomock.Any()).Return([]postgres.ListVisibleVariantDevicesRow{{
		ExerciseID: with.ID,
		Variants: []byte(`[{"id":"` + small + `","topology":{"devices":[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"web","type":"container","resource_preset":null,"resources":null}]}},
			{"id":"` + large + `","topology":{"devices":[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"web","type":"container","resource_preset":"large"},
			{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"sw","type":"unmanaged-switch"}]}}]`),
	}}, nil)

	res, err := uc.ListExercisesFor(context.Background(), exercise.Actor{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleSuperAdmin}, exercise.ExercisesFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Exercises[0].Resources
	if got.Min.Devices != 1 || got.Min.CPUMillicores != 15 || got.Min.MemoryBytes != 64<<20 {
		t.Fatalf("min = %+v: a device with nothing is the default preset", got.Min)
	}
	if got.Max.Devices != 1 || got.Max.CPUMillicores != 250 || got.Max.MemoryBytes != 1<<30 {
		t.Fatalf("max = %+v: the large preset; the switch runs no pod", got.Max)
	}
	if res.Exercises[0].ResourceHeavy {
		t.Fatal("a task inside the frame is not resource-heavy")
	}
	if z := res.Exercises[1].Resources; z.Max.Devices != 0 || z.Min.CPUMillicores != 0 {
		t.Fatalf("an exercise without a published version needs nothing: %+v", z)
	}
}

// TestListExercises_TableFilters: the catalog table filters reach the query as given: several statuses (archived
// with others lists both), tags with all of / none of (lower-cased), authors, the difficulty range (out of range
// bounds are no bound), the new sorts; the page rows carry the author name and the average difficulty (nil when
// the task has none).
func TestListExercises_TableFilters(t *testing.T) {
	author := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name         string
		filter       exercise.ExercisesFilter
		wantStatuses []string
		wantArchived string
		wantMin      int32
		wantMax      int32
		wantSort     string
	}{
		{name: "several statuses", filter: exercise.ExercisesFilter{Statuses: []string{"published", "changed", "bogus", "changed"}},
			wantStatuses: []string{"published", "changed"}, wantArchived: "exclude", wantSort: "updated"},
		{name: "archived with others lists both", filter: exercise.ExercisesFilter{Statuses: []string{"archived", "draft_only"}},
			wantStatuses: []string{"archived", "draft_only"}, wantArchived: "any", wantSort: "updated"},
		{name: "archived alone lists only archived", filter: exercise.ExercisesFilter{Statuses: []string{"archived"}},
			wantStatuses: []string{"archived"}, wantArchived: "only", wantSort: "updated"},
		{name: "difficulty range and sort", filter: exercise.ExercisesFilter{DifficultyMin: 2, DifficultyMax: 4, SortBy: "difficulty"},
			wantStatuses: []string{}, wantArchived: "exclude", wantMin: 2, wantMax: 4, wantSort: "difficulty"},
		{name: "out of range bounds are none", filter: exercise.ExercisesFilter{DifficultyMin: -1, DifficultyMax: 7, SortBy: "author"},
			wantStatuses: []string{}, wantArchived: "exclude", wantSort: "author"},
		{name: "unknown sort", filter: exercise.ExercisesFilter{SortBy: "flag"}, wantStatuses: []string{}, wantArchived: "exclude", wantSort: "updated"},
		{name: "subtasks sort", filter: exercise.ExercisesFilter{SortBy: "subtasks"}, wantStatuses: []string{}, wantArchived: "exclude", wantSort: "subtasks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
			tc.filter.Page = 1
			tc.filter.TagsAll, tc.filter.TagsNone, tc.filter.AuthorIDs = []string{" Web ", ""}, []string{"Legacy"}, []uuid.UUID{author}
			check := func(statuses, tagsAll, tagsNone []string, authors []uuid.UUID, archived string, minLevel, maxLevel int32) {
				t.Helper()
				if !slices.Equal(statuses, tc.wantStatuses) || archived != tc.wantArchived || minLevel != tc.wantMin || maxLevel != tc.wantMax {
					t.Fatalf("statuses=%v archived=%q min=%d max=%d", statuses, archived, minLevel, maxLevel)
				}
				if !slices.Equal(tagsAll, []string{"web"}) || !slices.Equal(tagsNone, []string{"legacy"}) || !slices.Equal(authors, []uuid.UUID{author}) {
					t.Fatalf("tags all=%v none=%v authors=%v", tagsAll, tagsNone, authors)
				}
			}
			withLevel, without := postgres.Exercise{ID: uuid.Must(uuid.NewV7())}, postgres.Exercise{ID: uuid.Must(uuid.NewV7())}
			q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.ListExercisesPageParams) ([]postgres.ListExercisesPageRow, error) {
					check(arg.Statuses, arg.TagsAll, arg.TagsNone, arg.AuthorIds, arg.Archived, arg.DifficultyMin, arg.DifficultyMax)
					if arg.SortBy != tc.wantSort {
						t.Fatalf("sort %q", arg.SortBy)
					}
					return []postgres.ListExercisesPageRow{
						{Exercise: withLevel, Status: "published", AuthorName: "Ada Lovelace", HasAvgDifficulty: true, AvgDifficulty: 3.5, HasTaskCount: true, TaskCount: 4},
						{Exercise: without, Status: "published"},
					}, nil
				})
			q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.CountExercisesPageParams) (int64, error) {
					check(arg.Statuses, arg.TagsAll, arg.TagsNone, arg.AuthorIds, arg.Archived, arg.DifficultyMin, arg.DifficultyMax)
					return 2, nil
				})
			res, err := uc.ListExercises(context.Background(), tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			first, second := res.Exercises[0], res.Exercises[1]
			if first.AuthorName != "Ada Lovelace" || first.AvgDifficulty == nil || *first.AvgDifficulty != 3.5 || first.TaskCount == nil || *first.TaskCount != 4 {
				t.Fatalf("row with a difficulty: %+v", first)
			}
			if second.AvgDifficulty != nil || second.AuthorName != "" || second.TaskCount != nil {
				t.Fatalf("row without a difficulty: %+v", second)
			}
		})
	}
}

// TestListExerciseAuthors_ViewerAndLimit: platform readers see every author; others only of what they may read.
func TestListExerciseAuthors_ViewerAndLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	managerID, authorID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	q.EXPECT().ListExerciseAuthors(gomock.Any(), postgres.ListExerciseAuthorsParams{Search: "ada", Ids: []uuid.UUID{authorID}, LimitVal: 50}).
		Return([]postgres.ListExerciseAuthorsRow{{ID: authorID, Name: "Ada Lovelace", ExerciseCount: 3}}, nil)
	items, err := uc.ListExerciseAuthors(context.Background(), exercise.Actor{Role: rbac.RoleAdminViewer}, " ada ", []uuid.UUID{authorID}, 0)
	if err != nil || len(items) != 1 || items[0].ID != authorID || items[0].Name != "Ada Lovelace" || items[0].Count != 3 {
		t.Fatalf("items=%+v err=%v", items, err)
	}

	q.EXPECT().ListExerciseAuthors(gomock.Any(), postgres.ListExerciseAuthorsParams{LimitVal: 200, Ids: []uuid.UUID{},
		ViewerID: uuid.NullUUID{UUID: managerID, Valid: true}}).Return(nil, nil)
	if _, err = uc.ListExerciseAuthors(context.Background(), exercise.Actor{UserID: managerID, Role: rbac.RoleUser}, "", nil, 1000); err != nil {
		t.Fatal(err)
	}
}
