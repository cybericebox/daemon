package exerciseModel

import (
	"github.com/gofrs/uuid"
	"time"
)

type TestDeploy struct {
	ID, VersionID, VariantID, CreatedBy uuid.UUID
	// GroupName is the author's lab group (shared by all their test labs); LabName is this deploy's Lab in it.
	GroupName, LabName   string
	CreatedAt, ExpiresAt time.Time
	// Flags are the test values injected into the linked devices, kept for the author.
	Flags []DeployFlag
	// Solved are the tasks the author has checked correctly in this deploy.
	Solved []uuid.UUID
	// ExerciseID is the exercise of VersionID; filled when deploys are listed, not stored.
	ExerciseID uuid.UUID
}
