package eventChallengeGroup

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

func TestGroupUpdate_NormalizesNameAndOrder(t *testing.T) {
	group, err := New(uuid.Must(uuid.NewV7()), "Initial", 0, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := group.Update("  Network  ", 3); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if group.Name != "Network" || group.Order != 3 {
		t.Fatalf("updated group = %#v, want normalized name and order", group)
	}
}

func TestGroupUpdate_RejectsInvalidValues(t *testing.T) {
	group, err := New(uuid.Must(uuid.NewV7()), "Initial", 0, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := group.Update(" ", -1); err == nil {
		t.Fatal("Update accepted invalid group values")
	}
}
