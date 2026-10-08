package eventLabModel

import (
	"github.com/gofrs/uuid"
	"time"
)

type AccessTarget struct {
	Group, ExpectedGroupUID string
	OperationID             uuid.UUID
	Revision                int64
}
type AccessFenceObservation struct {
	Group, ExpectedGroupUID, PolicyUID                               string
	OperationID                                                      uuid.UUID
	DesiredRevision, AppliedRevision, Generation, ObservedGeneration int64
	State, VPNBootID                                                 string
	ObservedAt                                                       time.Time
	CurrentVPNBootID                                                 string
	VPNObservedAt                                                    time.Time
	LastError                                                        string
}

func AccessFenceMatches(want AccessTarget, got AccessFenceObservation, currentVPNBootID string) bool {
	return want.Group != "" && want.ExpectedGroupUID != "" && want.OperationID != uuid.Nil && want.Revision > 0 &&
		got.Group == want.Group && got.ExpectedGroupUID == want.ExpectedGroupUID && got.PolicyUID != "" &&
		got.OperationID == want.OperationID && got.DesiredRevision == want.Revision && got.AppliedRevision == want.Revision &&
		got.State == "Applied" && got.Generation > 0 && got.Generation == got.ObservedGeneration && got.LastError == "" &&
		currentVPNBootID != "" && got.VPNBootID == currentVPNBootID
}

type GroupObservation struct {
	Name, UID, DesiredState, ActualState, FailureCode, FailureMessage string
	Revision, ObservedRevision                                        int64
	Ready                                                             bool
	ObservedAt                                                        *time.Time
	Allocation                                                        Allocation
}
