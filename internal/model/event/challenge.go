package eventModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	Challenge struct {
		ID         uuid.UUID `validate:"omitempty,uuid"`
		EventID    uuid.UUID `validate:"required,uuid"`
		CategoryID uuid.UUID `validate:"required,uuid"`

		Data ChallengeData `validate:"required"`

		ExerciseID     uuid.UUID `validate:"required,uuid"`
		ExerciseTaskID uuid.UUID `validate:"required,uuid"`

		Order int32 `validate:"required,number"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	ChallengeData struct {
		Name          string                       `validate:"required,min=3,max=50,alphanum"`
		Description   string                       `validate:"required,min=1"`
		Points        int32                        `validate:"required,min=1,max=1000"`
		AttachedFiles []exerciseModel.ExerciseFile `validate:"omitempty,dive"`
	}

	Order struct {
		ID         uuid.UUID `validate:"required,uuid"`
		CategoryID uuid.UUID `validate:"omitempty,uuid"`
		Index      int32     `validate:"required,number"`
	}

	ChallengeInfo struct {
		ID            uuid.UUID
		Name          string
		Description   string
		Points        int32
		AttachedFiles []exerciseModel.ExerciseFile

		Solved bool
	}
)

var (
	ErrEventChallenge = err.ErrInternal.WithObjectCode(model.EventChallengeObjectCode)

	ErrEventChallengeChallengeExists = err.ErrObjectExists.WithObjectCode(model.EventChallengeObjectCode).WithMessage("Event challenge already exists").WithDetailCode(1) // 41401

	ErrEventChallengeChallengeNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventChallengeObjectCode).WithMessage("Event challenge not found").WithDetailCode(1) // 31401

)
