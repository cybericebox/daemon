package notificationTypes

import (
	"encoding/json"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// Inbox-only notification types (platform templates, in-app channel): the
// managers' copy of a registration application and the exercise catalog
// proposal flow. They are sent by the inbox request router, not the planner.

type applicationSubmittedPayload struct {
	ScopeEventID    string `var:"scope_event_id" desc:"Event identifier" default:"01900000-0000-7000-8000-000000000000"`
	EventTag        string `var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	EventName       string `var:"event_name" desc:"Event public name" default:"Cyber ICE Box CTF"`
	ApplicantUserID string `var:"applicant_user_id" desc:"Applicant identifier" default:"01900000-0000-7000-8000-000000000002"`
	ApplicantName   string `var:"applicant_name" desc:"Applicant full name (email when unnamed)" default:"Jane Doe"`
	ApplicantEmail  string `var:"applicant_email" desc:"Applicant email" default:"participant@example.org"`
	UserID          string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail       string `var:"user_email" desc:"Recipient email" default:"moderator@example.org"`
	UserFirstName   string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName    string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture     string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName        string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

type proposalPayload struct {
	Type              NotificationType
	ProposalID        string `var:"proposal_id" desc:"Proposal identifier" default:"01900000-0000-7000-8000-000000000005"`
	ExerciseID        string `var:"exercise_id" desc:"Proposed exercise identifier" default:"01900000-0000-7000-8000-000000000006"`
	ExerciseName      string `var:"exercise_name" desc:"Proposed exercise name" default:"Web basics"`
	ProposerName      string `var:"proposer_name" desc:"Proposer full name (email when unnamed)" default:"Jane Doe"`
	Note              string `var:"note" desc:"Proposer note" default:"Ready for the catalog"`
	DecisionNote      string `var:"decision_note" desc:"Reviewer note (decisions only)" default:""`
	CatalogExerciseID string `var:"catalog_exercise_id" desc:"Catalog copy identifier (approval only)" default:""`
	UserID            string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail         string `var:"user_email" desc:"Recipient email" default:"moderator@example.org"`
	UserFirstName     string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName      string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture       string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName          string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

// elevationPayload is the resource elevation flow: the request to the platform admins and the decision to the
// author.
type elevationPayload struct {
	Type          NotificationType
	ElevationID   string `var:"elevation_id" desc:"Elevation request identifier" default:"01900000-0000-7000-8000-000000000007"`
	ExerciseID    string `var:"exercise_id" desc:"Exercise identifier" default:"01900000-0000-7000-8000-000000000006"`
	ExerciseName  string `var:"exercise_name" desc:"Exercise name" default:"Web basics"`
	RequesterName string `var:"requester_name" desc:"Author full name (email when unnamed)" default:"Jane Doe"`
	Reason        string `var:"reason" desc:"Author reason" default:"A database server needs more memory"`
	DecisionNote  string `var:"decision_note" desc:"Admin note (decisions only)" default:""`
	Devices       string `var:"devices" desc:"Requested devices with their values" default:"db: 500m / 2Gi"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"moderator@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (p elevationPayload) NotificationType() NotificationType { return p.Type }
func (elevationPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (elevationPayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

func (applicationSubmittedPayload) NotificationType() NotificationType {
	return inboxModel.TypeApplicationSubmitted
}
func (applicationSubmittedPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (applicationSubmittedPayload) Marshal() (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (p proposalPayload) NotificationType() NotificationType { return p.Type }
func (proposalPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (proposalPayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

func init() {
	Register(applicationSubmittedPayload{})
	for _, typ := range []NotificationType{inboxModel.TypeProposalSubmitted, inboxModel.TypeProposalApproved, inboxModel.TypeProposalRejected} {
		Register(proposalPayload{Type: typ})
	}
	for _, typ := range []NotificationType{inboxModel.TypeElevationRequested, inboxModel.TypeElevationApproved, inboxModel.TypeElevationRejected} {
		Register(elevationPayload{Type: typ})
	}
}
