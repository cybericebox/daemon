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
	ErrManualMode          = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithDetailCode(14).WithMessage("Manual laboratory controls require progressive reveal mode")
	ErrManualRevision      = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithDetailCode(15).WithMessage("The laboratory revision changed")
	ErrManualStage         = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithDetailCode(16).WithMessage("The laboratory is outside its available stage")
	ErrManualOwnership     = err.ErrObjectNotFound.WithObjectCode(model.EventLabObjectCode).WithDetailCode(17).WithMessage("Laboratory not found") // category A: foreign and absent identity are indistinguishable
	ErrManualInput         = err.ErrInvalidData.WithObjectCode(model.EventLabObjectCode).WithDetailCode(18).WithMessage("Laboratory command data is invalid")
	ErrManualKeyConflict   = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithDetailCode(19).WithMessage("The idempotency key belongs to another laboratory command")
	ErrManualKeyInProgress = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithDetailCode(20).WithMessage("The laboratory command is still in progress")
	ErrManualLimit         = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithDetailCode(21).WithMessage("The active laboratory limit has been reached")
	ErrManualRetained      = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithDetailCode(22).WithMessage("The retained laboratory state is unavailable")
	ErrManualUnsupported   = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithDetailCode(23).WithMessage("Laboratory lifecycle controls are unavailable")
	ErrManualState         = err.ErrConflict.WithObjectCode(model.EventLabObjectCode).WithDetailCode(24).WithMessage("The laboratory cannot perform this command in its current state")
	ErrManualSolved        = err.ErrForbidden.WithObjectCode(model.EventLabObjectCode).WithDetailCode(25).WithMessage("A solved laboratory is terminal")
)
