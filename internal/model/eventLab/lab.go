// Package eventLabModel owns the desired lifecycle of one team's shared lab generation.
package eventLabModel

import (
	"github.com/gofrs/uuid"
	"regexp"
	"time"
)

type Ref struct{ Group, Lab string }
type Target struct {
	Ref         Ref
	ExpectedUID string
	OperationID uuid.UUID
	Revision    int64
}
type StopRequest struct {
	Target         Target
	SnapshotMode   string
	RetentionUntil *time.Time
	Terminal       bool
}
type Observation struct {
	// Generation is live CR metadata; ObservedGeneration belongs to lifecycle status.
	// Neither is the deployment generation Lab.Generation.
	Ref                                                                   Ref
	UID                                                                   string
	OperationID                                                           uuid.UUID
	Generation, Revision, ObservedGeneration                              int64
	DesiredState, ActualState, SnapshotState, FailureCode, FailureMessage string
	StoppedAt, ObservedAt                                                 *time.Time
	RuntimeReady                                                          bool
	Allocation                                                            Allocation
	AccessFenced                                                          bool
	AccessFencedAt                                                        *time.Time
	AccessFenceVPNBootID                                                  string
}
type Lab struct {
	ID, EventID, TeamID, EventExerciseID                                  uuid.UUID
	Ref                                                                   Ref
	VariantIndex, Generation                                              int32
	AgentUID                                                              string
	AgentGeneration                                                       int64
	Revision, ObservedRevision                                            int64
	OperationID                                                           uuid.UUID
	DesiredState, ActualState, CloseReason, SnapshotMode, SnapshotState   string
	ClosedAt, RetentionUntil, ProtectedUntil, ActualStoppedAt, ObservedAt *time.Time
	ObjectiveCount                                                        int32
	Materialized                                                          bool
	RuntimeReady                                                          bool
	Allocation                                                            Allocation
	FailureCode, FailureMessage                                           string
	AccessFenced                                                          bool
	AccessFencedAt                                                        *time.Time
	AccessFenceVPNBootID                                                  string
	NextAttemptAt, CreatedAt, UpdatedAt                                   time.Time
}
type NewInput struct {
	EventID, TeamID, EventExerciseID uuid.UUID
	Ref                              Ref
	VariantIndex, Generation         int32
	ObjectiveIDs                     []uuid.UUID
	Policy                           Policy
	RetentionUntil                   *time.Time
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func validRef(r Ref) bool {
	return len(r.Group) > 0 && len(r.Group) <= 63 && len(r.Lab) > 0 && len(r.Lab) <= 63 && dnsLabel.MatchString(r.Group) && dnsLabel.MatchString(r.Lab)
}
func New(in NewInput, now time.Time) (Lab, error) {
	seen := map[uuid.UUID]bool{}
	invalid := in.EventID == uuid.Nil || in.TeamID == uuid.Nil || in.EventExerciseID == uuid.Nil || !validRef(in.Ref) || in.VariantIndex < 0 || in.Generation < 0 || len(in.ObjectiveIDs) == 0
	for _, id := range in.ObjectiveIDs {
		if id == uuid.Nil || seen[id] {
			invalid = true
		}
		seen[id] = true
	}
	if invalid {
		return Lab{}, ErrInvalid.Err()
	}
	if err := in.Policy.Validate(); err != nil {
		return Lab{}, err
	}
	return Lab{ID: uuid.Must(uuid.NewV7()), EventID: in.EventID, TeamID: in.TeamID, EventExerciseID: in.EventExerciseID, Ref: in.Ref, VariantIndex: in.VariantIndex, Generation: in.Generation, Revision: 1, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Running", ActualState: "Unknown", SnapshotMode: in.Policy.SnapshotMode, SnapshotState: "Unknown", RetentionUntil: cloneTime(in.RetentionUntil), ObjectiveCount: int32(len(in.ObjectiveIDs)), Allocation: Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}, nil
}
func (l *Lab) Close(reason string, operationID uuid.UUID, now time.Time) error {
	// Solved is irreversible, including attempts to replace its reason.
	if l.CloseReason == "solved" || (l.ClosedAt != nil && l.CloseReason == reason) {
		return nil
	}
	if (reason != "solved" && reason != "manual" && reason != "stage" && reason != "event") || operationID == uuid.Nil || operationID == l.OperationID || l.DesiredState == "Deleted" {
		return ErrCloseInvalid.Err()
	}
	l.DesiredState = "Stopped"
	l.CloseReason = reason
	l.ClosedAt = cloneTime(&now)
	l.Revision++
	l.OperationID = operationID
	l.RuntimeReady = false
	l.UpdatedAt = now
	l.NextAttemptAt = now
	l.FailureCode = ""
	l.FailureMessage = ""
	return nil
}
func (l *Lab) Start(operationID uuid.UUID, now time.Time) error {
	if l.CloseReason == "solved" {
		return ErrSolvedTerminal.Err()
	}
	retainedDefinition := l.ID != uuid.Nil && validRef(l.Ref) && (l.Allocation.StorageState == "Retained" || l.Allocation.StorageState == "None")
	// The persisted observed revision certifies the exact UID/ref/operation/live
	// generation through Observe. Historical Stopped state cannot authorize a
	// new start after an intervening intent changed the desired revision.
	currentStop := l.AgentUID != "" && l.AgentGeneration > 0 && l.ObservedRevision == l.Revision && l.ObservedAt != nil && l.AccessFenced && l.Allocation.RuntimeState == "Released" && l.Allocation.ReleasedAt != nil && l.FailureCode == ""
	currentBarrier := l.SnapshotMode == "skip" || (l.SnapshotMode == "required" && l.SnapshotState == "Succeeded")
	restartableClosure := l.ClosedAt != nil && (l.CloseReason == "manual" || l.CloseReason == "stage")
	if l.DesiredState != "Stopped" || l.ActualState != "Stopped" || !restartableClosure || !currentStop || !retainedDefinition || !currentBarrier || (l.RetentionUntil != nil && !now.Before(*l.RetentionUntil)) {
		return ErrRestartUnavailable.Err()
	}
	if operationID == uuid.Nil || operationID == l.OperationID {
		return ErrOperationInvalid.Err()
	}
	l.Revision++
	l.OperationID = operationID
	l.DesiredState = "Running"
	l.Allocation.RuntimeState = "Unknown"
	l.Allocation.ReleasedAt = nil
	l.CloseReason = ""
	l.ClosedAt = nil
	l.RuntimeReady = false
	l.UpdatedAt = now
	l.NextAttemptAt = now
	l.FailureCode = ""
	l.FailureMessage = ""
	return nil
}

// Observe is the only physical-state mutation. Exact fencing is mandatory for
// every observation, especially release credit; desired intent is untouched.
func (l *Lab) Observe(o Observation, now time.Time) bool {
	if !ObservationMatches(*l, o) || o.DesiredState != l.DesiredState || o.ObservedAt == nil || (l.ObservedAt != nil && !o.ObservedAt.After(*l.ObservedAt)) {
		return false
	}
	l.AgentGeneration = o.Generation
	l.ObservedRevision = o.Revision
	l.ActualState = o.ActualState
	l.SnapshotState = o.SnapshotState
	l.ActualStoppedAt = cloneTime(o.StoppedAt)
	l.ObservedAt = cloneTime(o.ObservedAt)
	l.RuntimeReady = o.RuntimeReady && l.ClosedAt == nil
	l.Allocation = l.mergeAllocation(o)
	l.FailureCode = o.FailureCode
	l.FailureMessage = o.FailureMessage
	l.AccessFenced = o.AccessFenced
	l.AccessFencedAt = cloneTime(o.AccessFencedAt)
	l.AccessFenceVPNBootID = o.AccessFenceVPNBootID
	l.UpdatedAt = now
	return true
}

// ObservationMatches permits an actual newer metadata generation, only after
// the producer has observed that exact live generation for this operation.
func ObservationMatches(l Lab, o Observation) bool {
	return l.AgentUID != "" && l.Ref == o.Ref && l.AgentUID == o.UID && l.OperationID == o.OperationID && l.Revision == o.Revision && o.Generation > 0 && o.ObservedGeneration == o.Generation && o.Generation >= l.AgentGeneration
}
func (l *Lab) MarkMaterialized(now time.Time) { l.Materialized = true; l.UpdatedAt = now }
func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}
