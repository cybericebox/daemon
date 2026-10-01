package notificationModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// Notification errors declared here are listed in the API error catalog
// (tools/errorcatalog scans errors.go); the older ones live in
// notification.go and share the same detail-code space.
// NotificationObjectCode — next free detail code: 25
var (
	// ErrInboxRequestNotFound: the item does not exist or the caller is not
	// one of its recipients (cross-user attempt → clean not-found).
	ErrInboxRequestNotFound = err.ErrObjectNotFound.WithObjectCode(model.NotificationObjectCode).
				WithMessage("Inbox request not found").WithDetailCode(17)
	// ErrInboxRequestNotResolvable: the item is not an action-required request,
	// or its type closes only through its own decision (an application or a
	// proposal is approved or rejected, not dismissed).
	ErrInboxRequestNotResolvable = err.ErrInvalidData.WithObjectCode(model.NotificationObjectCode).
					WithMessage("This notification cannot be resolved manually").WithDetailCode(18)
	// ErrInboxRequestResolved: somebody already resolved the request.
	ErrInboxRequestResolved = err.ErrConflict.WithObjectCode(model.NotificationObjectCode).
				WithMessage("Inbox request is already resolved").WithDetailCode(19)
)

// Broadcast errors. NotificationObjectCode — next free detail code: 25
var (
	ErrBroadcastNotFound = err.ErrObjectNotFound.WithObjectCode(model.NotificationObjectCode).
				WithMessage("Broadcast not found").WithDetailCode(20)
	ErrBroadcastInvalid = err.ErrInvalidData.WithObjectCode(model.NotificationObjectCode).
				WithMessage("Invalid broadcast").WithDetailCode(21)
	ErrBroadcastNoRecipients = err.ErrInvalidData.WithObjectCode(model.NotificationObjectCode).
					WithMessage("The audience has no recipients").WithDetailCode(22)
)

// Site banner errors.
var (
	ErrBannerNotFound = err.ErrObjectNotFound.WithObjectCode(model.NotificationObjectCode).
				WithMessage("Banner not found").WithDetailCode(23)
	ErrBannerInvalid = err.ErrInvalidData.WithObjectCode(model.NotificationObjectCode).
				WithMessage("Invalid banner").WithDetailCode(24)
)
