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
	Retirement *RetirementObservation
	Creation   *CreationReceipt
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
type CreateEvidence struct {
	Ref                                                Ref
	GroupUID, NamespaceUID, DefinitionHash, CreationID string
	OperationID                                        uuid.UUID
	Revision                                           int64
	DeploymentGeneration                               int32
}
type Lab struct {
	CreateEvidence                                                        *CreateEvidence
	RuntimeStageID                                                        *uuid.UUID
	RuntimeStageKnown                                                     bool
	DefinitionVersionID                                                   uuid.UUID
	DefinitionHash                                                        string
	RetirementStopTarget                                                  *Target
	RetirementState, RetirementError                                      string
	RetirementObservedAt                                                  *time.Time
	ID, EventID, TeamID, EventExerciseID                                  uuid.UUID
	Ref                                                                   Ref
	RetentionMinutes                                                      int32
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
	RuntimeStageID                   *uuid.UUID
	RuntimeStageKnown                bool
	DefinitionVersionID              uuid.UUID
	DefinitionHash                   string
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
	return Lab{RuntimeStageID: cloneUUID(in.RuntimeStageID), RuntimeStageKnown: in.RuntimeStageKnown, DefinitionVersionID: in.DefinitionVersionID, DefinitionHash: in.DefinitionHash, ID: uuid.Must(uuid.NewV7()), EventID: in.EventID, TeamID: in.TeamID, EventExerciseID: in.EventExerciseID, Ref: in.Ref, VariantIndex: in.VariantIndex, Generation: in.Generation, Revision: 1, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Running", ActualState: "Unknown", RetentionMinutes: in.Policy.RetentionMinutes, SnapshotMode: in.Policy.SnapshotMode, SnapshotState: "Unknown", RetentionUntil: cloneTime(in.RetentionUntil), ObjectiveCount: int32(len(in.ObjectiveIDs)), Allocation: Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}, nil
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
	deadline := l.RetentionUntil
	if l.ProtectedUntil != nil && (deadline == nil || l.ProtectedUntil.After(*deadline)) {
		deadline = l.ProtectedUntil
	}
	restartableClosure := l.ClosedAt != nil && (l.CloseReason == "manual" || l.CloseReason == "stage")
	if l.DesiredState != "Stopped" || l.ActualState != "Stopped" || !restartableClosure || !currentStop || !retainedDefinition || !currentBarrier || (deadline != nil && !now.Before(*deadline)) {
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
	if l.DesiredState == "Deleted" && l.RetirementStopTarget != nil {
		return l.ObserveRetirement(o, now)
	}
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

// ReadyForAccess is the single managed canonical acknowledgement predicate.
// Generic Ready, including historical revision1 rows, is never an operation receipt.
func (l *Lab) ReadyForAccess() bool {
	return l.DesiredState == "Running" && l.ClosedAt == nil && l.ActualState == "Running" && l.RuntimeReady && l.AgentUID != "" && l.AgentGeneration > 0 && l.OperationID != uuid.Nil && l.Revision > 0 && l.ObservedRevision == l.Revision && l.ObservedAt != nil && l.Allocation.RuntimeState == "Allocated" && l.Allocation.ObservedAt != nil
}

func cloneUUID(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	v := *id
	return &v
}

// CaptureRuntimeStage keeps the preparation's original runtime boundary when an
// authorized upcoming set assignment moves to another stage.
func (l *Lab) CaptureRuntimeStage(stageID *uuid.UUID, now time.Time) {
	if !l.RuntimeStageKnown {
		l.RuntimeStageID = cloneUUID(stageID)
		l.RuntimeStageKnown = true
		l.UpdatedAt = now
	}
}
func (l *Lab) AuthorizeRuntimeStage(stageID uuid.UUID, now time.Time) bool {
	if stageID == uuid.Nil || l.CloseReason == "solved" || l.DesiredState == "Deleted" {
		return false
	}
	l.RuntimeStageID = cloneUUID(&stageID)
	l.RuntimeStageKnown = true
	l.UpdatedAt = now
	return true
}
func (l *Lab) ApplyBoundaryRetention(base time.Time, override *int32, now time.Time) {
	minutes := l.RetentionMinutes
	if override != nil {
		minutes = *override
	}
	l.SetRetentionDeadline(base.Add(time.Duration(minutes)*time.Minute), now)
}
func (l *Lab) RestartBudgetCandidate() Lab {
	candidate := *l
	candidate.DesiredState = "Running"
	candidate.RuntimeReady = false
	candidate.Allocation.RuntimeState = "Admitted"
	candidate.Allocation.ReleasedAt = nil
	candidate.ClosedAt = nil
	return candidate
}

// CreationReceipt is immutable identity evidence, never runtime/storage release.
type CreationReceipt struct {
	GroupUID, NamespaceUID, DefinitionHash, CreationID, LabUID string
	OperationID                                                uuid.UUID
	Revision                                                   int64
	Committed                                                  bool
}

func (l *Lab) RecordCreateDispatch(groupUID, definitionHash string, initialOperation uuid.UUID, now time.Time) bool {
	if l.DesiredState != "Running" || l.ClosedAt != nil || l.Revision != 1 || l.OperationID != initialOperation || initialOperation == uuid.Nil || groupUID == "" || definitionHash == "" || !l.Allocation.ConfiguredRequestsKnown || l.Allocation.RuntimeState != "Admitted" {
		return false
	}
	if e := l.CreateEvidence; e != nil {
		return e.Ref == l.Ref && e.GroupUID == groupUID && e.DefinitionHash == definitionHash && e.OperationID == initialOperation && e.Revision == 1 && e.DeploymentGeneration == l.Generation
	}
	l.CreateEvidence = &CreateEvidence{Ref: l.Ref, GroupUID: groupUID, DefinitionHash: definitionHash, OperationID: initialOperation, Revision: 1, DeploymentGeneration: l.Generation}
	l.UpdatedAt = now
	return true
}
func (l *Lab) AdoptCreationReceipt(o Observation, now time.Time) bool {
	e, b := l.CreateEvidence, o.Creation
	if e == nil || b == nil || l.AgentUID != "" || l.DesiredState == "Deleted" || o.Ref != l.Ref || e.Ref != l.Ref || e.DeploymentGeneration != l.Generation || !b.Committed || e.GroupUID == "" || b.GroupUID != e.GroupUID || b.NamespaceUID == "" || b.CreationID == "" || e.DefinitionHash == "" || b.DefinitionHash != e.DefinitionHash || b.OperationID != e.OperationID || b.Revision != e.Revision || e.OperationID == uuid.Nil || e.Revision != 1 || b.LabUID == "" || o.UID != b.LabUID || o.Generation <= 0 {
		return false
	}
	if e.CreationID != "" && e.CreationID != b.CreationID {
		return false
	}
	if e.NamespaceUID != "" && e.NamespaceUID != b.NamespaceUID {
		return false
	}
	// Copy before mutation so a caller's prior entity remains immutable CAS input.
	copy := *e
	copy.NamespaceUID = b.NamespaceUID
	copy.CreationID = b.CreationID
	l.CreateEvidence = &copy
	l.AgentUID = b.LabUID
	l.AgentGeneration = o.Generation
	l.UpdatedAt = now
	return true
}

// CanRetryInitialDeployment never changes the admitted aggregate or its pins.
func (l *Lab) CanRetryInitialDeployment() bool {
	return l.Materialized && l.DesiredState == "Running" && l.ClosedAt == nil && l.CloseReason == "" && l.Revision == 1 && l.OperationID != uuid.Nil && l.AgentUID == "" && l.AgentGeneration == 0 && l.CreateEvidence == nil && !l.RuntimeReady
}
