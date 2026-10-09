package eventConfigModel

import (
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

func TestLabPolicyExplicitRetentionWinsLegacyTiming(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	c := NewEventConfig(uuid.Must(uuid.NewV7()), now)
	c.StandTiming.TeardownDelayMinutes = 90
	if got := c.EffectiveLabPolicy(); got.RetentionMinutes != 90 || got.SnapshotMode != "skip" {
		t.Fatal(got)
	}
	policy := eventLabModel.Policy{SnapshotMode: "required", RetentionMinutes: 0}
	if err := c.SetLabPolicy(policy, now, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	c.StandTiming.TeardownDelayMinutes = 60
	if got := c.EffectiveLabPolicy(); got.RetentionMinutes != 0 || got.SnapshotMode != "required" {
		t.Fatal(got)
	}
	policy.RetentionMinutes = -1
	if err := c.SetLabPolicy(policy, now, uuid.Nil); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if c.LabPolicy.RetentionMinutes != 0 {
		t.Fatal("invalid mutation changed policy")
	}
}
