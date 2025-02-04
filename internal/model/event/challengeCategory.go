package eventModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	ChallengeCategory struct {
		ID      uuid.UUID `validate:"omitempty,uuid"`
		EventID uuid.UUID `validate:"required,uuid"`

		Name  string `validate:"required,min=3,max=50,alphanum"`
		Order int32  `validate:"required,number"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	ChallengeCategoryInfo struct {
		ID         uuid.UUID
		Name       string
		Challenges []*ChallengeInfo
	}
)

var (
	ErrEventChallengeCategory = err.ErrInternal.WithObjectCode(model.EventChallengeCategoryObjectCode)

	ErrEventChallengeCategoryCategoryExists = err.ErrObjectExists.WithObjectCode(model.EventChallengeCategoryObjectCode).WithMessage("Event challenge category already exists").WithDetailCode(1) // 41501

	ErrEventChallengeCategoryCategoryNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventChallengeCategoryObjectCode).WithMessage("Event challenge category not found").WithDetailCode(1) // 31501

	ErrEventChallengeCategoryCategoryHasChallenges = err.ErrConflict.WithObjectCode(model.EventChallengeCategoryObjectCode).WithMessage("Event challenge category has challenges").WithDetailCode(1) // 71501
	ErrEventChallengeCategoryCategoryDataStale     = err.ErrConflict.WithObjectCode(model.EventChallengeCategoryObjectCode).WithMessage("Event challenge category data is stale").WithDetailCode(2)  // 71502
)
