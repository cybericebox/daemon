package platformAnalytics

import (
	"time"

	"github.com/gofrs/uuid"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

type (
	// InfrastructureView is the infrastructure report of one period.
	InfrastructureView struct {
		Period platformAnalyticsModel.Period
		// Bucket is the granularity of Peaks: "hour" or "day".
		Bucket string
		// Stands is the live picture right now, whatever the period.
		Stands StandCountsView
		// Moderators is the moderators-team part of Stands.
		Moderators StandCountsView
		// TestLabs are the catalog test labs (exercise test deploys), not team stands.
		TestLabs   TestLabCountsView
		StandHours StandHoursView
		// Resources is what the labs use right now, per kind.
		Resources ResourcesByKindView
		// Peaks counts the team stands only, TestLabPeaks the test labs only and
		// AllPeaks every lab at once (the peak of the sum, not the sum of peaks).
		Peaks        []PeakPointView
		TestLabPeaks []PeakPointView
		AllPeaks     []PeakPointView
		// PeakMax is the highest team stand peak of the period, AllPeakMax the highest of AllPeaks.
		PeakMax    int64
		AllPeakMax int64
		// Failures group failed labs / stands by reason code.
		Failures     []FailureReasonView
		FailedLabs   int64
		FailedStands int64
		// Capacity is the cluster capacity over time (all agents summed).
		Capacity            []CapacityPointView
		CapacityStepSeconds int64
	}

	// StandCountsView counts stands by their current status.
	StandCountsView struct {
		// Active is creating plus ready.
		Active, Creating, Ready, Failed, Removed int64
	}

	// ResourcesByKindView is the CPU and memory the labs of each kind use now.
	ResourcesByKindView struct {
		Event, Moderators, Test labMonitoringModel.Resources
	}

	// TestLabCountsView counts the catalog test labs: Active have a running
	// lease, Expired wait for the cleanup pass.
	TestLabCountsView struct {
		Active, Expired int64
	}

	// StandHoursView ranks events by summed stand active time (hours).
	StandHoursView struct {
		// TotalHours is the team stand time (event and moderators teams).
		TotalHours  float64
		TotalEvents int64
		Events      []StandHoursEventView
		// Kinds splits the time by lab kind (event, moderators, test), always in that order.
		Kinds []StandHoursKindView
		// AllHours is TotalHours plus the test lab time: every lab on the infrastructure.
		AllHours float64
	}

	// StandHoursKindView is the active time of one lab kind.
	StandHoursKindView struct {
		Kind  string
		Hours float64
		// Labs is the number of distinct stands or test labs that were active.
		Labs int64
	}

	StandHoursEventView struct {
		EventID   uuid.UUID
		EventName string
		Hours     float64
		// Stands is the number of distinct stands that were active.
		Stands int64
	}

	// PeakPointView is the most concurrently active stands in one bucket.
	PeakPointView struct {
		At   time.Time
		Peak int64
	}

	// FailureReasonView is one failure reason code (the frontend labels it).
	FailureReasonView struct {
		Code   string
		Labs   int64
		Stands int64
		Events int64
		LastAt time.Time
	}

	// CapacityPointView is the cluster capacity at one bucket.
	CapacityPointView struct {
		At                       time.Time
		AllocatableCPUMillicores int64
		RequestedCPUMillicores   int64
		AllocatableMemoryBytes   int64
		RequestedMemoryBytes     int64
		Agents                   int64
	}
)
