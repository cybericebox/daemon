package eventLabModel

import (
	"github.com/gofrs/uuid"
	"reflect"
	"testing"
	"time"
)

func equalServiceReleaseFixture() (Group, GroupObservation, time.Time) {
	at := time.UnixMilli(1791574310000)
	old := Compute{CPUMillicores: 140, MemoryBytes: 603979776}
	op := uuid.FromStringOrNil("01a1221a-a463-7c8d-93e6-d7d9ee1b4150")
	g := Group{Name: "moderator-group", AgentUID: "d5ba1eac-979d-4654-9262-875448249ca6", AgentGeneration: 7, Revision: 2, ObservedRevision: 2, OperationID: op, DesiredState: "Stopped", ActualState: "Stopped", ObservedAt: &at, ConfiguredRequests: old, Allocation: Allocation{RuntimeState: "Admitted", AllocatedRequests: old, ConfiguredRequests: old}}
	o := GroupObservation{Name: g.Name, UID: g.AgentUID, Generation: 7, ObservedGeneration: 7, OperationID: op, Revision: 2, ObservedRevision: 2, DesiredState: "Stopped", ActualState: "Stopped", ObservedAt: &at, AccessFenced: true, ServiceReleaseCertified: true, Allocation: Allocation{RuntimeState: "Released", ObservedAt: &at, ReleasedAt: &at}}
	return g, o, at.Add(time.Second)
}

func TestGroupEqualServiceReleaseQualificationIsMonotonicAndIdempotent(t *testing.T) {
	g, o, now := equalServiceReleaseFixture()
	old := g.ConfiguredRequests
	if !g.Observe(o, now) || g.HeldCompute() != (Compute{}) || !g.AccessFenced || g.Allocation.RuntimeState != "Released" {
		t.Fatalf("exact equal-time qualification did not release: %+v", g)
	}
	if g.ConfiguredRequests != old || g.Allocation.AllocatedRequests != old || g.Ready || !g.ObservedAt.Equal(*o.ObservedAt) {
		t.Fatal("qualification rewrote budget/readiness/native observation time")
	}
	qualified := g
	if g.Observe(o, now.Add(time.Minute)) || !reflect.DeepEqual(g, qualified) {
		t.Fatal("alreadyqualified equal duplicate mutated ledger")
	}
}

func TestGroupEqualServiceReleaseRejectsAnythingExceptCurrentCompleteUpgrade(t *testing.T) {
	changes := map[string]func(*Group, *GroupObservation, time.Time){
		"older": func(g *Group, o *GroupObservation, n time.Time) {
			at := o.ObservedAt.Add(-time.Second)
			o.ObservedAt = &at
			o.Allocation.ObservedAt = &at
		},
		"nil_observation": func(g *Group, o *GroupObservation, n time.Time) { o.ObservedAt = nil },
		"future_observation": func(g *Group, o *GroupObservation, n time.Time) {
			at := n.Add(time.Second)
			g.ObservedAt = &at
			o.ObservedAt = &at
			o.Allocation.ObservedAt = &at
		},
		"nonpositive_time": func(g *Group, o *GroupObservation, n time.Time) {
			at := time.UnixMilli(0)
			g.ObservedAt = &at
			o.ObservedAt = &at
			o.Allocation.ObservedAt = &at
		},
		"foreign_name":              func(g *Group, o *GroupObservation, n time.Time) { o.Name = "another" },
		"foreign_uid":               func(g *Group, o *GroupObservation, n time.Time) { o.UID = "other" },
		"new_generation":            func(g *Group, o *GroupObservation, n time.Time) { o.Generation++; o.ObservedGeneration++ },
		"old_generation":            func(g *Group, o *GroupObservation, n time.Time) { o.Generation--; o.ObservedGeneration-- },
		"stale_observed_generation": func(g *Group, o *GroupObservation, n time.Time) { o.ObservedGeneration-- },
		"wrong_operation": func(g *Group, o *GroupObservation, n time.Time) {
			o.OperationID = uuid.FromStringOrNil("01a1221a-a463-7c8d-93e6-d7d9ee1b4151")
		},
		"wrong_revision":             func(g *Group, o *GroupObservation, n time.Time) { o.Revision-- },
		"wrong_observed_revision":    func(g *Group, o *GroupObservation, n time.Time) { o.ObservedRevision-- },
		"prior_unobserved_revision":  func(g *Group, o *GroupObservation, n time.Time) { g.ObservedRevision = 0 },
		"prior_running_intent":       func(g *Group, o *GroupObservation, n time.Time) { g.DesiredState = "Running" },
		"prior_running_actual":       func(g *Group, o *GroupObservation, n time.Time) { g.ActualState = "Running" },
		"prior_ready":                func(g *Group, o *GroupObservation, n time.Time) { g.Ready = true },
		"running_intent":             func(g *Group, o *GroupObservation, n time.Time) { o.DesiredState = "Running" },
		"running_actual":             func(g *Group, o *GroupObservation, n time.Time) { o.ActualState = "Running" },
		"ready":                      func(g *Group, o *GroupObservation, n time.Time) { o.Ready = true },
		"prior_error":                func(g *Group, o *GroupObservation, n time.Time) { g.FailureCode = "StopFailed" },
		"error":                      func(g *Group, o *GroupObservation, n time.Time) { o.FailureMessage = "partial native reports" },
		"not_certified":              func(g *Group, o *GroupObservation, n time.Time) { o.ServiceReleaseCertified = false },
		"not_fenced":                 func(g *Group, o *GroupObservation, n time.Time) { o.AccessFenced = false },
		"not_released":               func(g *Group, o *GroupObservation, n time.Time) { o.Allocation.RuntimeState = "Unknown" },
		"allocated_cpu":              func(g *Group, o *GroupObservation, n time.Time) { o.Allocation.AllocatedRequests.CPUMillicores = 1 },
		"allocated_memory":           func(g *Group, o *GroupObservation, n time.Time) { o.Allocation.AllocatedRequests.MemoryBytes = 1 },
		"nil_allocation_observation": func(g *Group, o *GroupObservation, n time.Time) { o.Allocation.ObservedAt = nil },
		"wrong_allocation_time": func(g *Group, o *GroupObservation, n time.Time) {
			at := o.ObservedAt.Add(-time.Second)
			o.Allocation.ObservedAt = &at
		},
		"nil_release": func(g *Group, o *GroupObservation, n time.Time) { o.Allocation.ReleasedAt = nil },
		"nonpositive_release": func(g *Group, o *GroupObservation, n time.Time) {
			at := time.UnixMilli(0)
			o.Allocation.ReleasedAt = &at
		},
		"future_release": func(g *Group, o *GroupObservation, n time.Time) {
			at := n.Add(time.Hour)
			o.Allocation.ReleasedAt = &at
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			g, o, now := equalServiceReleaseFixture()
			change(&g, &o, now)
			before := g
			if g.Observe(o, now) || !reflect.DeepEqual(g, before) {
				t.Fatal("unqualified/equal or older receipt mutated group")
			}
		})
	}
}
