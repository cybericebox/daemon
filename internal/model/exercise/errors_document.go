package exerciseModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// Errors of the exercise document cib-exercise/1.0 (the check of a task, the keys and requirements of tasks, the
// runtime keys of a device and the variables). Each names the place in the public context: task (a name), device (a
// name), key, field (a document path under that task or device) or reference.
var (
	// ErrTaskKeyInvalid: a task key is not a slug (a-z, 0-9, _; starts with a letter; up to 32) or repeats in a variant.
	ErrTaskKeyInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("A task key must be 1-32 characters: lowercase a-z, 0-9 and '_', starting with a letter, and unique in the variant").
				WithDetailCode(1)
	// ErrTaskRequiresInvalid: requires names an unknown task, the task itself, a task twice, or makes a cycle.
	ErrTaskRequiresInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("A task may wait only for other tasks of its variant, without repeats and cycles").
				WithDetailCode(2)
	// ErrTaskCheckInvalid: a task has neither an answer nor a state, both, or another kind than its twin in
	// another variant.
	ErrTaskCheckInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("A task needs exactly one check, an answer or a state, of the same kind in every variant").
				WithDetailCode(3)
	// ErrAnswerInvalid: a key of check.answer is wrong; field names it.
	ErrAnswerInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("The answer of the task is invalid").
				WithDetailCode(4)
	// ErrStateInvalid: a key of check.state is wrong, or a condition names a device that is not a node of the
	// variant; field names it.
	ErrStateInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
			WithMessage("The state check of the task is invalid").
			WithDetailCode(5)
	// ErrDeviceRuntimeInvalid: command, args, system, hardware, terminal or bootstrap of a device is invalid; field
	// names the key.
	ErrDeviceRuntimeInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("A runtime setting of the device is invalid").
				WithDetailCode(6)
	// ErrVariableInvalid: a variable of the infrastructure is invalid; field names it.
	ErrVariableInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("A variable of the infrastructure is invalid").
				WithDetailCode(7)
	// ErrSubstitutionUnresolved: a {{task.<key>}} or {{var.<name>}} is malformed or names nothing; field is the text
	// that holds it and reference the substitution.
	ErrSubstitutionUnresolved = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
					WithMessage("A substitution names a task or a variable that does not exist").
					WithDetailCode(8)
	// ErrRuntimeNotSupported: the lab would start without something the document asks for (files, script, command,
	// terminal, a state check); features lists it.
	ErrRuntimeNotSupported = err.ErrConflict.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("The laboratory cannot apply everything this task asks for yet").
				WithDetailCode(9)
	// ErrRuntimeDeviceUnresolved: a resolved bootstrap names a device the topology does not have.
	ErrRuntimeDeviceUnresolved = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
					WithMessage("The device of a resolved bootstrap is not in the topology").
					WithDetailCode(10)
)
