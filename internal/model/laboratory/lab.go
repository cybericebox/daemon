package laboratoryModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
)

type (
	LaboratoryInfo struct {
		ID   uuid.UUID
		CIDR string
	}

	LaboratoryChallenge struct {
		ID        uuid.UUID
		Instances []exerciseModel.Instance
	}

	FlagVariable struct {
		LabID       uuid.UUID
		ChallengeID uuid.UUID
		InstanceID  uuid.UUID
		Flag        string
		Variable    string
	}
)

var (
	ErrLaboratory = err.ErrInternal.WithObjectCode(model.LaboratoryObjectCode)
)
