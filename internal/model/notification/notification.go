package notificationModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

var ErrDispatchNotFound = err.ErrObjectNotFound.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Notification dispatch not found").
	WithDetailCode(1)

// TemplateStatus is the lifecycle state of one template version. The version
// family of a notification type (draft / published / unpublished rows) is an
// aggregate whose invariants — one published per type, publish only from
// draft, restore only from saved published or unpublished versions — are
// enforced ATOMICALLY by the Publish*/Rollback* queries; do not re-implement
// them as Go transitions.
type TemplateStatus string

const (
	TemplateStatusDraft       TemplateStatus = "draft"
	TemplateStatusPublished   TemplateStatus = "published"
	TemplateStatusUnpublished TemplateStatus = "unpublished"
)

var ErrTemplateNotFound = err.ErrObjectNotFound.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Notification template not found").
	WithDetailCode(2)

// DetailCode 5 — was 1, which collided with ErrDispatchNotFound (err.As
// compares only DetailCode, so errors.Is conflated a 400 with a 404).
var ErrInvalidTemplateStatus = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Invalid template status").
	WithDetailCode(5)

// ErrInvalidTemplateVariables keeps unusable templates out of storage: a
// template may reference only variables documented by its notification type.
var ErrInvalidTemplateVariables = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Template references unavailable notification variables").
	WithDetailCode(6)

// ErrInboxNotFound is returned when a mark-read targets a notification that does
// not exist or is not owned by the caller (cross-user attempt → clean not-found).
var ErrInboxNotFound = err.ErrObjectNotFound.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Notification not found").
	WithDetailCode(3)

var ErrPresetNotFound = err.ErrObjectNotFound.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Email block preset not found").
	WithDetailCode(4)

// ErrTemplateTypeNotEventScoped keeps account/manager/platform-only
// notification types out of an Event's template and subscription surfaces:
// an Event may only list, preview and override the participant-facing
// signals in notificationTypes.EventScopedTypes().
var ErrTemplateTypeNotEventScoped = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Notification type is not available for event-scoped templates").
	WithDetailCode(7)

// ErrSignalDefaultTypeNotEventScoped: platform signal defaults are the base of
// Event inheritance, so only Event-scoped types are editable through them.
var ErrSignalDefaultTypeNotEventScoped = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Notification type has no platform signal default").
	WithDetailCode(8)

var ErrSignalDefaultChannelUnsupported = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Notification channel is unavailable for this signal type").
	WithDetailCode(9)

var ErrSignalDefaultInvalidAudience = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Invalid notification audience").
	WithDetailCode(10)

// ErrTemplateDraftExists: the Event already has its own version family for
// this notification type (409). Two sites report the same fact on purpose:
// Customize rejects a type the Event already overrides, and the Event template
// write classifier maps the one-draft-per-(type, scope) unique index
// violation (a concurrent customize/create) to it.
var ErrTemplateDraftExists = err.ErrObjectExists.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Event already has its own template for this notification type").
	WithDetailCode(11)

// ErrInvalidTemplateStyling keeps unrenderable styling out of storage: styling
// must be a JSON object of strings whose theme:* colour tokens are known.
var ErrInvalidTemplateStyling = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Template styling is invalid").
	WithDetailCode(12)

// ErrTemplateImageInvalid: an email template image is unusable — an upload
// that is not a decodable PNG/JPEG/GIF (SVG included), or an image block whose
// file_id is not a UUID of an existing uploaded file. Both are raised through
// the email use case's single templateImageInvalid helper.
var ErrTemplateImageInvalid = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Template image is invalid").
	WithDetailCode(13)

// ErrTemplateImageTooLarge: the uploaded image is above the raw upload cap or
// still above the per-image limit after processing.
// multi-site: the use case (raw cap and post-processing size, through its
// templateImageTooLarge helper) and the HTTP upload handler's
// http.MaxBytesReader boundary check report the same public fact.
var ErrTemplateImageTooLarge = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Template image is too large").
	WithDetailCode(14)

// ErrTemplateInlineTooLarge: a template's inline images (uploaded files plus
// the logo) exceed the per-email inline payload cap, checked at publish time.
var ErrTemplateInlineTooLarge = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Template inline images exceed the per-email size limit").
	WithDetailCode(15)

// ErrTemplatePreviewInvalid: a template draft sent to the preview endpoint
// cannot be rendered (template syntax error, variable without a value,
// malformed block JSON). The preview is requested on every edit, so a
// half-typed draft is ordinary input, not a platform failure; the message
// carries the renderer's reason for the editor to show.
var ErrTemplatePreviewInvalid = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("Template cannot be rendered").
	WithDetailCode(16)

// ErrPresetNested: a block preset uses another preset (or itself). Presets are building blocks of a
// template; letting them include each other makes loops possible.
var ErrPresetNested = err.ErrInvalidData.
	WithObjectCode(model.NotificationObjectCode).
	WithMessage("A block preset cannot contain another preset").
	WithDetailCode(26)
