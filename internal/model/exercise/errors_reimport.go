package exerciseModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

var (
	// multi-site: malformed/missing/extra choice keys are one invalid target-plan fact.
	ErrImportSelectionInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseImportObjectCode).
		WithMessage("Import targets do not match the archive items").WithDetailCode(1)
	ErrImportTargetDuplicate = err.ErrInvalidData.WithObjectCode(model.ExerciseImportObjectCode).
		WithMessage("An exercise cannot receive several items from one archive").WithDetailCode(2)
	ErrImportSourceInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseImportObjectCode).
		WithMessage("Choose exactly one archive source").WithDetailCode(3)
	// multi-site: in-task import has one source item and one immutable destination.
	ErrImportFixedTargetInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseImportObjectCode).
		WithMessage("In-task import requires one archive item and its fixed target").WithDetailCode(4)
)
