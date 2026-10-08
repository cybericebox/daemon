package event

import (
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"strconv"
	"time"
)

type ParticipantLabView struct {
	ID, EventExerciseID uuid.UUID
	Revision            string
	LogicalClosed       bool
	CloseReason         *string
	ClosedAt            *time.Time
	RuntimeState        string
	CanStop, CanRestart bool
	SnapshotPolicy      string
	RetentionUntil      *time.Time
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
