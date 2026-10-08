package eventLabModel

import (
	"github.com/gofrs/uuid"
	"time"
)

type GroupTarget struct {
	Group, ExpectedUID string
	OperationID        uuid.UUID
	Revision           int64
}
type Group struct {
	RetirementStopTarget                                                   *GroupTarget
	RetirementState, RetirementError                                       string
	RetirementObservedAt                                                   *time.Time
	EventID, TeamID                                                        uuid.UUID
	Name, AgentUID, DesiredState, ActualState, FailureCode, FailureMessage string
	AgentGeneration, Revision, ObservedRevision                            int64
	OperationID                                                            uuid.UUID
	Ready, AccessFenced                                                    bool
	PendingStarts                                                          int32
	ObservedAt, RetentionUntil, ProtectedUntil                             *time.Time
	Allocation                                                             Allocation
	ConfiguredRequests                                                     Compute
	NextAttemptAt, CreatedAt, UpdatedAt                                    time.Time
}

func (g *Group) Target() GroupTarget {
	return GroupTarget{g.Name, g.AgentUID, g.OperationID, g.Revision}
}
func (g *Group) HeldCompute() Compute {
	if g.DesiredState == "Deleted" && g.RetirementStopTarget != nil && g.RetirementStopTarget.ExpectedUID == g.AgentUID && g.Allocation.RuntimeState == "Released" && g.Allocation.ReleasedAt != nil && g.AccessFenced {
		return Compute{}
	}
	if g.AgentUID != "" && g.AgentGeneration > 0 && g.ObservedRevision == g.Revision && g.ObservedAt != nil && g.DesiredState != "Running" && (g.ActualState == "Stopped" || g.ActualState == "Deleted") && g.Allocation.RuntimeState == "Released" && g.Allocation.ReleasedAt != nil && g.AccessFenced && g.FailureCode == "" {
		return Compute{}
	}
	held := g.Allocation.AllocatedRequests
	return Compute{max(held.CPUMillicores, g.ConfiguredRequests.CPUMillicores), max(held.MemoryBytes, g.ConfiguredRequests.MemoryBytes)}
}
func (g *Group) RequestRunning(pending int32, operation uuid.UUID, now time.Time) bool {
	if g.DesiredState == "Deleted" || pending < 0 || operation == uuid.Nil {
		return false
	}
	if g.DesiredState != "Running" {
		g.Revision++
		g.OperationID = operation
		g.DesiredState = "Running"
		g.Ready = false
		g.Allocation.RuntimeState = "Admitted"
		g.Allocation.ReleasedAt = nil
	}
	g.PendingStarts = pending
	g.UpdatedAt = now
	g.NextAttemptAt = now
	return true
}
func (g *Group) RequestStop(children []Lab, operation uuid.UUID, authorizedRuntime bool, now time.Time) bool {
	if g.AgentUID == "" || g.AgentGeneration <= 0 || g.PendingStarts != 0 || authorizedRuntime || g.DesiredState == "Deleted" || operation == uuid.Nil {
		return false
	}
	for _, l := range children {
		if l.DesiredState == "Running" || (l.ActualState != "Stopped" && l.ActualState != "Deleted") || l.HeldCompute() != (Compute{}) || l.AgentUID == "" || l.ObservedRevision != l.Revision || l.Allocation.RuntimeState != "Released" || !l.AccessFenced || (l.SnapshotMode == "required" && l.SnapshotState != "Succeeded") {
			return false
		}
	}
	if g.DesiredState == "Stopped" {
		return false
	}
	g.DesiredState = "Stopped"
	g.Revision++
	g.OperationID = operation
	g.Ready = false
	g.UpdatedAt = now
	g.NextAttemptAt = now
	return true
}
func (g *Group) Observe(o GroupObservation, now time.Time) bool {
	if g.DesiredState == "Deleted" && g.RetirementStopTarget != nil {
		return g.ObserveRetirement(o, now)
	}
	if o.Name != g.Name || o.UID == "" || o.Generation <= 0 || o.ObservedAt == nil || (g.ObservedAt != nil && !o.ObservedAt.After(*g.ObservedAt)) {
		return false
	}
	if g.AgentUID == "" {
		if g.Revision != 1 || g.DesiredState != "Running" || !o.ImmutableSizesKnown || o.VPNSize.CPUMillicores+o.GatewaySize.CPUMillicores < g.ConfiguredRequests.CPUMillicores || o.VPNSize.MemoryBytes+o.GatewaySize.MemoryBytes < g.ConfiguredRequests.MemoryBytes {
			return false
		}
		g.AgentUID = o.UID
		g.AgentGeneration = o.Generation
	}
	if o.UID != g.AgentUID || o.Generation < g.AgentGeneration {
		return false
	}
	initial := g.Revision == 1 && g.DesiredState == "Running" && o.DesiredState == "Running" && o.InitialReady
	exact := o.OperationID == g.OperationID && o.Revision == g.Revision && o.ObservedGeneration == o.Generation && o.DesiredState == g.DesiredState
	if !initial && !exact {
		if g.Revision == 1 && g.DesiredState == "Running" && o.OperationID == uuid.Nil && o.Revision == 0 && o.ImmutableSizesKnown {
			g.ObservedAt = cloneTime(o.ObservedAt)
			g.UpdatedAt = now
			return true
		}
		return false
	}
	g.AgentGeneration = o.Generation
	g.ObservedAt = cloneTime(o.ObservedAt)
	g.FailureCode = o.FailureCode
	g.FailureMessage = o.FailureMessage
	g.UpdatedAt = now
	if initial {
		g.ActualState = "Running"
		g.Ready = true
		return true
	}
	g.ObservedRevision = o.Revision
	g.ActualState = o.ActualState
	g.Ready = o.Ready && g.DesiredState == "Running"
	g.AccessFenced = o.AccessFenced
	release := g.DesiredState != "Running" && (o.ActualState == "Stopped" || o.ActualState == "Deleted") && o.Allocation.RuntimeState == "Released" && o.Allocation.ReleasedAt != nil && o.AccessFenced && o.FailureCode == "" && o.FailureMessage == ""
	if release {
		previous := g.Allocation.AllocatedRequests
		g.Allocation = o.Allocation
		g.Allocation.AllocatedRequests = previous
	} else if o.Allocation.RuntimeState == "Allocated" || o.Allocation.RuntimeState == "Releasing" {
		previous := g.HeldCompute()
		g.Allocation = o.Allocation
		g.Allocation.AllocatedRequests = Compute{max(previous.CPUMillicores, o.Allocation.AllocatedRequests.CPUMillicores), max(previous.MemoryBytes, o.Allocation.AllocatedRequests.MemoryBytes)}
		g.Allocation.ReleasedAt = nil
	}
	return true
}
func (g *Group) AllowsChildren(now time.Time) bool {
	return g.DesiredState == "Running" && g.ActualState == "Running" && g.Ready && g.AgentUID != "" && g.ObservedAt != nil && !g.ObservedAt.After(now) && now.Sub(*g.ObservedAt) <= 30*time.Second && (g.Revision == 1 || g.ObservedRevision == g.Revision)
}

func NewGroup(eventID, teamID uuid.UUID, name string, need Compute, now time.Time) Group {
	return Group{EventID: eventID, TeamID: teamID, Name: name, DesiredState: "Running", ActualState: "Unknown", Revision: 1, OperationID: uuid.Must(uuid.NewV7()), ConfiguredRequests: need, Allocation: Allocation{ConfiguredRequestsKnown: true, ConfiguredRequests: need, ConfiguredLimits: need, AllocatedRequests: need, RuntimeState: "Admitted", StorageState: "None"}, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
}
