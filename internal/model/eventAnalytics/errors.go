package eventAnalyticsModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// EventAnalyticsObjectCode — next free detail code: 8
var (
	// ErrEventAnalyticsForbidden: the viewer has no analytics access to the
	// event. A missing event looks the same (no event enumeration).
	ErrEventAnalyticsForbidden = err.ErrForbidden.WithObjectCode(model.EventAnalyticsObjectCode).
					WithMessage("User cannot view the analytics of this event").WithDetailCode(1)
	// ErrEventAnalyticsSensitiveForbidden: wrong answers and integrity are
	// for the owner, write moderators and platform admins only (§7).
	ErrEventAnalyticsSensitiveForbidden = err.ErrForbidden.WithObjectCode(model.EventAnalyticsObjectCode).
						WithMessage("Only the event owner, write moderators and platform admins can view answers and integrity signals").WithDetailCode(2)
	ErrEventAnalyticsPeriodInvalid = err.ErrInvalidData.WithObjectCode(model.EventAnalyticsObjectCode).
					WithMessage("The analytics period is invalid").WithDetailCode(3)
	// ErrEventAnalyticsReportNotReady: the event report exists only after
	// the finish (§6.8).
	ErrEventAnalyticsReportNotReady = err.ErrConflict.WithObjectCode(model.EventAnalyticsObjectCode).
					WithMessage("The event report is available after the event finishes").WithDetailCode(4)
	// ErrEventAnalyticsSolveNotFound: the solve to review is not one of the
	// event's solves.
	ErrEventAnalyticsSolveNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventAnalyticsObjectCode).
					WithMessage("The solve was not found in this event").WithDetailCode(5)
	ErrEventAnalyticsReviewNoteTooLong = err.ErrInvalidData.WithObjectCode(model.EventAnalyticsObjectCode).
						WithMessage("The review note is too long").WithDetailCode(6)
	// ErrEventAnalyticsDismissalInvalid: the signal kind cannot be dismissed,
	// or the key or scope does not fit it.
	ErrEventAnalyticsDismissalInvalid = err.ErrInvalidData.WithObjectCode(model.EventAnalyticsObjectCode).
						WithMessage("This integrity pattern cannot be dismissed").WithDetailCode(7)
)

// MaxReviewNoteLength bounds an organizer's note on a solve (characters).
const MaxReviewNoteLength = 1000
