package event

import (
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"strconv"
	"time"
)

type ParticipantLabView struct {
	ID              uuid.UUID  `json:"ID"`
	EventExerciseID uuid.UUID  `json:"EventExerciseID"`
	Revision        string     `json:"Revision"`
	LogicalClosed   bool       `json:"LogicalClosed"`
	CloseReason     *string    `json:"CloseReason" extensions:"x-nullable"`
	ClosedAt        *time.Time `json:"ClosedAt" extensions:"x-nullable"`
	RuntimeState    string     `json:"RuntimeState"`
	CanStop         bool       `json:"CanStop"`
	CanRestart      bool       `json:"CanRestart"`
	SnapshotPolicy  string     `json:"SnapshotPolicy"`
	RetentionUntil  *time.Time `json:"RetentionUntil" extensions:"x-nullable"`
}

type ComputeView struct {
	CPUMillicores string `json:"CPUMillicores"`
	MemoryBytes   string `json:"MemoryBytes"`
}

type AllocationView struct {
	ConfiguredRequests            ComputeView `json:"ConfiguredRequests"`
	ConfiguredLimits              ComputeView `json:"ConfiguredLimits"`
	AllocatedRequests             ComputeView `json:"AllocatedRequests"`
	ReleasedRequests              ComputeView `json:"ReleasedRequests"`
	RuntimeState                  string      `json:"RuntimeState"`
	ObservedAt                    *time.Time  `json:"ObservedAt" extensions:"x-nullable"`
	ReleasedAt                    *time.Time  `json:"ReleasedAt" extensions:"x-nullable"`
	UsageAvailable                bool        `json:"UsageAvailable"`
	Used                          ComputeView `json:"Used"`
	SnapshotQuotaBytes            string      `json:"SnapshotQuotaBytes"`
	StorageState                  string      `json:"StorageState"`
	PhysicalStorageBytesAvailable bool        `json:"PhysicalStorageBytesAvailable"`
	PhysicalStorageBytes          string      `json:"PhysicalStorageBytes"`
}

type ManagedLabView struct {
	SnapshotPolicy   string         `json:"SnapshotPolicy"`
	ID               uuid.UUID      `json:"ID"`
	EventExerciseID  uuid.UUID      `json:"EventExerciseID"`
	TeamID           uuid.UUID      `json:"TeamID"`
	ExerciseName     string         `json:"ExerciseName"`
	Revision         string         `json:"Revision"`
	ObservedRevision string         `json:"ObservedRevision"`
	Generation       int32          `json:"Generation"`
	AgentUID         string         `json:"AgentUID"`
	DesiredState     string         `json:"DesiredState"`
	ActualState      string         `json:"ActualState"`
	CloseReason      *string        `json:"CloseReason" extensions:"x-nullable"`
	ClosedAt         *time.Time     `json:"ClosedAt" extensions:"x-nullable"`
	ActualStoppedAt  *time.Time     `json:"ActualStoppedAt" extensions:"x-nullable"`
	RetentionUntil   *time.Time     `json:"RetentionUntil" extensions:"x-nullable"`
	ObservedAt       *time.Time     `json:"ObservedAt" extensions:"x-nullable"`
	SnapshotState    string         `json:"SnapshotState"`
	FailureCode      string         `json:"FailureCode"`
	FailureMessage   string         `json:"FailureMessage"`
	Resources        AllocationView `json:"Resources"`
}

type ManagedGroupView struct {
	Name             string         `json:"Name"`
	Revision         string         `json:"Revision"`
	ObservedRevision string         `json:"ObservedRevision"`
	AgentUID         string         `json:"AgentUID"`
	DesiredState     string         `json:"DesiredState"`
	ActualState      string         `json:"ActualState"`
	Ready            bool           `json:"Ready"`
	ObservedAt       *time.Time     `json:"ObservedAt" extensions:"x-nullable"`
	FailureCode      string         `json:"FailureCode"`
	FailureMessage   string         `json:"FailureMessage"`
	Resources        AllocationView `json:"Resources"`
}

