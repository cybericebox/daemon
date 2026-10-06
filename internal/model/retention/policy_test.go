package retentionModel_test

import (
	"testing"
	"time"

	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
)

var now = time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC)

func TestDefaultPolicy_MatchesPrivacyPolicy(t *testing.T) {
	c := retentionModel.DefaultPolicy().Cutoffs(now)
	cases := []struct {
		name string
		got  time.Time
		want time.Time
	}{
		{"sessions: 90 days after expiry", c.SessionsExpiredBefore, time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC)},
		{"lab telemetry: 90 days", c.LabObservedBefore, time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC)},
		{"delivery log: 180 days", c.DispatchesCreatedBefore, time.Date(2026, 4, 2, 3, 0, 0, 0, time.UTC)},
		{"form answers: event end + 365 days", c.EventsEndedBefore, time.Date(2025, 9, 29, 3, 0, 0, 0, time.UTC)},
		{"inactive: 3 x 365 days", c.InactiveSince, time.Date(2023, 9, 30, 3, 0, 0, 0, time.UTC)},
		{"deletion: 30 days after the warning", c.WarnedBefore, time.Date(2026, 8, 30, 3, 0, 0, 0, time.UTC)},
		{"signal history: 365 days", c.SignalsCreatedBefore, time.Date(2025, 9, 29, 3, 0, 0, 0, time.UTC)},
		{"event analytics: event end + 365 days", c.AnalyticsEventsEndedBefore, time.Date(2025, 9, 29, 3, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		if !tc.got.Equal(tc.want) {
			t.Errorf("%s: got %s, want %s", tc.name, tc.got, tc.want)
		}
	}
}

func TestPolicy_CutoffsFollowConfiguredPeriods(t *testing.T) {
	p := retentionModel.Policy{
		SessionAfterExpiry: 48 * time.Hour, LabTelemetry: 72 * time.Hour, DeliveryLog: 96 * time.Hour,
		FormAnswersAfterEventEnd: 120 * time.Hour, InactiveAccount: 144 * time.Hour, InactivityGrace: 24 * time.Hour,
		SignalHistory: 168 * time.Hour, EventAnalyticsAfterEnd: 192 * time.Hour, BatchSize: 10,
	}
	c := p.Cutoffs(now)
	if !c.SessionsExpiredBefore.Equal(now.Add(-48*time.Hour)) || !c.LabObservedBefore.Equal(now.Add(-72*time.Hour)) ||
		!c.DispatchesCreatedBefore.Equal(now.Add(-96*time.Hour)) || !c.EventsEndedBefore.Equal(now.Add(-120*time.Hour)) ||
		!c.InactiveSince.Equal(now.Add(-144*time.Hour)) || !c.WarnedBefore.Equal(now.Add(-24*time.Hour)) ||
		!c.SignalsCreatedBefore.Equal(now.Add(-168*time.Hour)) || !c.AnalyticsEventsEndedBefore.Equal(now.Add(-192*time.Hour)) {
		t.Fatalf("cutoffs do not follow the policy: %+v", c)
	}
}

func TestPolicy_DeletionDateIsWarningPlusGrace(t *testing.T) {
	got := retentionModel.DefaultPolicy().DeletionDate(now)
	if want := time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("DeletionDate: got %s, want %s", got, want)
	}
}

func TestPolicy_Validate(t *testing.T) {
	if err := retentionModel.DefaultPolicy().Validate(); err != nil {
		t.Fatalf("default policy must be valid: %v", err)
	}
	short := retentionModel.DefaultPolicy()
	short.DeliveryLog = time.Hour
	if short.Validate() == nil {
		t.Fatal("a period under 24h must be rejected")
	}
	noBatch := retentionModel.DefaultPolicy()
	noBatch.BatchSize = 0
	if noBatch.Validate() == nil {
		t.Fatal("a zero batch size must be rejected")
	}
}
