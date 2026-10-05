package eventStandModel

import (
	"math"
	"time"
)

// The pre-deploy lead is computed by the platform, one formula for the event start and for every stage
// (docs/specs/event-stages.md, Labs):
//
//	waves = ceil(workload / maxPods)
//	lead  = clamp(waves x avgPodStart x 1.5 + image pre-pull, 10 min, 240 min)
const (
	// DefaultAvgPodStart is how long one wave of pods takes to start until the platform learns it from history.
	DefaultAvgPodStart = 90 * time.Second
	// LeadSafety is the margin on the estimated start time.
	LeadSafety = 1.5
	MinLead    = 10 * time.Minute
	MaxLead    = 240 * time.Minute
	// DefaultImagePrePull is the image pre-pull share of a lead when the platform image cache can warm the
	// images of the sets that open.
	DefaultImagePrePull = 5 * time.Minute
	// PlanningLead is the lead the resource calendar assumes when it plans an event window (it has no workload).
	PlanningLead = 30 * time.Minute
)

// LeadInput is what the lead depends on.
type LeadInput struct {
	// Workload is the pods to start at that moment: teams x devices of the sets that open (plus the VPN and
	// gateway pods of every team's group at the event start).
	Workload int
	// MaxPods is the sum of the pods the serving agents start at once; 0 means no limit (one wave).
	MaxPods int
	// AvgPodStart is the average time a wave of pods takes; zero means DefaultAvgPodStart.
	AvgPodStart time.Duration
	// PrePull is the time to pull the images of the sets that open.
	PrePull time.Duration
}

// Waves is how many rounds of at most MaxPods pods the workload needs.
func (in LeadInput) Waves() int {
	if in.Workload <= 0 {
		return 0
	}
	if in.MaxPods <= 0 {
		return 1
	}
	return (in.Workload + in.MaxPods - 1) / in.MaxPods
}

// ComputeLead is how long before a moment the labs that open then must start deploying.
func ComputeLead(in LeadInput) time.Duration {
	avg := in.AvgPodStart
	if avg <= 0 {
		avg = DefaultAvgPodStart
	}
	lead := time.Duration(math.Round(float64(in.Waves()) * float64(avg) * LeadSafety))
	lead += in.PrePull
	return min(max(lead, MinLead), MaxLead)
}
