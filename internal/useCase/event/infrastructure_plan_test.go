package event

import (
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestInfrastructurePlan_StaticOnlyStartsWithoutLaboratories(t *testing.T) {
	plan := infrastructurePlan([]exerciseModel.Topology{{}}, false)
	if plan.HasDynamicLabs || plan.LaboratoriesAvailable || plan.RequiresVPN || !plan.CanStart || plan.Reason != nil {
		t.Fatalf("static plan = %+v, want startable with no requirements", plan)
	}
}

func TestInfrastructurePlan_AvailabilityDoesNotDependOnAttachedLabs(t *testing.T) {
	plan := infrastructurePlan(nil, true)
	if !plan.LaboratoriesAvailable || plan.HasDynamicLabs || !plan.CanStart {
		t.Fatalf("plan = %+v, want available laboratories without dynamic exercises", plan)
	}
}

func TestInfrastructurePlan_AnyDynamicVariantRequiresHealthyLaboratories(t *testing.T) {
	plan := infrastructurePlan([]exerciseModel.Topology{
		{},
		{Devices: []exerciseModel.Device{{Name: "target"}}, VPN: exerciseModel.NetworkSpec{Enabled: true}},
	}, false)
	if !plan.HasDynamicLabs || !plan.RequiresVPN || plan.CanStart || plan.Reason == nil || *plan.Reason != "infrastructure_unavailable" {
		t.Fatalf("dynamic plan = %+v, want blocked dynamic/VPN event", plan)
	}
}
