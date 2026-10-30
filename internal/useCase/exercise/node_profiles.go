package exercise

import (
	"context"

	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
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

// requireProfilesSupported refuses a version with a node whose profile the platform does not support (save, publish
// and import all pass it).
func (u *ExerciseUseCase) requireProfilesSupported(ctx context.Context, variants []exerciseModel.Variant) error {
	return u.SupportedProfiles(ctx).UnsupportedProfile(variants)
}
