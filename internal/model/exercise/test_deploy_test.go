package exerciseModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestTestDeploy_StaticIsTheRowShape(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	id, ver, variant, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	s := exerciseModel.NewStaticTestDeploy(id, ver, variant, owner, nil, now, 2*time.Hour)
	if !s.IsStatic() || s.Kind() != exerciseModel.DeployKindStatic || !s.ExpiresAt.Equal(now.Add(2*time.Hour)) || !s.CreatedAt.Equal(now) {
		t.Fatalf("%+v", s)
	}
	if (exerciseModel.TestDeploy{GroupName: "tu-x", LabName: "l-1"}).IsStatic() {
		t.Fatal("lab row")
	}
	ev := uuid.Must(uuid.NewV7())
	if (exerciseModel.TestDeploy{EventID: &ev}).IsStatic() {
		t.Fatal("event preview rows are never static")
	}
	if (exerciseModel.TestDeploy{GroupName: "tu-x", LabName: "l-1"}).Kind() != exerciseModel.DeployKindLab {
		t.Fatal("lab kind")
	}
}
