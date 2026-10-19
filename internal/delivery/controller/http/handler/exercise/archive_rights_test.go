package exercise_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
)

var (
	moderatedEvent = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000e1")
	foreignEvent   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000e2")
)

// engineAs is the router with the real PermSelf gate for role and the caller jobCaller.
func engineAs(uc *fakeUC, role rbac.Role) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: jobCaller, Role: role}))
		c.Next()
	})
	exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: role}).Init(r.Group("api"))
	return r
}

// An event moderator imports into the event they manage: the target reaches the use case. Into the catalog or
// another event: 403, nothing imported.
func TestImportUpload_ModeratorTargetsTheirEventOnly(t *testing.T) {
	uploadID := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name   string
		owner  string
		status int
	}{
		{name: "own event", owner: `"` + moderatedEvent.String() + `"`, status: http.StatusAccepted},
		{name: "catalog", owner: `null`, status: http.StatusForbidden},
		{name: "another event", owner: `"` + foreignEvent.String() + `"`, status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeUC{managedEvent: moderatedEvent, archiveImport: archiveImportCalls{start: exerciseUseCase.ArchiveImportStart{JobID: jobID}}}
			w := jsonRequest(engineAs(uc, rbac.RoleUser), http.MethodPost, "/api/exercises/import/upload",
				`{"UploadID":"`+uploadID.String()+`","OwnerEventID":`+tc.owner+`}`)
			if w.Code != tc.status {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if tc.status != http.StatusAccepted {
				if len(uc.archiveImport.uploaded) != 0 {
					t.Fatal("a refused import reached the use case")
				}
				return
			}
			if got := uc.archiveImport.uploaded; len(got) != 1 || got[0].OwnerEventID == nil || *got[0].OwnerEventID != moderatedEvent {
				t.Fatalf("import input %+v", got)
			}
		})
	}
}

// The multipart import refuses a user who may import nowhere before reading the archive, and passes the
// OwnerEventID form field on.
func TestImportMultipart_TargetAndRefusal(t *testing.T) {
	form := func(owner string) (*bytes.Buffer, string) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, _ := mw.CreateFormFile("archive", "task.zip")
		_, _ = part.Write([]byte("not read"))
		if owner != "" {
			_ = mw.WriteField("OwnerEventID", owner)
		}
		_ = mw.Close()
		return &body, mw.FormDataContentType()
	}
	t.Run("nobody's import is not read", func(t *testing.T) {
		uc := &fakeUC{}
		body, contentType := form(moderatedEvent.String())
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/exercises/import", body)
		req.Header.Set("Content-Type", contentType)
		engineAs(uc, rbac.RoleUser).ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || len(uc.importTargets) != 0 || uc.limitReads != 0 {
			t.Fatalf("status %d targets %v limit reads %d", w.Code, uc.importTargets, uc.limitReads)
		}
	})
	t.Run("a bad target is a bad request", func(t *testing.T) {
		uc := &fakeUC{managedEvent: moderatedEvent}
		body, contentType := form("not-a-uuid")
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/exercises/import", body)
		req.Header.Set("Content-Type", contentType)
		engineAs(uc, rbac.RoleUser).ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d body %s", w.Code, w.Body.String())
		}
	})
}

// Export: exercises.export holders export anything; a moderator only their event's tasks; one task outside
// refuses the whole selection before anything is exported; a plain user nothing.
func TestExport_PerRole(t *testing.T) {
	own, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	single, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		role   rbac.Role
		ids    []uuid.UUID
		status int
	}{
		{name: "admin exports any task", role: rbac.RoleAdmin, ids: []uuid.UUID{own, other}, status: http.StatusOK},
		{name: "moderator exports their event's task", role: rbac.RoleUser, ids: []uuid.UUID{own}, status: http.StatusOK},
		{name: "moderator cannot export another task", role: rbac.RoleUser, ids: []uuid.UUID{own, other}, status: http.StatusForbidden},
		{name: "admin viewer cannot export", role: rbac.RoleAdminViewer, ids: []uuid.UUID{other}, status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeUC{exportedArchive: single, exerciseView: exerciseUseCase.ExerciseView{Name: "Веб"}, chunkBytes: 128,
				exportable: map[uuid.UUID]bool{own: true}}
			body, _ := json.Marshal(map[string]any{"IDs": tc.ids})
			w := jsonRequest(engineAs(uc, tc.role), http.MethodPost, "/api/exercises/export", string(body))
			if w.Code != tc.status {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if tc.status == http.StatusForbidden && (len(uc.exportLimits) != 0 || strings.Contains(w.Body.String(), "FileID")) {
				t.Fatal("a refused export exported something")
			}
		})
	}
}

