package platformSettingsModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`. Each var has exactly one non-test call site. A refused
// typed value carries the public context `field` (and `min`/`max` for a range)
// so the admin form shows the error under that field.
//
// SettingObjectCode — next free detail code: 8
var (
	ErrSettingNotFound = err.ErrObjectNotFound.WithObjectCode(model.SettingObjectCode).
				WithMessage("Setting not found").
				WithDetailCode(1)
	// ErrSettingValueMissing: a typed setting is saved whole; a field is absent.
	ErrSettingValueMissing = err.ErrInvalidData.WithObjectCode(model.SettingObjectCode).
				WithMessage("A setting value is missing").WithDetailCode(2)
	// ErrSettingUnknownField: a typed setting got a field it does not have.
	ErrSettingUnknownField = err.ErrInvalidData.WithObjectCode(model.SettingObjectCode).
				WithMessage("Unknown setting field").WithDetailCode(3)
	// ErrSettingValueOutOfRange: a value is outside its field's bounds.
	ErrSettingValueOutOfRange = err.ErrInvalidData.WithObjectCode(model.SettingObjectCode).
					WithMessage("The setting value is out of range").WithDetailCode(4)
	// ErrRetentionGraceNotShorter: the deletion grace after the inactivity warning must be shorter than the
	// inactivity period itself.
	ErrRetentionGraceNotShorter = err.ErrInvalidData.WithObjectCode(model.SettingObjectCode).
					WithMessage("The deletion grace must be shorter than the inactivity period").WithDetailCode(5)
	// ErrArchiveEntryOverUnpacked: the archive limits allow one file larger than all files together. Declared here, not
	// in exerciseModel: ExerciseObjectCode has no detail code under 100 left, and 100+ would spill into the next
	// object's full codes.
	ErrArchiveEntryOverUnpacked = err.ErrInvalidData.WithObjectCode(model.SettingObjectCode).
					WithMessage("One file may not be larger than all files together").WithDetailCode(6)
	// ErrTestLeaseOverMax: the longest test run is shorter than one lease (declared here for the same reason).
	ErrTestLeaseOverMax = err.ErrInvalidData.WithObjectCode(model.SettingObjectCode).
				WithMessage("The longest test run may not be shorter than one lease").WithDetailCode(7)
)
