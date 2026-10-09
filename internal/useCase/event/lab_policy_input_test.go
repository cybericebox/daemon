package event

import (
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"testing"
)

func TestLabPolicyInputRetainsOmittedFallbackAndExplicitZero(t *testing.T) {
	mode := "required"
	in := UpdateLabPolicyInput{SnapshotMode: &mode}
	current := eventLabModel.DefaultPolicy()
	current.RetentionMinutes = 90
	got := in.Resolve(current)
	if got.RetentionMinutes != 90 || got.SnapshotMode != "required" || got.MaxActiveLabsPerTeam != nil {
		t.Fatal(got)
	}
	zero := int32(0)
	in.RetentionMinutes = &zero
	got = in.Resolve(current)
	if got.RetentionMinutes != 0 {
		t.Fatal(got)
	}
}
