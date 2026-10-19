package resourceCalendar

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

var (
	partsNow = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	agentA   = uuid.Must(uuid.FromString("00000000-0000-0000-0000-00000000000a"))
	agentB   = uuid.Must(uuid.FromString("00000000-0000-0000-0000-00000000000b"))
)

func seqIDs() func() uuid.UUID {
	n := 0
	return func() uuid.UUID {
		n++
		return uuid.Must(uuid.FromString("00000000-0000-0000-0000-1000000000" + string(rune('0'+n/10)) + string(rune('0'+n%10))))
	}
}

func eventReservation(size Amount, teams int, shares ...Share) *Reservation {
	return &Reservation{
		ID: uuid.Must(uuid.FromString("00000000-0000-0000-0000-0000000000e1")), Kind: KindEvent, Teams: teams, Size: size,
		Window: Window{Start: partsNow, End: partsNow.Add(4 * time.Hour)}, Placement: shares,
	}
}

func TestSplitCalendarProportionalCeil(t *testing.T) {
	r := eventReservation(Amount{CPUMillicores: 10000, MemoryBytes: 3 << 30}, 3, Share{AgentID: agentA, Units: 2}, Share{AgentID: agentB, Units: 1})
	keep, add, retire := SplitCalendar(r, nil, seqIDs(), partsNow)
	if len(keep) != 0 || len(retire) != 0 || len(add) != 2 {
		t.Fatalf("keep %d add %d retire %d", len(keep), len(add), len(retire))
	}
	if add[0].AgentID != agentA || add[0].Amount.CPUMillicores != 6667 || add[0].Amount.MemoryBytes != 2<<30 {
		t.Fatalf("part on A: %+v", add[0])
	}
	if add[1].AgentID != agentB || add[1].Amount.CPUMillicores != 3334 || add[1].Amount.MemoryBytes != 1<<30 {
		t.Fatalf("part on B: %+v", add[1])
	}
	for _, p := range add {
		if p.Source != PartCalendar || p.CalendarID == nil || *p.CalendarID != r.ID || p.Window != r.Window || p.DesiredRev != 1 || p.SyncedRev != 0 {
			t.Fatalf("part fields: %+v", p)
		}
	}
}

func TestSplitKeepsIDsOnResize(t *testing.T) {
	r := eventReservation(Amount{CPUMillicores: 3000, MemoryBytes: 3000}, 3, Share{AgentID: agentA, Units: 2}, Share{AgentID: agentB, Units: 1})
	_, first, _ := SplitCalendar(r, nil, seqIDs(), partsNow)
	for i := range first {
		first[i].MarkSynced(first[i].DesiredRev, partsNow)
	}
	r.Size = Amount{CPUMillicores: 6000, MemoryBytes: 6000}
	r.Window.End = r.Window.End.Add(time.Hour)
	later := partsNow.Add(time.Minute)
	keep, add, retire := SplitCalendar(r, first, seqIDs(), later)
	if len(add) != 0 || len(retire) != 0 || len(keep) != 2 {
		t.Fatalf("keep %d add %d retire %d", len(keep), len(add), len(retire))
	}
	for i, p := range keep {
		if p.ID != first[i].ID {
			t.Fatalf("id changed: %s -> %s", first[i].ID, p.ID)
		}
		if p.Synced() || p.DesiredRev != 2 || p.Window.End != r.Window.End {
			t.Fatalf("resize not marked: %+v", p)
		}
	}
	if keep[0].Amount.CPUMillicores != 4000 || keep[1].Amount.CPUMillicores != 2000 {
		t.Fatalf("amounts: %+v %+v", keep[0].Amount, keep[1].Amount)
	}
	// The same split again changes nothing: no new revision.
	again, _, _ := SplitCalendar(r, keep, seqIDs(), later)
	if again[0].DesiredRev != 2 {
		t.Fatalf("unchanged split bumped the revision: %d", again[0].DesiredRev)
	}
}

