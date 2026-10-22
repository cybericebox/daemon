package eventContentModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// EventObjectCode — content pages are an event-owned resource. Detail codes
// continue after the event aggregate's own codes and remain stable for API
// clients. Event detail codes: next free detail code: 40 (37 retired); 55-59 are
// the live screens (see eventModel errors: next free 60).
var (
	ErrPageInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
			WithMessage("Event page is invalid").WithDetailCode(10)
	ErrPageNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
			WithMessage("Event page not found").WithDetailCode(11)
	ErrPageExists = err.ErrObjectExists.WithObjectCode(model.EventObjectCode).
			WithMessage("Event page with this slug already exists").WithDetailCode(12)
	ErrLiveLayoutInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event live layout is invalid").WithDetailCode(13)
	ErrLiveDraftNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
				WithMessage("Event live layout draft not found").WithDetailCode(21)
	ErrLiveDraftInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event live layout draft is invalid; save it again before publishing").WithDetailCode(22)
	ErrPageDraftNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
				WithMessage("Event page has no unpublished changes").WithDetailCode(24)
	ErrPageModified = err.ErrConflict.WithObjectCode(model.EventObjectCode).
			WithMessage("Event page changed while publishing; reload and try again").WithDetailCode(25)
	ErrLandingDraftNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
				WithMessage("Event landing has no unpublished changes").WithDetailCode(26)
	ErrLiveScreenLinkExpiryInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("Choose how long the live screen link works; «until the event ends» needs a future finish").WithDetailCode(34)
	ErrLiveScreenLinkNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
					WithMessage("The event has no working live screen link").WithDetailCode(35)
	// multi-site (category A, security-indistinguishable): unknown, revoked,
	// expired or malformed tokens all read the same; the reason goes to logs.
	ErrLiveScreenTokenInvalid = err.ErrForbidden.WithObjectCode(model.EventObjectCode).
					WithMessage("The live screen link is invalid or expired").WithDetailCode(36)
	ErrLiveLogoTypeInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("The live screen logo must be an SVG, PNG or WebP image").WithDetailCode(38)
	ErrLiveLogoTooLarge = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("The live screen logo must be at most 1 MB").WithDetailCode(39)
	ErrLiveScreenNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
				WithMessage("Event live screen not found").WithDetailCode(55)
	ErrLiveScreenLimit = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("An event has at most 10 live screens").WithDetailCode(56)
	ErrLiveScreenLast = err.ErrConflict.WithObjectCode(model.EventObjectCode).
				WithMessage("The only live screen of the event cannot be deleted").WithDetailCode(57)
	ErrLiveScreenNameInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("A live screen name has 1 to 40 characters").WithDetailCode(58)
	ErrLiveScreenOrderInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("The live screen order must list every screen of the event once").WithDetailCode(59)
	ErrPageDraftSlugTaken = err.ErrObjectExists.WithObjectCode(model.EventObjectCode).
				WithMessage("Another event page already uses this address").WithDetailCode(27)
)
