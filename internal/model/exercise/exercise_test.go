package exerciseModel_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

var (
	fixedNow = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	adminID  = uuid.Must(uuid.NewV7())
)

func TestNewExercise_SetsDomainDefaults(t *testing.T) {
	e, err := exerciseModel.NewExercise("SQLi basics", "Intro task", []string{"web", "sqli"}, adminID, fixedNow)
	if err != nil {
		t.Fatalf("NewExercise: %v", err)
	}
	if e.ID == uuid.Nil || e.ID.Version() != 7 {
		t.Fatalf("factory must assign a UUIDv7 id, got %s (version %d)", e.ID, e.ID.Version())
	}
	if !e.CreatedAt.Equal(fixedNow) || !e.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("timestamps must come from now param: %+v", e)
	}
	if !e.CreatedBy.Valid || e.CreatedBy.UUID != adminID {
		t.Fatalf("createdBy must be set: %+v", e.CreatedBy)
	}
	if e.DraftVersionID.Valid || e.PublishedVersionID.Valid {
		t.Fatal("new exercise must have no versions")
	}
}

func TestNewExercise_Validation(t *testing.T) {
	cases := []struct {
		name    string
		exName  string
		desc    string
		tags    []string
		wantErr error
	}{
		{"name too short", "ab", "", nil, exerciseModel.ErrExerciseNameInvalid.Err()},
		{"name too long", strings.Repeat("x", 51), "", nil, exerciseModel.ErrExerciseNameInvalid.Err()},
		{"name whitespace only", "   ", "", nil, exerciseModel.ErrExerciseNameInvalid.Err()},
		{"description too long", "valid name", strings.Repeat("x", 2001), nil, exerciseModel.ErrExerciseDescriptionTooLong.Err()},
		{"empty tag", "valid name", "", []string{""}, exerciseModel.ErrExerciseTagsInvalid.Err()},
		{"tag too long", "valid name", "", []string{strings.Repeat("t", 31)}, exerciseModel.ErrExerciseTagsInvalid.Err()},
		{"ok minimal", "abc", "", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := exerciseModel.NewExercise(tc.exName, tc.desc, tc.tags, adminID, fixedNow)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestUpdateIdentity_TouchesUpdatedAt(t *testing.T) {
	e, _ := exerciseModel.NewExercise("SQLi basics", "", nil, adminID, fixedNow)
	later := fixedNow.Add(time.Hour)
	editor := uuid.Must(uuid.NewV7())

	if err := e.UpdateIdentity("Renamed", "new desc", []string{"web"}, editor, later); err != nil {
		t.Fatalf("UpdateIdentity: %v", err)
	}
	if e.Name != "Renamed" || e.Description != "new desc" || len(e.Tags) != 1 {
		t.Fatalf("fields not applied: %+v", e)
	}
	if !e.UpdatedAt.Equal(later) {
		t.Fatal("UpdatedAt must be touched by the mutation")
	}
	if !e.UpdatedBy.Valid || e.UpdatedBy.UUID != editor {
		t.Fatal("UpdatedBy must be the editor")
	}
	if !e.CreatedAt.Equal(fixedNow) {
		t.Fatal("CreatedAt is immutable")
	}
}

func TestUpdateIdentity_InvalidLeavesEntityUntouched(t *testing.T) {
	e, _ := exerciseModel.NewExercise("SQLi basics", "", nil, adminID, fixedNow)
	before := e
	if err := e.UpdateIdentity("ab", "", nil, adminID, fixedNow.Add(time.Hour)); err == nil {
		t.Fatal("want validation error")
	}
	// Struct contains a slice (Tags), so we compare fields individually.
	if e.Name != before.Name || !e.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("failed mutation must not modify the entity")
	}
}

func TestExercise_ArchiveUnarchive(t *testing.T) {
	e, err := exerciseModel.NewExercise("Archive me", "", nil, adminID, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	archivedAt := fixedNow.Add(time.Hour)
	e.Archive(adminID, archivedAt)
	if e.ArchivedAt == nil || !e.ArchivedAt.Equal(archivedAt) || !e.UpdatedAt.Equal(archivedAt) {
		t.Fatalf("archive must stamp ArchivedAt and UpdatedAt: %+v", e)
	}
	e.Archive(adminID, archivedAt.Add(time.Hour)) // idempotent
	if !e.ArchivedAt.Equal(archivedAt) || !e.UpdatedAt.Equal(archivedAt) {
		t.Fatalf("repeated archive must not change anything: %+v", e)
	}
	if !errors.Is(e.EnsureNotArchived(), exerciseModel.ErrExerciseArchived.Err()) {
		t.Fatal("archived exercise must refuse mutations")
	}
	if err := e.UpdateIdentity("Renamed", "", nil, adminID, archivedAt); !errors.Is(err, exerciseModel.ErrExerciseArchived.Err()) {
		t.Fatalf("rename of archived exercise: want ErrExerciseArchived, got %v", err)
	}
	if e.Name != "Archive me" {
		t.Fatalf("rejected rename must not touch the entity: %q", e.Name)
	}

	unarchivedAt := archivedAt.Add(2 * time.Hour)
	e.Unarchive(adminID, unarchivedAt)
	if e.ArchivedAt != nil || !e.UpdatedAt.Equal(unarchivedAt) {
		t.Fatalf("unarchive must clear ArchivedAt: %+v", e)
	}
	e.Unarchive(adminID, unarchivedAt.Add(time.Hour)) // idempotent
	if !e.UpdatedAt.Equal(unarchivedAt) {
		t.Fatalf("repeated unarchive must not touch UpdatedAt: %+v", e)
	}
	if err := e.EnsureNotArchived(); err != nil {
		t.Fatalf("active exercise: %v", err)
	}
}

func TestExercise_ArchivedAtIsNotPortable(t *testing.T) {
	e, _ := exerciseModel.NewExercise("Portable", "", nil, adminID, fixedNow)
	e.Archive(adminID, fixedNow)
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ArchivedAt") {
		t.Fatalf("archive state is catalog-local and must not be exported: %s", b)
	}
}

func TestExercise_HasUnpublishedChanges(t *testing.T) {
	someID := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	boom := errors.New("boom")
	for _, tc := range []struct {
		name             string
		draft, published uuid.NullUUID
		differs          bool
		differsErr       error
		want             bool
		wantCalled       bool
		wantErr          error
	}{
		{name: "never published", draft: someID, want: true},
		{name: "never saved nor published", want: true},
		{name: "published, no draft row", published: someID, want: false},
		{name: "draft equals published", draft: someID, published: someID, differs: false, want: false, wantCalled: true},
		{name: "draft differs", draft: someID, published: someID, differs: true, want: true, wantCalled: true},
		{name: "compare fails", draft: someID, published: someID, differsErr: boom, wantCalled: true, wantErr: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := exerciseModel.Exercise{DraftVersionID: tc.draft, PublishedVersionID: tc.published}
			called := false
			got, err := e.HasUnpublishedChanges(func() (bool, error) {
				called = true
				return tc.differs, tc.differsErr
			})
			if !errors.Is(err, tc.wantErr) || got != tc.want || called != tc.wantCalled {
				t.Fatalf("got=%t err=%v called=%t; want %t %v %t", got, err, called, tc.want, tc.wantErr, tc.wantCalled)
			}
		})
	}
}

func TestEnsureNotInUse(t *testing.T) {
	if err := exerciseModel.EnsureNotInUse(nil); err != nil {
		t.Fatalf("unused exercise: %v", err)
	}
	err := exerciseModel.EnsureNotInUse([]string{"Spring CTF"})
	if !errors.Is(err, exerciseModel.ErrExerciseInUse.Err()) || !strings.Contains(err.Error(), "Spring CTF") {
		t.Fatalf("want ErrExerciseInUse with events context, got %v", err)
	}
}

func TestCatalogStatus_Valid(t *testing.T) {
	for _, s := range []exerciseModel.CatalogStatus{"none", "draft_only", "changed", "published", "archived"} {
		if !s.Valid() {
			t.Fatalf("%q must be valid", s)
		}
	}
	for _, s := range []exerciseModel.CatalogStatus{"", "draft", "Published"} {
		if s.Valid() {
			t.Fatalf("%q must be invalid", s)
		}
	}
}

func TestExercise_SetAccess(t *testing.T) {
	origin := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	for _, tc := range []struct {
		name         string
		level        exerciseModel.AccessLevel
		hasSelection bool
		origin       uuid.NullUUID
		eventScoped  bool
		wantErr      bool
	}{
		{name: "all events", level: exerciseModel.AccessAllEvents},
		{name: "selected needs a selection", level: exerciseModel.AccessSelectedEvents, wantErr: true},
		{name: "selected events", level: exerciseModel.AccessSelectedEvents, hasSelection: true},
		{name: "origin needs an origin event", level: exerciseModel.AccessOriginEvent, wantErr: true},
		{name: "origin event", level: exerciseModel.AccessOriginEvent, origin: origin},
		{name: "no event needs no selection", level: exerciseModel.AccessNone},
		{name: "no event ignores a selection", level: exerciseModel.AccessNone, hasSelection: true},
		{name: "unknown level", level: exerciseModel.AccessNone + 1, wantErr: true},
		{name: "event exercises have no access level", level: exerciseModel.AccessNone, eventScoped: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := exerciseModel.NewExercise("Access test", "", nil, adminID, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			e.OriginEventID = tc.origin
			if tc.eventScoped {
				e.Scope = exerciseModel.ScopeEvent
			}
			later := fixedNow.Add(time.Hour)
			err = e.SetAccess(tc.level, tc.hasSelection, adminID, later)
			if tc.wantErr {
				if err == nil || e.AccessLevel != exerciseModel.AccessAllEvents {
					t.Fatalf("want an error and no change: level=%d err=%v", e.AccessLevel, err)
				}
				return
			}
			if err != nil || e.AccessLevel != tc.level || !e.UpdatedAt.Equal(later) {
				t.Fatalf("level=%d updated=%v err=%v", e.AccessLevel, e.UpdatedAt, err)
			}
		})
	}
}

// The name limit counts characters, not bytes: a 50-letter Cyrillic name (100 bytes) is valid.
func TestUpdateIdentity_NameLimitInCharacters(t *testing.T) {
	e, _ := exerciseModel.NewExercise("SQLi basics", "", nil, adminID, fixedNow)
	name := strings.Repeat("ї", 50)
	if err := e.UpdateIdentity(name, "", nil, adminID, fixedNow); err != nil {
		t.Fatalf("50 Cyrillic letters must pass: %v", err)
	}
	if err := e.UpdateIdentity(name+"ї", "", nil, adminID, fixedNow); err == nil {
		t.Fatal("51 letters must be refused")
	}
}
