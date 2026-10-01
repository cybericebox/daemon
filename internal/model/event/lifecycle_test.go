package eventModel

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestLifecycleContainsOnlyConcreteRuntimeConfiguration(t *testing.T) {
	lifecycleType := reflect.TypeOf(Lifecycle{})
	for _, obsolete := range []string{"Type", "Schedule"} {
		if _, exists := lifecycleType.FieldByName(obsolete); exists {
			t.Fatalf("lifecycle must not retain obsolete %s field", obsolete)
		}
	}
}

func TestNewLifecycle_ScheduledRuntimeDerivesEveryStatus(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(4 * time.Hour)
	withdrawAt := finishAt.Add(time.Hour)

	lifecycle, err := NewLifecycle(JoinPolicyLockedAtStart, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}

	checks := []struct {
		at   time.Time
		want LifecycleStatus
	}{
		{publishAt.Add(-time.Nanosecond), LifecycleNotPublished},
		{publishAt, LifecyclePublished},
		{startAt, LifecycleStarted},
		{finishAt, LifecycleFinished},
		{withdrawAt, LifecycleWithdrawn},
	}
	for _, check := range checks {
		if got := lifecycle.Status(check.at); got != check.want {
			t.Errorf("Status(%s) = %v, want %v", check.at, got, check.want)
		}
	}
}

func TestNewLifecycle_WithoutFinishHasNoAutomaticFinish(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	lifecycle, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	if got := lifecycle.Status(startAt.AddDate(2, 0, 0)); got != LifecycleStarted {
		t.Fatalf("permanent event status = %v, want started", got)
	}
}

func TestLifecycleRuntimeOpen_OnlyWhileStarted(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(time.Hour)
	withdrawAt := finishAt.Add(time.Hour)
	lifecycle, err := NewLifecycle(JoinPolicyLockedAtStart, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	if lifecycle.RuntimeOpen(startAt.Add(-time.Nanosecond)) {
		t.Fatal("runtime opened before start")
	}
	if !lifecycle.RuntimeOpen(startAt) {
		t.Fatal("runtime must open at the start boundary")
	}
	if lifecycle.RuntimeOpen(finishAt) {
		t.Fatal("runtime stayed open at finish")
	}
}

func TestNewLifecycle_ManualFinishOverridesScheduledFinishUntilWithdrawal(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(4 * time.Hour)
	withdrawAt := finishAt.Add(time.Hour)
	manualFinishAt := startAt.Add(time.Hour)
	lifecycle, err := NewLifecycle(JoinPolicyLockedAtStart, publishAt, startAt, &finishAt, &withdrawAt, &manualFinishAt)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	if got := lifecycle.Status(manualFinishAt); got != LifecycleFinished {
		t.Fatalf("manual finish status = %v, want finished", got)
	}
	if got := lifecycle.Status(withdrawAt); got != LifecycleWithdrawn {
		t.Fatalf("withdrawal must take precedence, got %v", got)
	}
}

func TestNewLifecycle_RejectsIncompleteFinishWindow(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(time.Hour)
	withdrawAt := finishAt.Add(time.Hour)

	tests := []struct {
		name     string
		finish   *time.Time
		withdraw *time.Time
	}{
		{"finish needs withdrawal", nil, &withdrawAt},
		{"withdrawal needs finish", &finishAt, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, test.finish, test.withdraw, nil)
			if !errors.Is(err, ErrEventLifecycleInvalid.Err()) {
				t.Fatalf("want ErrEventLifecycleInvalid, got %v", err)
			}
		})
	}
}

func TestNewLifecycle_RejectsOutOfOrderTimes(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(time.Hour)
	withdrawBeforeFinish := finishAt.Add(-time.Nanosecond)
	_, err := NewLifecycle(JoinPolicyLockedAtStart, publishAt, startAt, &finishAt, &withdrawBeforeFinish, nil)
	if !errors.Is(err, ErrEventLifecycleInvalid.Err()) {
		t.Fatalf("want ErrEventLifecycleInvalid, got %v", err)
	}
}

func TestEventUpdateLifecycle_ReplacesCanonicalRuntimeAndTouchesAudit(t *testing.T) {
	event, err := NewEvent("summer", "Summer", from, until, someUser, fixedNow)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	lifecycle, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	updatedAt := fixedNow.Add(time.Hour)

	event.UpdateLifecycle(lifecycle, someUser, updatedAt)

	if event.Lifecycle != lifecycle || !event.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("lifecycle update was not applied: %+v", event)
	}
}

func TestLifecycle_RegistrationOpenFollowsJoinPolicy(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(time.Hour)
	withdrawAt := finishAt.Add(time.Hour)
	locked, err := NewLifecycle(JoinPolicyLockedAtStart, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatalf("NewLifecycle locked: %v", err)
	}
	rolling, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatalf("NewLifecycle rolling: %v", err)
	}

	if !locked.RegistrationOpen(publishAt) || locked.RegistrationOpen(startAt) {
		t.Fatal("locked-at-start registration must be [publish, start)")
	}
	if !rolling.RegistrationOpen(startAt) || rolling.RegistrationOpen(finishAt) {
		t.Fatal("rolling registration must be [publish, finish)")
	}
}

func TestLifecycle_RegistrationClosesOnManualFinishOrWithdrawal(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(4 * time.Hour)
	manualFinishAt := startAt.Add(time.Hour)
	withdrawAt := finishAt.Add(time.Hour)
	finished, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, &finishAt, &withdrawAt, &manualFinishAt)
	if err != nil {
		t.Fatal(err)
	}
	withdrawn, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if finished.RegistrationOpen(manualFinishAt) || withdrawn.RegistrationOpen(withdrawAt) {
		t.Fatal("registration must close as soon as the event finishes or is withdrawn")
	}
}

func TestLifecycle_RosterOpenFollowsJoinPeriod(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	finishAt := startAt.Add(time.Hour)
	withdrawAt := finishAt.Add(time.Hour)
	locked, err := NewLifecycle(JoinPolicyLockedAtStart, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatalf("NewLifecycle locked: %v", err)
	}
	rolling, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatalf("NewLifecycle rolling: %v", err)
	}
	if !locked.RosterOpen(startAt.Add(-time.Nanosecond)) || locked.RosterOpen(startAt) {
		t.Fatal("locked roster must be open before start and closed at start")
	}
	if !rolling.RosterOpen(startAt.Add(-time.Nanosecond)) || !rolling.RosterOpen(startAt) || !rolling.RosterOpen(finishAt.Add(-time.Nanosecond)) {
		t.Fatal("rolling registration must keep the roster open while the event runs")
	}
	if rolling.RosterOpen(finishAt) || rolling.RosterOpen(withdrawAt) {
		t.Fatal("rolling roster must freeze once the event finishes")
	}
	if (Lifecycle{}).RosterOpen(startAt) {
		t.Fatal("an unconfigured lifecycle has no open roster")
	}
}

func TestLifecycleHasStarted(t *testing.T) {
	start := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	lifecycle := Lifecycle{Configured: true, PublishAt: start.Add(-time.Hour), StartAt: start}

	if lifecycle.HasStarted(start.Add(-time.Second)) {
		t.Fatal("HasStarted before start")
	}
	if !lifecycle.HasStarted(start) || !lifecycle.HasStarted(start.Add(time.Hour)) {
		t.Fatal("HasStarted false at or after start")
	}
	if (Lifecycle{StartAt: start}).HasStarted(start.Add(time.Hour)) {
		t.Fatal("unscheduled lifecycle reported as started")
	}
}
