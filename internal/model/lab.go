package model

import "github.com/gofrs/uuid"

type (
	LabInfo struct {
		ID   uuid.UUID
		CIDR string
	}

	LabChallenge struct {
		ID        uuid.UUID
		Instances []Instance
	}
)
