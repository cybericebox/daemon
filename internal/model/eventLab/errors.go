package eventLabModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
	"net/http"
)

// next free detail code: 14
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
