package eventAnalyticsModel_test

import (
	"errors"
	"testing"
	"time"

	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

func TestNewPeriod(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 2, 0, 0, time.UTC)
	finish := time.Date(2026, 9, 29, 16, 3, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 9, 29, h, m, 0, 0, time.UTC) }
	ptr := func(t time.Time) *time.Time { return &t }
	cases := []struct {
		name     string
		from, to *time.Time
		finish   *time.Time
		now      time.Time
		want     eventAnalyticsModel.Period
	}{
		{"finished event: start to finish", nil, nil, &finish, at(20, 0), eventAnalyticsModel.Period{From: at(10, 0), To: at(16, 5)}},
		{"running event: start to now", nil, nil, &finish, at(12, 31), eventAnalyticsModel.Period{From: at(10, 0), To: at(12, 35)}},
		{"not started: one bucket", nil, nil, nil, at(9, 0), eventAnalyticsModel.Period{From: at(10, 0), To: at(10, 5)}},
		{"explicit bounds", ptr(at(11, 0)), ptr(at(12, 0)), &finish, at(20, 0), eventAnalyticsModel.Period{From: at(11, 0), To: at(12, 0)}},
	}
	for _, tc := range cases {
		got, err := eventAnalyticsModel.NewPeriod(tc.from, tc.to, start, tc.finish, tc.now)
		if err != nil || !got.From.Equal(tc.want.From) || !got.To.Equal(tc.want.To) {
			t.Errorf("%s: got %+v err=%v, want %+v", tc.name, got, err, tc.want)
		}
	}
	for name, bounds := range map[string][2]time.Time{
		"reversed": {at(12, 0), at(11, 0)},
		"too long": {at(0, 0), at(0, 0).AddDate(0, 2, 0)},
	} {
		if _, err := eventAnalyticsModel.NewPeriod(ptr(bounds[0]), ptr(bounds[1]), start, &finish, at(20, 0)); !errors.Is(err, eventAnalyticsModel.ErrEventAnalyticsPeriodInvalid.Err()) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