func computeView(v eventLabModel.Compute) ComputeView {
	return ComputeView{strconv.FormatInt(v.CPUMillicores, 10), strconv.FormatInt(v.MemoryBytes, 10)}
}
func allocationView(a eventLabModel.Allocation) AllocationView {
	if a.RuntimeState == "Admitted" {
		a.RuntimeState = "Unknown"
	}
	out := AllocationView{ConfiguredRequests: computeView(a.ConfiguredRequests), ConfiguredLimits: computeView(a.ConfiguredLimits), AllocatedRequests: computeView(a.AllocatedRequests), ReleasedRequests: computeView(eventLabModel.Compute{}), RuntimeState: a.RuntimeState, ObservedAt: a.ObservedAt, ReleasedAt: a.ReleasedAt, UsageAvailable: a.UsageAvailable, Used: computeView(a.Used), SnapshotQuotaBytes: strconv.FormatInt(a.SnapshotQuotaBytes, 10), StorageState: a.StorageState, PhysicalStorageBytesAvailable: a.PhysicalStorageBytesAvailable, PhysicalStorageBytes: strconv.FormatInt(a.PhysicalStorageBytes, 10)}
	if a.RuntimeState == "Released" && a.ReleasedAt != nil {
		out.ReleasedRequests = computeView(a.AllocatedRequests)
		out.AllocatedRequests = computeView(eventLabModel.Compute{})
	}
	return out
}
func managedLabView(l eventLabModel.Lab, name string) ManagedLabView {
	safe := participantLabView(l)
	snapshot := l.SnapshotState
	if l.SnapshotMode == "skip" {
		snapshot = "NotRequired"
	} else if snapshot == "Snapshotting" {
		snapshot = "Pending"
	}
	return ManagedLabView{SnapshotPolicy: safe.SnapshotPolicy, ID: l.ID, EventExerciseID: l.EventExerciseID, TeamID: l.TeamID, ExerciseName: name, Revision: safe.Revision, ObservedRevision: strconv.FormatInt(l.ObservedRevision, 10), Generation: l.Generation, AgentUID: l.AgentUID, DesiredState: l.DesiredState, ActualState: l.ActualState, CloseReason: safe.CloseReason, ClosedAt: l.ClosedAt, ActualStoppedAt: l.ActualStoppedAt, RetentionUntil: l.RetentionUntil, ObservedAt: l.ObservedAt, SnapshotState: snapshot, FailureCode: l.FailureCode, FailureMessage: l.FailureMessage, Resources: allocationView(l.Allocation)}
}

func participantLabView(lab eventLabModel.Lab) ParticipantLabView {
	out := ParticipantLabView{ID: lab.ID, EventExerciseID: lab.EventExerciseID, Revision: strconv.FormatInt(lab.Revision, 10), LogicalClosed: lab.ClosedAt != nil, ClosedAt: lab.ClosedAt, RuntimeState: "preparing", SnapshotPolicy: "none", RetentionUntil: lab.RetentionUntil}
	if lab.CloseReason != "" {
		reason := lab.CloseReason
		out.CloseReason = &reason
	}
	if lab.SnapshotMode == "required" {
		out.SnapshotPolicy = "required"
	}
	if out.LogicalClosed {
		out.RuntimeState = "closed"
		return out
	}
	switch lab.ActualState {
	case "Running":
		if lab.RuntimeReady {
			out.RuntimeState = "ready"
		}
	case "Unknown", "StopFailed":
		out.RuntimeState = "unavailable"
	}
	return out
}

func managedGroupView(g eventLabModel.GroupObservation) ManagedGroupView {
	return ManagedGroupView{Name: g.Name, Revision: strconv.FormatInt(g.Revision, 10), ObservedRevision: strconv.FormatInt(g.ObservedRevision, 10), AgentUID: g.UID, DesiredState: g.DesiredState, ActualState: g.ActualState, Ready: g.Ready, ObservedAt: g.ObservedAt, FailureCode: g.FailureCode, FailureMessage: g.FailureMessage, Resources: allocationView(g.Allocation)}
}
