package event

import (
	"testing"
	"time"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// The screen timer walks the stages in time order, whatever the stored order.
func TestLiveScreenStagesAreInTimeOrder(t *testing.T) {
	at := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	got := toLiveScreenStages([]eventModel.Stage{
		{Name: "Етап 2", OpensAt: at.Add(3 * time.Hour), ClosesAt: at.Add(5 * time.Hour), Returnable: true},
		{Name: "Етап 1", OpensAt: at, ClosesAt: at.Add(2 * time.Hour)},
	})
	want := []LiveScreenStageView{{Name: "Етап 1", OpensAt: at, ClosesAt: at.Add(2 * time.Hour)}, {Name: "Етап 2", OpensAt: at.Add(3 * time.Hour), ClosesAt: at.Add(5 * time.Hour)}}
	if len(got) != len(want) {
		t.Fatalf("stages = %+v", got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || !got[i].OpensAt.Equal(want[i].OpensAt) || !got[i].ClosesAt.Equal(want[i].ClosesAt) {
			t.Fatalf("stage %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if stages := toLiveScreenStages(nil); stages == nil || len(stages) != 0 {
		t.Fatalf("no stages = %#v, want an empty list", stages)
	}
}
