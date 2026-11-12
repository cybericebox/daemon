package exercise

import (
	"context"

	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// SupportedProfiles are the node profiles the platform supports (the setting «Профілі вузлів»); the container only
// while none is saved or when the setting cannot be read.
func (u *ExerciseUseCase) SupportedProfiles(ctx context.Context) exerciseModel.SupportedProfiles {
	if u.settings == nil {
		return exerciseModel.DefaultSupportedProfiles()
	}
	supported, err := u.settings.NodeProfiles(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Node profiles cannot be read, the container only applies")
		return exerciseModel.DefaultSupportedProfiles()
	}
	return supported
}

// LabCapabilitiesView is what the laboratories can run, for the catalog editor: the aggregate of the agents that are used
// (infraModel.CapabilitiesOf) and the node profiles the platform promises. It names no agent and carries no count.
type LabCapabilitiesView struct {
	infraModel.LabCapabilities
	// Profiles are the node profiles (container, microvm, vm): "all" when the platform setting ticks the profile, else "none".
	// The setting is the platform's promise and the same for every laboratory; whether one agent can run a profile right now
	// is not part of it.
	Profiles map[string]infraModel.Availability
}

// LabCapabilities reads the cached features of the agents (no call is made). Without an infrastructure that reports them,
// or before any agent has reported, every option is "unknown".
func (u *ExerciseUseCase) LabCapabilities(ctx context.Context) LabCapabilitiesView {
	view := LabCapabilitiesView{LabCapabilities: infraModel.CapabilitiesOf(nil), Profiles: map[string]infraModel.Availability{}}
	if reporter, ok := u.infra.(interface {
		LabCapabilities() infraModel.LabCapabilities
	}); ok {
		view.LabCapabilities = reporter.LabCapabilities()
	}
	supported := u.SupportedProfiles(ctx)
	for _, profile := range exerciseModel.NodeProfiles {
		view.Profiles[string(profile)] = infraModel.AvailabilityNone
		if supported.Has(profile) {
			view.Profiles[string(profile)] = infraModel.AvailabilityAll
		}
	}
	return view
}

// requireProfilesSupported refuses a version with a node whose profile the platform does not support (save, publish
// and import all pass it).
func (u *ExerciseUseCase) requireProfilesSupported(
	ctx context.Context,
	variants []exerciseModel.Variant,
) error {
	return u.SupportedProfiles(ctx).UnsupportedProfile(variants)
}
