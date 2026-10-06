package eventModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/exercise/errors.go. Enforced by
// `make lint-errors`. Each var has exactly one non-test call site.
//
// EventObjectCode — detail codes 10–13 are reserved by eventContent.
var (
	// multi-site: all read paths + the optimistic-lock re-read (row gone).
	ErrEventNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
				WithMessage("Event not found").WithDetailCode(1)
	// multi-site: reading or submitting the same absent registration form.
	ErrParticipantFormNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
					WithMessage("Participant form not found").WithDetailCode(20)

	// multi-site: the live-tag-uniqueness guard pre-checks on create and
	// update, plus the DB unique-index-violation fallback (classifyEventWriteError)
	// — all three represent the SAME domain fact (a subdomain-tag collision
	// within the active window) and deliberately share one public 409.
	ErrEventExists = err.ErrObjectExists.WithObjectCode(model.EventObjectCode).
			WithMessage("Event with this tag already exists").WithDetailCode(2)

	// optimistic-lock guard hit 0 rows while the row still exists — 409, client reloads.
	ErrEventModified = err.ErrConflict.WithObjectCode(model.EventObjectCode).
				WithMessage("Event was modified concurrently").WithDetailCode(3)

	ErrEventTagInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event tag is invalid").WithDetailCode(4)
	// The tag is a reserved platform subdomain: api, id, admin, exercises, www, or one of EVENT_RESERVED_TAGS_EXTRA.
	ErrEventTagReserved = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event tag is reserved for a platform address").WithDetailCode(42)
	ErrEventDatesInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event archive time must be after availability time").WithDetailCode(5)
	ErrEventNameTooLong = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event name is too long").WithDetailCode(6)
	ErrEventLifecycleInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("Event lifecycle is invalid").WithDetailCode(7)

	// multi-site: participant actions that mutate or activate event runtime.
	ErrEventRuntimeNotOpen = err.ErrConflict.WithObjectCode(model.EventObjectCode).
				WithMessage("Event runtime is not active").WithDetailCode(8)

	ErrEventScoringProfileInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("Event scoring profile is invalid").WithDetailCode(9)
	ErrEventStaticPointsInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("Static scoring needs positive points per task").WithDetailCode(33)
	// multi-site: platform and public names must both be nonempty.
	ErrEventNameInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event name is required").WithDetailCode(14)
	ErrEventLogoTypeInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event logo must be a PNG, JPEG, or WebP image").WithDetailCode(15)
	ErrEventLogoTooLarge = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event logo exceeds the 2 MB limit").WithDetailCode(16)
	ErrEventPreviewPictureTypeInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
						WithMessage("Event preview picture must be a PNG, JPEG, or WebP image").WithDetailCode(17)
	ErrEventPreviewPictureTooLarge = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("Event preview picture exceeds the 5 MB limit").WithDetailCode(18)
	// multi-site: invalid staged branding kind, action, or draft ownership.
	ErrEventBrandDraftInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("Event brand draft is invalid or expired").WithDetailCode(19)
	// multi-site: ErrEventInfrastructureUnavailable rejects allowing
	// infrastructure (at creation or before publication) while Laboratory is
	// not connected.
	ErrEventInfrastructureUnavailable = err.ErrConflict.WithObjectCode(model.EventObjectCode).
						WithMessage("Infrastructure challenges need a connected laboratory").WithDetailCode(23)
	// ErrEventInfrastructureLocked: the flag changes only before publication.
	ErrEventInfrastructureLocked = err.ErrConflict.WithObjectCode(model.EventObjectCode).
					WithMessage("Infrastructure can be changed only before the event is published").WithDetailCode(40)
	// ErrEventInfrastructureInUse: sets with infrastructure are still attached.
	ErrEventInfrastructureInUse = err.ErrConflict.WithObjectCode(model.EventObjectCode).
					WithMessage("Detach the exercises that need infrastructure before turning it off").WithDetailCode(41)

	// Files attached to «Файл» questions of participant and team answers.
	ErrAnswerFileFieldInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("The question does not take a file").WithDetailCode(28)
	ErrAnswerFileTypeInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("The file type is not allowed for this question").WithDetailCode(29)
	ErrAnswerFileTooLarge = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("The file exceeds the size limit of this question").WithDetailCode(30)
	// multi-site (category A): a missing file and a file the caller may not
	// read are indistinguishable.
	ErrAnswerFileNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
				WithMessage("Answer file not found").WithDetailCode(31)
	ErrAnswerFileUnavailable = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
					WithMessage("The answer references a file that cannot be used").WithDetailCode(32)

	// Event stages (docs/specs/event-stages.md). EventObjectCode detail codes 43-52.
	ErrEventStageNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).
				WithMessage("Event stage not found").WithDetailCode(43)
	// ErrEventStageInvalid: the name or the times of a stage are not valid.
	ErrEventStageInvalid = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("Event stage name or times are invalid").WithDetailCode(44)
	// ErrEventStageOverlap: the stage would overlap another stage of the event.
	ErrEventStageOverlap = err.ErrConflict.WithObjectCode(model.EventObjectCode).
				WithMessage("Event stage overlaps another stage").WithDetailCode(45)
	ErrEventStageNameExists = err.ErrObjectExists.WithObjectCode(model.EventObjectCode).
				WithMessage("Event stage with this name already exists").WithDetailCode(46)
	// ErrEventStageAnchor: the first stage opens with the event and the last closes with it.
	ErrEventStageAnchor = err.ErrInvalidData.WithObjectCode(model.EventObjectCode).
				WithMessage("The first stage opens at the event start and the last closes at the event finish").WithDetailCode(47)
	// ErrEventStageClosedLocked: a closed stage accepts only a new name and nothing new in it.
	ErrEventStageClosedLocked = err.ErrConflict.WithObjectCode(model.EventObjectCode).
					WithMessage("The stage is closed and cannot be changed").WithDetailCode(48)
	// ErrEventStageOpenedLocked: once a stage has opened, its opening time stays and nothing is removed from it.
	ErrEventStageOpenedLocked = err.ErrConflict.WithObjectCode(model.EventObjectCode).
					WithMessage("The stage has opened: nothing can be removed from it or moved out of it").WithDetailCode(49)
	// ErrEventStageNotDeletable: only an upcoming stage without exercises can be deleted.
	ErrEventStageNotDeletable = err.ErrConflict.WithObjectCode(model.EventObjectCode).
					WithMessage("Only an upcoming stage without exercises can be deleted").WithDetailCode(50)
	// ErrEventStageNeedsFinish: stages need a scheduled event finish.
	ErrEventStageNeedsFinish = err.ErrConflict.WithObjectCode(model.EventObjectCode).
					WithMessage("Stages need a scheduled event start and finish").WithDetailCode(51)
	// ErrEventStageNotOpen: only an open stage can be closed now.
	ErrEventStageNotOpen = err.ErrConflict.WithObjectCode(model.EventObjectCode).
				WithMessage("Only an open stage can be closed now").WithDetailCode(52)
	// ErrEventArchived: an archived event is read-only for every manage write.
	ErrEventArchived = err.ErrConflict.WithObjectCode(model.EventObjectCode).
				WithMessage("Event is archived and read-only").WithDetailCode(53)
)
