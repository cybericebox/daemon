package resourceCalendar

import "testing"

func TestPlacedTeamsAreAdmittedWithoutTheUnplacedPart(t *testing.T) {
	r := &Reservation{Teams: 19, Unplaced: 4, Size: Amount{CPUMillicores: 19000, MemoryBytes: 19 << 30}}
	if r.PlacedTeams() != 15 {
		t.Fatalf("placed = %d", r.PlacedTeams())
	}
	if !r.HoldsTeams(15) || r.HoldsTeams(16) {
		t.Fatal("the first 15 teams are admitted, the 16th waits")
	}
	if got := r.PlacedSize(); got.CPUMillicores != 15000 || got.MemoryBytes != 15<<30 {
		t.Fatalf("placed size = %+v", got)
	}
	r.Unplaced = 0
	if r.PlacedSize() != r.Size || !r.HoldsTeams(19) {
		t.Fatal("a fully placed reservation holds all teams")
	}
}

func TestUnplacedReason(t *testing.T) {
	w := Window{}
	r := &Reservation{Teams: 3, Unplaced: 1, Window: w, LargestDevice: Amount{CPUMillicores: 4000}}
	if got := r.UnplacedReason(nil); got != UnplacedNoAgents {
		t.Fatalf("no agents: %q", got)
	}
	small := Agent{DeviceMax: Amount{CPUMillicores: 1000}, Capacity: Amount{CPUMillicores: 10000, MemoryBytes: 1 << 40}}
	if got := r.UnplacedReason([]Agent{small}); got != UnplacedDeviceTooLarge {
		t.Fatalf("device: %q", got)
	}
	big := Agent{Capacity: Amount{CPUMillicores: 10000, MemoryBytes: 1 << 40}}
	if got := r.UnplacedReason([]Agent{small, big}); got != UnplacedNoCapacity {
		t.Fatalf("capacity: %q", got)
	}
	r.Unplaced = 0
	if r.UnplacedReason(nil) != "" {
		t.Fatal("all placed has no reason")
	}
}
