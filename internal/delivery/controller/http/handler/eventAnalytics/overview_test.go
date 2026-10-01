package eventAnalytics

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

func TestOverviewFeedIsSerialized(t *testing.T) {
	at := time.Date(2026, 9, 29, 10, 4, 0, 0, time.UTC)
	teamID := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{overview: eventAnalyticsUseCase.OverviewView{
		Feed: []eventAnalyticsUseCase.FeedItemView{
			{Kind: "first_blood", At: at, TeamID: &teamID, TeamName: "Blue", ChallengeName: "Web 1"},
			{Kind: "freeze_started", At: at.Add(time.Hour)},
		},
	}}
	w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/overview")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"Kind":"first_blood"`, `"TeamName":"Blue"`, `"ChallengeName":"Web 1"`, `"Kind":"freeze_started"`, `"TeamID":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %s: %s", want, body)
		}
	}
}

// The export is the overview series as a UTF-8 CSV with a BOM, behind the
// sections gate, for the requested period.
func TestOverviewExportCSV(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	uc := &fakeUseCase{overview: eventAnalyticsUseCase.OverviewView{
		Series: []eventAnalyticsUseCase.SeriesPointView{{At: start, Attempts: 3, Correct: 1, Solves: 1, Opens: 2}, {At: start.Add(5 * time.Minute)}},
	}}
	w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/overview/export.csv?from=2026-09-29T10:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if len(uc.levels) != 1 || uc.levels[0] != eventAnalyticsUseCase.LevelSections {
		t.Fatalf("access levels checked: %v", uc.levels)
	}
	if uc.from == nil || !uc.from.Equal(start) {
		t.Fatalf("period from = %v", uc.from)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") || !strings.Contains(w.Header().Get("Content-Disposition"), "analytics-overview-") {
		t.Fatalf("headers: %v", w.Header())
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(w.Body.String(), "\xEF\xBB\xBF")), "\n")
	if len(lines) != 3 || lines[1] != "2026-09-29T10:00:00Z,3,1,1,2" || lines[2] != "2026-09-29T10:05:00Z,0,0,0,0" {
		t.Fatalf("csv lines: %q", lines)
	}
}
