package exercise

import (
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// fitInfra is an infrastructure port that only knows the agents' resource limits.
type fitInfra struct {
	IInfrastructure
	agents map[string]infraModel.LimitsFeature // by agent name
}

func (f fitInfra) TopologyFit(topo exerciseModel.Topology) infraModel.TopologyFit {
	need := infraModel.PlacementNeed{Labs: []infraModel.Demand{infraModel.DemandOf(topo)}}
	fit := infraModel.TopologyFit{}
	for name, l := range f.agents {
		if v := l.Fits(need); v != nil {
			fit.Warnings = append(fit.Warnings, infraModel.FitWarning{Agent: name, FitViolation: *v})
		} else {
			fit.FitsAny = true
		}
	}
	return fit
}

func (f fitInfra) DeviceLimits() (infraModel.LimitsFeature, bool) {
	return infraModel.LimitsFeature{DeviceMaxCPUMillicores: 4000}, len(f.agents) > 0
}

func variantWith(cpu string) exerciseModel.Variant {
	return exerciseModel.Variant{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "img", Resources: &exerciseModel.DeviceResources{CPULimit: cpu},
	}}}}
}

func TestVariantsThatNoAgentCanRunWarnAndBlockPublishing(t *testing.T) {
	u := &ExerciseUseCase{infra: fitInfra{agents: map[string]infraModel.LimitsFeature{
		"small": {DeviceMaxCPUMillicores: 500}, "big": {DeviceMaxCPUMillicores: 4000},
	}}}
	fits, partial, nobody := variantWith("250m"), variantWith("2"), variantWith("8")
	fitList := u.variantFits([]exerciseModel.Variant{fits, partial, nobody})
	if len(fitList) != 2 || fitList[0].VariantID != partial.ID || !fitList[0].FitsAny || fitList[1].VariantID != nobody.ID || fitList[1].FitsAny {
		t.Fatalf("fit = %+v", fitList)
	}
	if fitList[0].Warnings[0].Agent != "small" || fitList[0].Warnings[0].Max != 500 || fitList[0].Warnings[0].Requested != 2000 {
		t.Fatalf("the warning names the agent and the limit: %+v", fitList[0].Warnings)
	}
	if err := u.requireVariantsFit([]exerciseModel.Variant{fits, partial}); err != nil {
		t.Fatalf("a variant some agent can run is publishable: %v", err)
	}
	if err := u.requireVariantsFit([]exerciseModel.Variant{fits, nobody}); !errors.Is(err, infraModel.ErrTopologyExceedsAgents.Err()) {
		t.Fatalf("a variant no agent can run = %v", err)
	}
	if limits, known := u.DeviceLimits(); !known || limits.DeviceMaxCPUMillicores != 4000 {
		t.Fatalf("limits = %+v %v", limits, known)
	}
	// Without an infrastructure that knows limits nothing is checked.
	bare := &ExerciseUseCase{}
	if bare.variantFits([]exerciseModel.Variant{nobody}) != nil || bare.requireVariantsFit([]exerciseModel.Variant{nobody}) != nil {
		t.Fatal("no limits known: no check")
	}
}