// The author filter suggestions: search and limit reach the use case; names only.
func TestListAuthors_Route(t *testing.T) {
	author := uuid.Must(uuid.NewV7())
	uc := &fakeUC{authors: []exerciseUseCase.ExerciseAuthorSuggestion{{ID: author, Name: "Ада Лавлейс", Count: 2}}}
	w := httptest.NewRecorder()
	engineAs(uc, rbac.RoleUser).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/authors?search=%D0%90%D0%B4%D0%B0&limit=20", nil))
	if w.Code != http.StatusOK || uc.authorSearch != "Ада" || uc.authorLimit != 20 || !strings.Contains(w.Body.String(), `"Name":"Ада Лавлейс"`) {
		t.Fatalf("status %d search %q limit %d body %s", w.Code, uc.authorSearch, uc.authorLimit, w.Body.String())
	}
	named := httptest.NewRecorder()
	engineAs(uc, rbac.RoleUser).ServeHTTP(named, httptest.NewRequest(http.MethodGet, "/api/exercises/authors?id="+author.String(), nil))
	if named.Code != http.StatusOK || len(uc.authorIDs) != 1 || uc.authorIDs[0] != author {
		t.Fatalf("ids: status %d ids %v", named.Code, uc.authorIDs)
	}
	for _, query := range []string{"limit=0", "id=nope"} {
		bad := httptest.NewRecorder()
		engineAs(uc, rbac.RoleUser).ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/exercises/authors?"+query, nil))
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", query, bad.Code)
		}
	}
}

// The table filters of an offset page reach the use case: statuses, tags with all of / none of, authors, the
// difficulty range; bad values are refused.
func TestList_TableFilters(t *testing.T) {
	author := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	w := httptest.NewRecorder()
	url := "/api/exercises?page=1&pageSize=20&status=published,changed&status=archived&withTag=web&withTag=sqli&withoutTag=legacy" +
		"&author=" + author.String() + "&difficultyMin=2&difficultyMax=4&sortBy=difficulty&sortDir=asc"
	engineAs(uc, rbac.RoleUser).ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	f := uc.listFilter
	if strings.Join(f.Statuses, ",") != "published,changed,archived" || strings.Join(f.TagsAll, ",") != "web,sqli" ||
		strings.Join(f.TagsNone, ",") != "legacy" || len(f.AuthorIDs) != 1 || f.AuthorIDs[0] != author ||
		f.DifficultyMin != 2 || f.DifficultyMax != 4 || f.SortBy != "difficulty" {
		t.Fatalf("filter %+v", f)
	}
	for _, query := range []string{"difficultyMin=0", "difficultyMax=7", "difficultyMin=5&difficultyMax=2", "author=nope"} {
		bad := httptest.NewRecorder()
		engineAs(&fakeUC{}, rbac.RoleUser).ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/exercises?page=1&"+query, nil))
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", query, bad.Code)
		}
	}
}

// An event viewer reads their event's tasks without the hidden flags: the working copy and every version; whoever
// may see flags (owner/manager, exercises.read) gets them.
func TestViewerReadsVersionsWithoutFlags(t *testing.T) {
	exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	version := exerciseUseCase.VersionView{ID: versionID, ExerciseID: exerciseID, Status: "published", Variants: []exerciseModel.Variant{{
		Tasks: []exerciseModel.Task{{ID: uuid.Must(uuid.NewV7()), Name: "Find it", Difficulty: exerciseModel.DifficultyEasy, Flag: []string{"ICE{secret}"}, DeviceFlagVar: "SECRET_FLAG_VAR"}},
	}}}
	for _, tc := range []struct {
		name   string
		access exerciseUseCase.Access
		flags  bool
	}{
		{name: "event viewer", access: exerciseUseCase.Access{Full: true}, flags: false},
		{name: "event moderator", access: exerciseUseCase.Access{Full: true, Flags: true}, flags: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/draft", "/versions/" + versionID.String()} {
				access := tc.access
				uc := &fakeUC{versionView: version, access: &access}
				w := httptest.NewRecorder()
				engineAs(uc, rbac.RoleUser).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exerciseID.String()+path, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status %d body %s", path, w.Code, w.Body.String())
				}
				if got := strings.Contains(w.Body.String(), "SECRET_FLAG_VAR"); got != tc.flags {
					t.Fatalf("%s: flag env var shown=%t, want %t", path, got, tc.flags)
				}
				if got := strings.Contains(w.Body.String(), "ICE{secret}"); got != tc.flags {
					t.Fatalf("%s: flag shown=%t, want %t: %s", path, got, tc.flags, w.Body.String())
				}
				if version.Variants[0].Tasks[0].Flag == nil {
					t.Fatal("the redaction changed the use case's view")
				}
			}
		})
	}
}