func TestSplitRetiresEmptiedShare(t *testing.T) {
	r := eventReservation(Amount{CPUMillicores: 3000, MemoryBytes: 3000}, 3, Share{AgentID: agentA, Units: 2}, Share{AgentID: agentB, Units: 1})
	_, first, _ := SplitCalendar(r, nil, seqIDs(), partsNow)
	r.Placement = []Share{{AgentID: agentA, Units: 3}, {AgentID: agentB, Units: 0}}
	keep, add, retire := SplitCalendar(r, first, seqIDs(), partsNow)
	if len(keep) != 1 || len(add) != 0 || len(retire) != 1 {
		t.Fatalf("keep %d add %d retire %d", len(keep), len(add), len(retire))
	}
	if retire[0].AgentID != agentB || retire[0].RetireAt == nil || !retire[0].RetireAt.Equal(partsNow) {
		t.Fatalf("retired: %+v", retire[0])
	}
	// A canceled reservation retires every part.
	r.Cancel(partsNow)
	keep, _, retire = SplitCalendar(r, keep, seqIDs(), partsNow)
	if len(keep) != 0 || len(retire) != 1 {
		t.Fatalf("canceled: keep %d retire %d", len(keep), len(retire))
	}
}

func TestUnitAssignmentFollowsShares(t *testing.T) {
	shares := []Share{{AgentID: agentA, Units: 3}, {AgentID: agentB, Units: 2}}
	assigned := map[string]uuid.UUID{}
	var got []uuid.UUID
	for _, team := range []string{"team:1", "team:2", "team:3", "team:4", "team:5"} {
		a, err := AssignUnit(shares, assigned, team)
		if err != nil {
			t.Fatal(err)
		}
		assigned[team] = a
		got = append(got, a)
	}
	want := []uuid.UUID{agentA, agentA, agentA, agentB, agentB}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("team %d on %s, want %s", i+1, got[i], want[i])
		}
	}
}

func TestUnitAssignmentStable(t *testing.T) {
	shares := []Share{{AgentID: agentA, Units: 1}, {AgentID: agentB, Units: 1}}
	assigned := map[string]uuid.UUID{"team:1": agentB}
	a, err := AssignUnit(shares, assigned, "team:1")
	if err != nil || a != agentB {
		t.Fatalf("kept assignment: %s %v", a, err)
	}
	// The next team takes the share that is still free.
	a, err = AssignUnit(shares, assigned, "team:2")
	if err != nil || a != agentA {
		t.Fatalf("next team: %s %v", a, err)
	}
	// A preview owner never takes a team's place: it goes to the first share.
	assigned["team:2"] = agentA
	a, err = AssignUnit(shares, assigned, PreviewUnit(agentB))
	if err != nil || a != agentA {
		t.Fatalf("preview: %s %v", a, err)
	}
}

func TestUnitNotPlaced(t *testing.T) {
	shares := []Share{{AgentID: agentA, Units: 1}}
	assigned := map[string]uuid.UUID{"team:1": agentA}
	if _, err := AssignUnit(shares, assigned, "team:2"); !errors.Is(err, ErrUnitNotPlaced.Err()) {
		t.Fatalf("want ErrUnitNotPlaced, got %v", err)
	}
	if _, err := AssignUnit(nil, map[string]uuid.UUID{}, PreviewUnit(agentA)); !errors.Is(err, ErrUnitNotPlaced.Err()) {
		t.Fatalf("no shares: want ErrUnitNotPlaced, got %v", err)
	}
}

func TestPartSyncState(t *testing.T) {
	p := Part{ID: agentA, AgentID: agentA, Source: PartTestRun, Window: Window{Start: partsNow, End: partsNow.Add(time.Hour)}, Amount: Amount{CPUMillicores: 1, MemoryBytes: 1}, DesiredRev: 1}
	if p.Synced() {
		t.Fatal("new part is synced")
	}
	p.MarkSyncFailed("agent down", partsNow)
	if p.Synced() || p.SyncError != "agent down" {
		t.Fatalf("failed: %+v", p)
	}
	p.MarkSynced(1, partsNow)
	if !p.Synced() || p.SyncError != "" || p.SyncedAt == nil {
		t.Fatalf("synced: %+v", p)
	}
	// An answer for an older revision does not mark a newer one synced.
	p.SetDesired(p.Window, Amount{CPUMillicores: 2, MemoryBytes: 2}, partsNow)
	p.MarkSynced(1, partsNow)
	if p.Synced() {
		t.Fatal("old revision marked the new one synced")
	}
}
