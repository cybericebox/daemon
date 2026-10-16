package eventLabModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
	"net/http"
)

// next free detail code: 26
var (
	ErrLinkRebound          = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory binding changed while opening the link").WithDetailCode(12)
	ErrLinkChangedBeforePin = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory changed before opening the link").WithDetailCode(13)
	ErrLinkChanged          = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory changed while opening the link").WithDetailCode(11)
	ErrAccessClosed         = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory is closed").WithDetailCode(9) // category B: canonical lifecycle access guard
	ErrNotFound             = err.ErrObjectNotFound.WithObjectCode(model.EventLabObjectCode).WithMessage("Laboratory not found").WithDetailCode(10)
	ErrInvalid              = err.ErrInvalidData.WithObjectCode(model.EventLabObjectCode).WithMessage("Laboratory lifecycle data is invalid").WithDetailCode(1)
	ErrPolicyInvalid        = err.ErrInvalidData.WithObjectCode(model.EventLabObjectCode).WithMessage("Laboratory policy is invalid").WithDetailCode(2)
	ErrSolvedTerminal       = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithMessage("A solved laboratory cannot be restarted").WithDetailCode(3)
	ErrRestartUnavailable   = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory has no retained stopped state to restart").WithDetailCode(4)
	ErrOperationInvalid     = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("A new laboratory lifecycle operation is required").WithDetailCode(5)
	ErrCloseInvalid         = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("Laboratory closure data is invalid").WithDetailCode(7)
	ErrAssignmentRepair     = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory assignment is being repaired, try again").WithDetailCode(8).WithHTTPCode(http.StatusServiceUnavailable)
	ErrCaptureUnavailable   = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("Required laboratory snapshots are unavailable").WithDetailCode(6)
)

var (
	ErrManualMode          = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithMessage("Manual laboratory controls require progressive reveal mode").WithDetailCode(14)
	ErrManualRevision      = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory revision changed").WithDetailCode(15)
	ErrManualStage         = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory is outside its available stage").WithDetailCode(16)
	ErrManualOwnership     = err.ErrObjectNotFound.WithObjectCode(model.EventLabObjectCode).WithMessage("Laboratory not found").WithDetailCode(17) // category A: foreign and absent identity are indistinguishable
	ErrManualInput         = err.ErrInvalidData.WithObjectCode(model.EventLabObjectCode).WithMessage("Laboratory command data is invalid").WithDetailCode(18)
	ErrManualKeyConflict   = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The idempotency key belongs to another laboratory command").WithDetailCode(19)
	ErrManualKeyInProgress = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory command is still in progress").WithDetailCode(20)
	ErrManualLimit         = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The active laboratory limit has been reached").WithDetailCode(21)
	ErrManualRetained      = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The retained laboratory state is unavailable").WithDetailCode(22)
	ErrManualUnsupported   = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("Laboratory lifecycle controls are unavailable").WithDetailCode(23)
	ErrManualState         = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithMessage("The laboratory cannot perform this command in its current state").WithDetailCode(24)
	ErrManualSolved        = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithMessage("A solved laboratory is terminal").WithDetailCode(25)
)