// The internal notes (AdminNote, Variants[].Note) reach only full access: admins and the owner event's
// owners/managers (Access.Flags). An event viewer and a published-only reader of a catalog task get them blank,
// on the version, the working copy and the history list.
func TestNotesOnlyForFullAccess(t *testing.T) {
	exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	version := exerciseUseCase.VersionView{
		ID:         versionID,
		ExerciseID: exerciseID,
		Status:     "published",
		AdminNote:  "ADMIN_NOTE_TEXT",
		Variants: []exerciseModel.Variant{
			{
				Note: "VARIANT_NOTE_TEXT",
				Tasks: []exerciseModel.Task{
					{
						ID:         uuid.Must(uuid.NewV7()),
						Name:       "Find it",
						Difficulty: exerciseModel.DifficultyEasy,
					},
				},
			},
		},
	}
	list := []exerciseUseCase.VersionListItem{
		{ID: versionID, Status: "published", AdminNote: "ADMIN_NOTE_TEXT", VariantCount: 1},
	}
	for _, tc := range []struct {
		name   string
		access exerciseUseCase.Access
		notes  bool
		paths  []string
	}{
		{name: "platform admin or event moderator", access: exerciseUseCase.Access{Full: true, Flags: true}, notes: true, paths: []string{"/draft", "/versions/" + versionID.String(), "/versions"}},
		{name: "event viewer", access: exerciseUseCase.Access{Full: true}, notes: false, paths: []string{"/draft", "/versions/" + versionID.String(), "/versions"}},
		{name: "published-only reader", access: exerciseUseCase.Access{}, notes: false, paths: []string{"/versions/" + versionID.String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range tc.paths {
				access := tc.access
				body := readNotesPath(
					t,
					&fakeUC{versionView: version, versionList: list, access: &access},
					exerciseID,
					path,
				)
				if got := strings.Contains(body, "ADMIN_NOTE_TEXT"); got != tc.notes {
					t.Fatalf("%s: admin note shown=%t, want %t: %s", path, got, tc.notes, body)
				}
				if got := strings.Contains(
					body,
					"VARIANT_NOTE_TEXT",
				); path != "/versions" &&
					got != tc.notes {
					t.Fatalf("%s: variant note shown=%t, want %t: %s", path, got, tc.notes, body)
				}
			}
			if version.AdminNote == "" || version.Variants[0].Note == "" ||
				list[0].AdminNote == "" {
				t.Fatal("the redaction changed the use case's view")
			}
		})
	}
}

func readNotesPath(t *testing.T, uc *fakeUC, exerciseID uuid.UUID, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	engineAs(
		uc,
		rbac.RoleUser,
	).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exerciseID.String()+path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status %d body %s", path, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// A reader without full access (a published-only reader of a catalog task) never learns about the working copy:
// the card carries no draft and no «unpublished changes» (ES3); full access keeps both.
func TestCardWorkingCopyOnlyForFullAccess(t *testing.T) {
	exerciseID, draftID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name   string
		access exerciseUseCase.Access
		shown  bool
	}{
		{name: "full access", access: exerciseUseCase.Access{Full: true, Flags: true}, shown: true},
		{name: "published-only reader", access: exerciseUseCase.Access{}, shown: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := tc.access
			uc := &fakeUC{
				exerciseView: exerciseUseCase.ExerciseView{
					ID:             exerciseID,
					Name:           "card",
					HasChanges:     true,
					DraftVersionID: &draftID,
				},
				access: &access,
			}
			w := httptest.NewRecorder()
			engineAs(
				uc,
				rbac.RoleUser,
			).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exerciseID.String(), nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if got := strings.Contains(body, `"HasChanges":true`); got != tc.shown {
				t.Fatalf("changes shown=%t, want %t: %s", got, tc.shown, body)
			}
			if got := strings.Contains(body, draftID.String()); got != tc.shown {
				t.Fatalf("draft id shown=%t, want %t: %s", got, tc.shown, body)
			}
		})
	}
}
