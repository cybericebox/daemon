package eventModel

import (
	"errors"
	"testing"
	"time"
)

func unpublishedEvent(t *testing.T, allowed bool) Event {
	t.Helper()
	e, err := NewEvent("infra", "Infra", from, until, someUser, fixedNow)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	e.InfrastructureAllowed = allowed
	return e
}

func TestSetInfrastructureAllowed_BeforePublication(t *testing.T) {
	later := fixedNow.Add(time.Minute)
	e := unpublishedEvent(t, false)
	if err := e.SetInfrastructureAllowed(true, 0, someUser, later); err != nil {
		t.Fatalf("turn on: %v", err)
	}
	if !e.InfrastructureAllowed || !e.UpdatedAt.Equal(later) || e.UpdatedBy.UUID != someUser {
		t.Fatalf("flag/audit fields not applied: %+v", e)
	}
	if err := e.SetInfrastructureAllowed(false, 0, someUser, later.Add(time.Minute)); err != nil || e.InfrastructureAllowed {
		t.Fatalf("turn off: %v %+v", err, e)
	}
}

func TestSetInfrastructureAllowed_SameValueIsNoOp(t *testing.T) {
	e := unpublishedEvent(t, true)
	if err := e.SetInfrastructureAllowed(true, 3, someUser, fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("no-op must not fail: %v", err)
	}
	if !e.UpdatedAt.Equal(fixedNow) {
		t.Fatal("no-op must not touch UpdatedAt")
	}
}

func TestSetInfrastructureAllowed_LockedAfterPublication(t *testing.T) {
	e := unpublishedEvent(t, false)
	// publish now, start later
	l, err := NewLifecycle(JoinPolicyLockedAtStart, from, until.Add(-time.Hour), nil, nil, nil)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	e.Lifecycle = l
	e.Lifecycle.Configured = true
	now := from.Add(time.Minute)
	if e.Lifecycle.Status(now) == LifecycleNotPublished {
		t.Fatal("fixture must be published")
	}
	for _, allowed := range []bool{true} {
		if err := e.SetInfrastructureAllowed(allowed, 0, someUser, now); !errors.Is(err, ErrEventInfrastructureLocked.Err()) {
			t.Fatalf("want ErrEventInfrastructureLocked, got %v", err)
		}
	}
	if e.InfrastructureAllowed {
		t.Fatal("flag must stay unchanged when locked")
	}
}

func TestSetInfrastructureAllowed_OffRefusedWhileSetsAttached(t *testing.T) {
	e := unpublishedEvent(t, true)
	err := e.SetInfrastructureAllowed(false, 2, someUser, fixedNow.Add(time.Minute))
	if !errors.Is(err, ErrEventInfrastructureInUse.Err()) {
		t.Fatalf("want ErrEventInfrastructureInUse, got %v", err)
	}
	if !e.InfrastructureAllowed || !e.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("entity must stay untouched on refusal: %+v", e)
	}
}
