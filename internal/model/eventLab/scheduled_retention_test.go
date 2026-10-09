package eventLabModel

import (
	"github.com/gofrs/uuid"
	"reflect"
	"testing"
	"time"
)

func TestScheduledRetentionMovesCapturedTTLWithoutChangingTerminalIdentity(t *testing.T) {
	l, err := New(labInput(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	l.RuntimeStageKnown = true
	l.RetentionMinutes = 15
	if err := l.Close("solved", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	before := l
	finish := testNow.Add(4 * time.Hour)
	if !l.RefreshWholeEventRetention(&finish, testNow.Add(time.Second)) {
		t.Fatal("deadline unchanged")
	}
	want := finish.Add(15 * time.Minute)
	if l.RetentionUntil == nil || !l.RetentionUntil.Equal(want) {
		t.Fatal("did not use captured TTL")
	}
	l.RetentionUntil = before.RetentionUntil
	l.UpdatedAt = before.UpdatedAt
	if !reflect.DeepEqual(l, before) {
		t.Fatal("scheduled retention changed terminal identity/state")
	}
}
func TestScheduledRetentionLeavesDifferentDeadlineAuthoritiesUntouched(t *testing.T) {
	for _, kind := range []string{"manual", "stage", "unknown-scope", "deleted", "retiring"} {
		t.Run(kind, func(t *testing.T) {
			l, err := New(labInput(), testNow)
			if err != nil {
				t.Fatal(err)
			}
			l.RuntimeStageKnown = true
			switch kind {
			case "manual":
				l.CloseReason = "manual"
			case "stage":
				id := uuid.Must(uuid.NewV7())
				l.RuntimeStageID = &id
			case "unknown-scope":
				l.RuntimeStageKnown = false
			case "deleted":
				l.DesiredState = "Deleted"
			case "retiring":
				l.RetirementStopTarget = &Target{}
			}
			before := l
			finish := testNow.Add(time.Hour)
			if l.RefreshWholeEventRetention(&finish, testNow.Add(time.Second)) || !reflect.DeepEqual(before, l) {
				t.Fatal("overrode another deadline authority")
			}
		})
	}
}
func TestScheduledRetentionRemovingFinishClearsOnlyScheduleDeadline(t *testing.T) {
	l, err := New(labInput(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	l.RuntimeStageKnown = true
	finish := testNow.Add(time.Hour)
	if !l.RefreshWholeEventRetention(&finish, testNow) {
		t.Fatal("missing scheduled deadline")
	}
	protection := finish.Add(6 * time.Hour)
	l.ProtectedUntil = &protection
	if !l.RefreshWholeEventRetention(nil, testNow.Add(time.Second)) || l.RetentionUntil != nil || !l.EffectiveRetentionUntil().Equal(protection) {
		t.Fatal("finishless event lost future protection or retained old scheduled expiry")
	}
	before := l
	if l.RefreshWholeEventRetention(nil, testNow.Add(2*time.Second)) || !reflect.DeepEqual(before, l) {
		t.Fatal("identical deadline caused another mutation")
	}
}
