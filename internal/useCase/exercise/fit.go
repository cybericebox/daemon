package exercise

import (
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// fitChecker is the optional capability of the infrastructure port that knows the agents' resource limits.
type fitChecker interface {
	TopologyFit(topo exerciseModel.Topology) infraModel.TopologyFit
	DeviceLimits() (infraModel.LimitsFeature, bool)
}

// versionView is the version for the editor: secrets masked, and which variants some agent cannot run.
func (u *ExerciseUseCase) versionView(v exerciseModel.ExerciseVersion) VersionView {
	view := toVersionView(v)
	view.Fit = u.variantFits(v.Variants)
	return view
}

// variantFits returns the variants that do not fit every enabled agent that reported its limits.
func (u *ExerciseUseCase) variantFits(variants []exerciseModel.Variant) []VariantFit {
	checker, ok := u.infra.(fitChecker)
	if !ok {
		return nil
	}
	var out []VariantFit
	for _, variant := range variants {
		fit := checker.TopologyFit(variant.Topology)
		if len(fit.Warnings) > 0 {
			out = append(out, VariantFit{VariantID: variant.ID, FitsAny: fit.FitsAny, Warnings: fit.Warnings})
		}
	}
	return out
}

// requireVariantsFit refuses a variant that no enabled agent can run, naming the device and the limit.
func (u *ExerciseUseCase) requireVariantsFit(variants []exerciseModel.Variant) error {
	for _, fit := range u.variantFits(variants) {
		if fit.FitsAny {
			continue
		}
		worst := fit.Warnings[0].FitViolation
		for _, w := range fit.Warnings {
			if w.Max > worst.Max {
				worst = w.FitViolation
			}
		}
		return infraModel.ErrTopologyExceedsAgents.WithContext("variant", fit.VariantID.String()).WithContext("device", worst.Device).
			WithContext("resource", worst.Resource).WithContext("requested", worst.Requested).WithContext("max", worst.Max).Err()
	}
	return nil
}

// DeviceLimits is the most any enabled agent allows a device and a lab, for the editor's hints; known is
// false while no agent has reported its limits.
func (u *ExerciseUseCase) DeviceLimits() (infraModel.LimitsFeature, bool) {
	if checker, ok := u.infra.(fitChecker); ok {
		return checker.DeviceLimits()
	}
	return infraModel.LimitsFeature{}, false
}
