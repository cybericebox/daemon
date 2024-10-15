package model

import (
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/gofrs/uuid"
)

type (
	LaboratoryInfo struct {
		ID   uuid.UUID
		CIDR string
	}

	LaboratoryChallenge struct {
		ID        uuid.UUID
		Instances []Instance
	}
)

var (
	ErrLaboratory = appError.ErrInternal.WithObjectCode(laboratoryObjectCode)
)
