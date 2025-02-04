package eventModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	Team struct {
		ID      uuid.UUID `validate:"omitempty,uuid"`
		EventID uuid.UUID `validate:"required,uuid"`

		Name     string `validate:"required,min=3,max=50,alphanum"`
		JoinCode string `validate:"-"`

		Hidden         bool  `validate:"required,boolean"`
		ApprovalStatus int32 `validate:"required,number,oneof=0 1 2"`

		ParticipantsCount int64

		LaboratoryID uuid.NullUUID

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	TeamInfo struct {
		ID   uuid.UUID
		Name string
	}
)

var (
	ErrEventTeam = err.ErrInternal.WithObjectCode(model.EventTeamObjectCode)

	ErrEventTeamTeamExists        = err.ErrObjectExists.WithObjectCode(model.EventTeamObjectCode).WithMessage("Team already exists").WithDetailCode(1)  // 41801
	ErrEventTeamUserAlreadyInTeam = err.ErrObjectExists.WithObjectCode(model.EventTeamObjectCode).WithMessage("User already in team").WithDetailCode(2) // 41802

	ErrEventTeamTeamNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventTeamObjectCode).WithMessage("Team not found").WithDetailCode(1) // 31801

	ErrEventTeamWrongCredentials = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).WithMessage("Team wrong credentials").WithDetailCode(1) // 21801
)
