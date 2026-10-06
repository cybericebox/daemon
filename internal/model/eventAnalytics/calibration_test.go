package eventAnalyticsModel_test

import (
	"testing"

	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

func TestCalibrate(t *testing.T) {
	tests := []struct {
		name        string
		difficulty  string
		tried, done int64
		want        string
	}{
		{"in band", "medium", 10, 5, eventAnalyticsModel.CalibrationOK},
		{"hard task everybody solves", "hard", 10, 9, eventAnalyticsModel.CalibrationTooEasy},
		{"easy task nobody solves", "easy", 10, 2, eventAnalyticsModel.CalibrationTooHard},
		{"band edge is ok", "medium", 10, 3, eventAnalyticsModel.CalibrationOK},
		{"too few teams", "easy", 2, 0, eventAnalyticsModel.CalibrationInsufficient},
		{"unknown difficulty", "", 10, 5, eventAnalyticsModel.CalibrationUnknown},
		{"insane solved by half", "insane", 10, 5, eventAnalyticsModel.CalibrationTooEasy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventAnalyticsModel.Calibrate(tt.difficulty, tt.tried, tt.done).Verdict; got != tt.want {
				t.Fatalf("verdict = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSolveRate(t *testing.T) {
	if got := eventAnalyticsModel.SolveRate(0, 0); got != 0 {
		t.Fatalf("no tries: %v", got)
	}
	if got := eventAnalyticsModel.SolveRate(4, 1); got != 0.25 {
		t.Fatalf("got %v", got)
	}
}
