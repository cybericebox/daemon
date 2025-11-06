package laboratoryModel

import (
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/exercise"
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
