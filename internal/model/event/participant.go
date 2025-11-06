package eventModel

import (
	"time"

	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

type (
	Participant struct {
		UserID   uuid.UUID     `validate:"required,uuid"`
		EventID  uuid.UUID     `validate:"required,uuid"`
		TeamID   uuid.NullUUID `validate:"omitempty,uuid"`
		TeamName string        `validate:"omitempty,min=3,max=50,alphanum"`

		Name  string `validate:"required,min=3,max=255,alphanum"`
		Email string `validate:"required,email"`

		ApprovalStatus int32 `validate:"required,number,oneof=0 1 2"`
		Hidden         bool  `validate:"required,boolean"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	ParticipantInfo struct {
		UserID  uuid.UUID     `validate:"required,uuid"`
		EventID uuid.UUID     `validate:"required,uuid"`
		TeamID  uuid.NullUUID `validate:"omitempty,uuid"`
		Name    string        `validate:"required,min=3,max=255,alphanum"`
		Email   string        `validate:"required,email"`
	}
)

var (
	ErrEventParticipantExists = err.ErrObjectExists.WithObjectCode(model.EventParticipantObjectCode).WithMessage("Participant already exists").WithDetailCode(1) // 41601

	ErrEventParticipantNotFound     = err.ErrObjectNotFound.WithObjectCode(model.EventParticipantObjectCode).WithMessage("Participant not found").WithDetailCode(1)      // 31601
	ErrEventParticipantTeamNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventParticipantObjectCode).WithMessage("Participant team not found").WithDetailCode(2) // 31602
)
