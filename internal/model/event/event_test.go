package eventModel

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

var (
	fixedNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	from     = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	until    = time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
	someUser = uuid.Must(uuid.NewV7())
)

func TestNewEvent_OK(t *testing.T) {
	e, err := NewEvent("winter", "Winter Arena", from, until, someUser, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.ID == uuid.Nil {
		t.Fatal("expected a generated id")
	}
	if e.Tag != "winter" || e.Name != "Winter Arena" || e.InternalName != "Winter Arena" {
		t.Fatalf("tag/name not set: %+v", e)
	}
	if !e.AvailableFrom.Equal(from) || !e.ArchiveAt.Equal(until) {
		t.Fatalf("dates not set: %+v", e)
	}
	if e.Lifecycle.Configured || e.Lifecycle.FinishAt != nil || e.Lifecycle.WithdrawAt != nil ||
		!e.Lifecycle.PublishAt.Equal(from) || !e.Lifecycle.StartAt.Equal(from) ||
		e.Lifecycle.Status(from) != LifecycleNotPublished {
		t.Fatalf("new event must remain unpublished until managers schedule it: %+v", e.Lifecycle)
	}
	if !e.CreatedAt.Equal(fixedNow) || !e.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("timestamps not from clock: %+v", e)
	}
}

func TestNewEvent_DefaultsAvailableFromToNow(t *testing.T) {
	e, err := NewEvent("winter", "Winter", time.Time{}, until, someUser, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !e.AvailableFrom.Equal(fixedNow) {
		t.Fatalf("expected AvailableFrom defaulted to now, got %v", e.AvailableFrom)
	}
}

func TestNewEvent_RejectsBadTag(t *testing.T) {
	for _, tag := range []string{"", "ab", "UPPER", "has space", "sym!bol"} {
		if _, err := NewEvent(tag, "Winter", from, until, someUser, fixedNow); err == nil {
			t.Fatalf("expected ErrEventTagInvalid for tag %q", tag)
		}
	}
}

func TestNewEvent_RejectsReservedTag(t *testing.T) {
	for _, tag := range []string{"api", "admin", "exercises", "labs", "vpn", "ctl", "www"} {
		_, err := NewEvent(tag, "Winter", from, until, someUser, fixedNow)
		if !errors.Is(err, ErrEventTagReserved.Err()) {
			t.Fatalf("tag %q: want ErrEventTagReserved, got %v", tag, err)
		}
	}
}

func TestUpdateEvent_RenameToReservedTagIsRefused(t *testing.T) {
	e, _ := NewEvent("winter", "Winter", from, until, someUser, fixedNow)
	if err := e.UpdateEvent("api", "Winter", from, until, someUser, fixedNow); err == nil {
		t.Fatal("renaming to a reserved tag must be refused")
	}
	if e.Tag != "winter" {
		t.Fatalf("the tag must stay untouched, got %q", e.Tag)
	}
	// An event that already holds a reserved tag (made before the rule) stays editable under it.
	old := Event{Tag: "www"}
	if err := old.UpdateEvent("www", "Renamed", from, until, someUser, fixedNow); err != nil {
		t.Fatalf("keeping its own tag: %v", err)
	}
}

func TestNewEvent_RejectsArchiveNotAfterAvailable(t *testing.T) {
	if _, err := NewEvent("winter", "Winter", until, from, someUser, fixedNow); err == nil {
		t.Fatal("expected ErrEventDatesInvalid when ArchiveAt <= AvailableFrom")
	}
	if _, err := NewEvent("winter", "Winter", from, from, someUser, fixedNow); err == nil {
		t.Fatal("expected ErrEventDatesInvalid when ArchiveAt == AvailableFrom")
	}
}

func TestUpdateEvent_AppliesAndTouchesAudit(t *testing.T) {
	e, _ := NewEvent("winter", "Old", from, until, someUser, fixedNow)
	later := fixedNow.Add(time.Hour)
	newUntil := until.Add(24 * time.Hour)
	if err := e.UpdateEvent("winter", "New", from, newUntil, someUser, later); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Name != "Old" || e.InternalName != "New" || !e.ArchiveAt.Equal(newUntil) || !e.UpdatedAt.Equal(later) || !e.Lifecycle.PublishAt.Equal(from) {
		t.Fatalf("update not applied: %+v", e)
	}
}

func TestUpdateEventPreservesConfiguredPublicScheduleAndName(t *testing.T) {
	e, _ := NewEvent("winter", "Internal", from, until, someUser, fixedNow)
	publicFinish := until.Add(-time.Hour)
	publicWithdraw := until.Add(-time.Minute)
	lifecycle, err := NewLifecycle(JoinPolicyLockedAtStart, from.Add(time.Hour), from.Add(2*time.Hour), &publicFinish, &publicWithdraw, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.UpdateLifecycle(lifecycle, someUser, fixedNow)
	if err := e.UpdatePublicName("Public", someUser, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := e.UpdateEvent("winter2", "Changed internal", from, until.Add(time.Hour), someUser, fixedNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if e.Name != "Public" || e.InternalName != "Changed internal" || !e.Lifecycle.PublishAt.Equal(lifecycle.PublishAt) || !e.Lifecycle.WithdrawAt.Equal(*lifecycle.WithdrawAt) {
		t.Fatalf("platform edit must not overwrite public configuration: %+v", e)
	}
}

func TestUpdateEvent_InvalidLeavesEntityUntouched(t *testing.T) {
	e, _ := NewEvent("winter", "Keep", from, until, someUser, fixedNow)
	before := e
	if err := e.UpdateEvent("winter", "Changed", until, from, someUser, fixedNow.Add(time.Hour)); err == nil {
		t.Fatal("expected error for bad dates")
	}
	if e != before {
		t.Fatalf("entity mutated on invalid update: %+v", e)
	}
}

func TestArchive_SetsArchiveAtToNow(t *testing.T) {
	e, _ := NewEvent("winter", "Winter", from, until, someUser, fixedNow)
	at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	e.Archive(at, someUser)
	if !e.ArchiveAt.Equal(at) || !e.UpdatedAt.Equal(at) {
		t.Fatalf("archive not applied: %+v", e)
	}
	if e.Status(at) != EventArchivedStatus {
		t.Fatal("expected Archived after Archive")
	}
}

func TestArchiveBeforeStartProducesValidWithdrawnLifecycle(t *testing.T) {
	e, err := NewEvent("winter", "Winter", from, until, someUser, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	at := fixedNow.Add(time.Hour)
	e.Archive(at, someUser)
	if !e.ArchiveAt.Equal(at) || !e.AvailableFrom.Equal(from) {
		t.Fatalf("archive must retain original availability and set end: %+v", e)
	}
	if e.Lifecycle.FinishAt == nil || !e.Lifecycle.FinishAt.Equal(at) ||
		e.Lifecycle.WithdrawAt == nil || !e.Lifecycle.WithdrawAt.After(at) ||
		!e.Lifecycle.StartAt.Before(at) || e.Lifecycle.PublishAt.After(e.Lifecycle.StartAt) {
		t.Fatalf("pre-start archive must leave a valid immediate withdrawal lifecycle: %+v", e.Lifecycle)
	}
	if e.Lifecycle.Status(*e.Lifecycle.WithdrawAt) != LifecycleWithdrawn {
		t.Fatal("archived event must be withdrawn from its tenant lifecycle")
	}
}

func TestStatus_Boundaries(t *testing.T) {
	e, _ := NewEvent("winter", "Winter", from, until, someUser, fixedNow)
	if got := e.Status(fixedNow); got != EventPendingStatus { // before AvailableFrom
		t.Fatalf("want Pending, got %v", got)
	}
	if got := e.Status(from); got != EventActiveStatus { // == AvailableFrom
		t.Fatalf("want Active at AvailableFrom, got %v", got)
	}
	if got := e.Status(until); got != EventArchivedStatus { // == ArchiveAt
		t.Fatalf("want Archived at ArchiveAt, got %v", got)
	}
}
