package event

import (
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// limitsInfra answers TopologyFit from one cap on a device's CPU; everything else of the port stays nil.
type limitsInfra struct {
	Infrastructure
	maxCPU int64
}

func (l limitsInfra) TopologyFit(topo exerciseModel.Topology) infraModel.TopologyFit {
	need := infraModel.PlacementNeed{Labs: []infraModel.Demand{infraModel.DemandOf(topo)}}
	limits := infraModel.LimitsFeature{DeviceMaxCPUMillicores: l.maxCPU}
	if v := limits.Fits(need); v != nil {
		return infraModel.TopologyFit{Warnings: []infraModel.FitWarning{{Agent: "a", FitViolation: *v}}}
	}
	return infraModel.TopologyFit{FitsAny: true}
}

func TestVersionFitsListsTheVariantsNoLaboratoryCanRun(t *testing.T) {
	heavy := exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "img", Resources: &exerciseModel.DeviceResources{CPULimit: "2"},
	}}}}
	light := exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "img"}}}}
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{light, heavy}}
	u := &EventUseCase{infra: limitsInfra{maxCPU: 500}}
	fits := u.versionFits(version)
	if len(fits) != 1 || fits[0].VariantIndex != 1 || fits[0].FitsAny || fits[0].Warnings[0].Max != 500 {
		t.Fatalf("fits = %+v", fits)
	}
	if got := (&EventUseCase{}).versionFits(version); got != nil {
		t.Fatalf("no infrastructure, no check: %+v", got)
	}
}
