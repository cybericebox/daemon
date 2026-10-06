package infrastructure

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

func TestInfrastructureStatusWithoutAgentIsExplicitlyUnavailable(t *testing.T) {
	uc := NewInfrastructureUseCase(Dependencies{})
	status, err := uc.InfrastructureStatus(context.Background())
	if err != nil {
		t.Fatalf("InfrastructureStatus() error = %v", err)
	}
	if status.Connected || status.Healthy || status.Capabilities.Laboratories {
		t.Fatalf("unexpected available status: %+v", status)
	}
	if status.Mode != AvailabilityMode("missing_config") || status.Warning == nil || status.Warning.Code != "infrastructure_unavailable" {
		t.Fatalf("missing explicit unavailable state: %+v", status)
	}
	got := uc.RequireLaboratories(context.Background())
	var typed appErr.Error
	if got == nil || !errors.As(got, &typed) || typed.StatusCode().HTTPCode() != http.StatusServiceUnavailable || !typed.StatusCode().As(infraModel.ErrInfrastructureUnavailable.Err().StatusCode()) {
		t.Fatalf("RequireLaboratories() = %v, want typed 503 infrastructure unavailable", got)
	}
}

// Compile-time guard: the optional port remains able to express topology work.
var _ Agent = (*testAgent)(nil)

type testAgent struct{}

func (*testAgent) Health(context.Context) error { return model.ErrPlatform.Err() }
func (*testAgent) DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error {
	return nil
}
func (*testAgent) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{}, nil
}
func (*testAgent) DeleteLab(context.Context, string, string) error       { return nil }
func (*testAgent) DeleteLabClient(context.Context, string, string) error { return nil }
func (*testAgent) LabClientHandshake(context.Context, string, string) (time.Time, error) {
	return time.Time{}, nil
}
func (*testAgent) EnsureLabClient(context.Context, string, string) (string, error) { return "", nil }
func (*testAgent) EnsureVPNGroup(context.Context, string) error                    { return nil }
func (*testAgent) ReconcileLabGroupAccess(context.Context, string, []labAccessModel.ClientPolicy) error {
	return nil
}
func (*testAgent) DestroyLabGroup(context.Context, string) error { return nil }
