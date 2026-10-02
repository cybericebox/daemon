package inboxModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

// Category is the inbox tab an item belongs to. It is computed on the server
// from (notification type × recipient role) and stored per row at insert, so
// clients never guess it.
type Category string

const (
	// CategoryRequests: somebody waits for the recipient's decision or action.
	CategoryRequests Category = "requests"
	// CategoryPersonal: about the recipient and their own actions.
	CategoryPersonal Category = "personal"
	// CategoryActivity: life of events the recipient takes part in or manages.
	CategoryActivity Category = "activity"
)

// ParseCategory validates an API filter value.
func ParseCategory(s string) (Category, bool) {
	switch c := Category(s); c {
	case CategoryRequests, CategoryPersonal, CategoryActivity:
		return c, true
	}
	return "", false
}

// RecipientRole is why a recipient receives a notification.
type RecipientRole string

const (
	// RoleSubject: the person the fact is about (applicant, assignee, the
	// notified manager of a per-recipient signal).
	RoleSubject RecipientRole = "subject"
	// RoleParticipant: a member of an Event-wide audience.
	RoleParticipant RecipientRole = "participant"
	// RoleManager: an Event owner, moderator or viewer.
	RoleManager RecipientRole = "manager"
	// RoleAdmin: a platform administrator.
	RoleAdmin RecipientRole = "admin"
)

// Resolution records how an action-required request was closed.
type Resolution string

const (
	ResolutionApproved Resolution = "approved"
	ResolutionRejected Resolution = "rejected"
	// ResolutionFixed: the failed laboratory was re-created.
	ResolutionFixed Resolution = "fixed"
	// ResolutionResolved: a recipient closed the request by hand.
	ResolutionResolved Resolution = "resolved"
	// ResolutionExpired: the Event finished before anyone decided.
	ResolutionExpired Resolution = "expired"
	// ResolutionWithdrawn: the applicant deleted their account.
	ResolutionWithdrawn Resolution = "withdrawn"
)

// Notification types that exist only as inbox messages (no signal of the same
// name): the manager-facing copy of a registration application and the
// exercise catalog proposal flow.
const (
	TypeApplicationSubmitted = "event.application.submitted"
	TypeProposalSubmitted    = "exercise.proposal.submitted"
	TypeProposalApproved     = "exercise.proposal.approved"
	TypeProposalRejected     = "exercise.proposal.rejected"
	// The resource elevation flow: an author asks to take task devices above the platform frame, the platform
	// admins decide.
	TypeElevationRequested = "exercise.elevation.requested"
	TypeElevationApproved  = "exercise.elevation.approved"
	TypeElevationRejected  = "exercise.elevation.rejected"
	// TypeResultsPublished: a moderator opened the Event results.
	TypeResultsPublished = "participant.event.results_published"
)

// categoryRules is the (type × role) matrix. A role missing from a type's row
// falls back to that row's "" entry, and an unknown type is personal.
var categoryRules = map[string]map[RecipientRole]Category{
	"participant.approval_registration.submitted": {"": CategoryPersonal},
	TypeApplicationSubmitted:                      {"": CategoryRequests},
	"event.lab.failed":                            {"": CategoryRequests},
	TypeProposalSubmitted:                         {RoleAdmin: CategoryRequests, "": CategoryPersonal},
	TypeProposalApproved:                          {"": CategoryPersonal},
	TypeProposalRejected:                          {"": CategoryPersonal},
	TypeElevationRequested:                        {RoleAdmin: CategoryRequests, "": CategoryPersonal},
	TypeElevationApproved:                         {"": CategoryPersonal},
	TypeElevationRejected:                         {"": CategoryPersonal},
	"participant.event.start_reminder":            {"": CategoryActivity},
	"participant.event.finished":                  {"": CategoryActivity},
	TypeResultsPublished:                          {"": CategoryActivity},
	"event.manager.assigned":                      {"": CategoryPersonal},
	// A broadcast is an announcement: activity for an Event audience,
	// personal for a platform one.
	"broadcast": {RoleParticipant: CategoryActivity, "": CategoryPersonal},
}

// Classify returns the inbox category of a notification type for a recipient
// role. account.* and every other type not in the matrix are personal.
func Classify(notificationType string, role RecipientRole) Category {
	rules, ok := categoryRules[notificationType]
	if !ok {
		return CategoryPersonal
	}
	if c, ok := rules[role]; ok {
		return c
	}
	return rules[""]
}

// manuallyResolvable are the request types a recipient may close by hand;
// every other request closes only through its domain decision.
var manuallyResolvable = map[string]bool{"event.lab.failed": true}

// ManuallyResolvable reports whether a request of this type may be closed via
// the inbox resolve endpoint.
func ManuallyResolvable(notificationType string) bool {
	return manuallyResolvable[notificationType]
}

// Subject references tie every recipient's copy of one request together, so
// resolving the object closes the request for all of them.

func ApplicationRef(eventID, userID uuid.UUID) string {
	return "application:" + eventID.String() + ":" + userID.String()
}

func StandRef(eventID, teamID uuid.UUID) string {
	return "stand:" + eventID.String() + ":" + teamID.String()
}

func ElevationRef(elevationID uuid.UUID) string {
	return "elevation:" + elevationID.String()
}

func ProposalRef(proposalID uuid.UUID) string {
	return "proposal:" + proposalID.String()
}

// EventApplicationsPattern matches (SQL LIKE) every application of an Event.
func EventApplicationsPattern(eventID uuid.UUID) string {
	return "application:" + eventID.String() + ":%"
}

// UserApplicationsPattern matches (SQL LIKE) every application of a user.
func UserApplicationsPattern(userID uuid.UUID) string {
	return "application:%:" + userID.String()
}

// Meta is the inbox classification of one delivery. ActionRequired is true
// exactly for requests; SubjectRef is kept only on requests.
type Meta struct {
	Type           string        `json:"type"`
	Category       Category      `json:"category"`
	ActionRequired bool          `json:"action_required"`
	SubjectRef     string        `json:"subject_ref,omitempty"`
	Role           RecipientRole `json:"role,omitempty"`
	// RaisedAt is when the request arose (the signal time); a decision taken
	// at or after it resolves a copy that is delivered later.
	RaisedAt *time.Time `json:"raised_at,omitempty"`
}

// Raised returns m with the moment its request arose.
func (m Meta) Raised(at time.Time) Meta {
	m.RaisedAt = &at
	return m
}

// NewMeta classifies a delivery of notificationType to a recipient in role.
func NewMeta(notificationType string, role RecipientRole, subjectRef string) Meta {
	category := Classify(notificationType, role)
	m := Meta{Type: notificationType, Category: category, Role: role}
	if category == CategoryRequests {
		m.ActionRequired = true
		m.SubjectRef = strings.TrimSpace(subjectRef)
	}
	return m
}

// Counts are the per-tab badge numbers. Requests counts open requests (and
// unread legacy requests); Personal and Activity count unread items; All is
// their sum.
type Counts struct {
	All      int64
	Requests int64
	Personal int64
	Activity int64
}
