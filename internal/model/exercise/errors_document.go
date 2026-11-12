package exerciseModel

import (
	"net/http"

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
	// ErrVPNOptionInvalid: reverse_connections is set without vpn.enabled, or on the internet gateway; field names it.
	ErrVPNOptionInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("Reverse connections need the VPN network switched on and cannot be set on the Internet gateway").
				WithDetailCode(20)
	// ErrServiceUserUnknown: a device is started through the launcher (a bootstrap list, a command or arguments) and the
	// platform does not know which user its image declares: an image by reference to an external registry, or a platform
	// image uploaded before its configuration was kept. device and variant name the place, field is "image".
	ErrServiceUserUnknown = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("The user of the image is not known: a device started with a bootstrap or a command needs a platform image").
				WithDetailCode(21)
	// ErrServiceCommandUnknown: a device needs the laboratory told what to run after its bootstrap (or has a command of
	// its own) but neither the author's command nor the image configuration gives one; device names it.
	ErrServiceCommandUnknown = err.ErrConflict.WithObjectCode(model.ExerciseDocumentObjectCode).
					WithMessage("The command to start after the bootstrap of a device is unknown: set the command of the device").
					WithDetailCode(11)
	// ErrLabOptionUnavailable: the target laboratory lacks an option the task needs (checks, terminal, a bootstrap limit);
	// options names it and limit the value when it is a limit.
	ErrLabOptionUnavailable = err.ErrConflict.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("The laboratory does not offer an option this task needs").
				WithDetailCode(12)
	// ErrStateTaskNotForEvent: a task with check.state belongs to the Academy and cannot be part of an event.
	ErrStateTaskNotForEvent = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("A task checked by the state of the lab cannot be used in an event").
				WithDetailCode(13)
	// ErrTaskLocked: the task waits for other tasks (requires) that are not done; waits_for names them.
	ErrTaskLocked = err.ErrConflict.WithObjectCode(model.ExerciseDocumentObjectCode).
			WithMessage("This task opens after the tasks it waits for are done").
			WithDetailCode(14)
	// ErrChecksRunNotAllowed: the task does not allow a manual run of its checks (or has no checks).
	ErrChecksRunNotAllowed = err.ErrConflict.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("This task does not allow a manual check").
				WithDetailCode(15)
	// ErrChecksRunTooOften: the manual run was asked for too often (HTTP 429, Retry-After).
	ErrChecksRunTooOften = err.ErrConflict.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("The check was asked for too often, try again later").
				WithDetailCode(16).
				WithHTTPCode(http.StatusTooManyRequests)
	// ErrSubstitutionNotAllowed: a {{task.<key>}} or {{var.<name>}} is in the description or a hint, which the participant reads;
	// task names the task and field the place (description, hints[N].text).
	ErrSubstitutionNotAllowed = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
					WithMessage("A description or a hint cannot hold a task or variable substitution").
					WithDetailCode(19)
	// ErrBootstrapTooLarge: a bootstrap file is over 256 KiB, or the script and files of a device are over 512 KiB in
	// all; device, field, file (the file's path in the device) and limit (bytes) say where and how much.
	// ErrTestDeployNoConsole: the lab cannot give a console link (it does not have the device, or it cannot say its base domain).
	ErrTestDeployNoConsole = err.ErrObjectNotFound.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("The console of the device is not available").
				WithDetailCode(18)
	ErrBootstrapTooLarge = err.ErrInvalidData.WithObjectCode(model.ExerciseDocumentObjectCode).
				WithMessage("A bootstrap file is over 256 KiB, or the script and files of a device are over 512 KiB").
				WithDetailCode(17)
)
