// Package broadcastModel is the domain of custom broadcasts: one message an
// admin or an Event manager authors and sends to a chosen audience.
package broadcastModel

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Status is the lifecycle of a broadcast.
type Status string

const (
	StatusSending Status = "sending"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// AudienceKind selects who receives a broadcast.
type AudienceKind string

const (
	// Platform scope.
	KindAll   AudienceKind = "all"
	KindRoles AudienceKind = "roles"
	KindUsers AudienceKind = "users"
	// Event scope.
	KindAllParticipants AudienceKind = "all_participants"
	KindApproved        AudienceKind = "approved"
	KindPending         AudienceKind = "pending"
	KindCaptains        AudienceKind = "captains"
	KindTeams           AudienceKind = "teams"
	KindParticipants    AudienceKind = "participants"
	KindStaff           AudienceKind = "staff"
)

// MaxSelected bounds a hand-picked audience (users, teams, participants).
const MaxSelected = 1000

// MaxTextLen bounds the subject and the in-app title.
const MaxTextLen = 200

// Audience is the audience definition stored with a broadcast.
type Audience struct {
	Kind    AudienceKind `json:"kind"`
	Roles   []string     `json:"roles,omitempty"`
	UserIDs []uuid.UUID  `json:"user_ids,omitempty"`
	TeamIDs []uuid.UUID  `json:"team_ids,omitempty"`
}

// Validate checks the audience against the scope it is sent in.
func (a Audience) Validate(eventScoped bool) error {
	invalid := func(msg string) error { return notificationModel.ErrBroadcastInvalid.WithMessage(msg).Err() }
	if eventScoped {
		switch a.Kind {
		case KindAllParticipants, KindApproved, KindPending, KindCaptains, KindStaff:
		case KindTeams:
			if len(a.TeamIDs) == 0 || len(a.TeamIDs) > MaxSelected {
				return invalid("Select at least one team")
			}
		case KindParticipants:
			if len(a.UserIDs) == 0 || len(a.UserIDs) > MaxSelected {
				return invalid("Select at least one participant")
			}
		default:
			return invalid("Unknown audience for an Event broadcast")
		}
		return nil
	}
	switch a.Kind {
	case KindAll:
	case KindRoles:
		if len(a.Roles) == 0 {
			return invalid("Select at least one role")
		}
		for _, role := range a.Roles {
			if !rbac.ValidRole(role) {
				return invalid("Unknown role")
			}
		}
	case KindUsers:
		if len(a.UserIDs) == 0 || len(a.UserIDs) > MaxSelected {
			return invalid("Select at least one user")
		}
	default:
		return invalid("Unknown audience for a platform broadcast")
	}
	return nil
}

// Content is the authored message. The email part is a block array like a
// template body; the in-app part is a title, an HTML body and one link.
type Content struct {
	Channels     []notificationTypes.NotificationChannel
	Subject      string
	Preheader    string
	EmailBody    json.RawMessage
	EmailStyling json.RawMessage
	InAppTitle   string
	InAppBody    string
	InAppLink    string
}

// Validate checks that every chosen channel has its content and that the
// variables used are the ones a broadcast provides.
func (c Content) Validate() error {
	invalid := func(msg string) error { return notificationModel.ErrBroadcastInvalid.WithMessage(msg).Err() }
	if len(c.Channels) == 0 {
		return invalid("Select at least one channel")
	}
	seen := map[notificationTypes.NotificationChannel]bool{}
	for _, ch := range c.Channels {
		if !notificationTypes.Supports(notificationTypes.NotificationTypeBroadcast, ch) || seen[ch] {
			return invalid("Unsupported channel")
		}
		seen[ch] = true
	}
	if seen[notificationTypes.NotificationChannelEmail] {
		if strings.TrimSpace(c.Subject) == "" || len([]rune(c.Subject)) > MaxTextLen {
			return invalid("Email subject is required")
		}
		var blocks []json.RawMessage
		if err := json.Unmarshal(c.EmailBody, &blocks); err != nil || len(blocks) == 0 {
			return invalid("Email body is required")
		}
		if err := notificationTypes.ValidateEmailBodyVariables(notificationTypes.NotificationTypeBroadcast, c.EmailBody); err != nil {
			return invalid(err.Error())
		}
		if err := notificationTypes.ValidateTemplateVariables(notificationTypes.NotificationTypeBroadcast, notificationTypes.NotificationChannelEmail, c.Subject, c.Preheader); err != nil {
			return invalid(err.Error())
		}
	}
	if seen[notificationTypes.NotificationChannelInApp] {
		if strings.TrimSpace(c.InAppTitle) == "" || len([]rune(c.InAppTitle)) > MaxTextLen {
			return invalid("In-app title is required")
		}
		if err := notificationTypes.ValidateTemplateVariables(notificationTypes.NotificationTypeBroadcast, notificationTypes.NotificationChannelInApp, c.InAppTitle, c.InAppBody, c.InAppLink); err != nil {
			return invalid(err.Error())
		}
	}
	return nil
}

// HasChannel reports whether the broadcast goes out on ch.
func (c Content) HasChannel(ch notificationTypes.NotificationChannel) bool {
	return slices.Contains(c.Channels, ch)
}

// Broadcast is a stored broadcast with its delivery counters.
type Broadcast struct {
	ID             uuid.UUID
	ScopeEventID   *uuid.UUID
	EventName      string
	CreatedBy      *uuid.UUID
	CreatedByName  string
	Content        Content
	Audience       Audience
	RecipientCount int32
	SentCount      int64
	FailedCount    int64
	Status         Status
	CreatedAt      time.Time
	FinishedAt     *time.Time
}

// Recipient is one resolved audience member.
type Recipient struct {
	ID        uuid.UUID
	Email     string
	FirstName string
	LastName  string
}

// Delivery is one recipient's outcome on one channel.
type Delivery struct {
	DispatchID      uuid.UUID
	RecipientUserID uuid.UUID
	RecipientEmail  string
	DispatchStatus  string
	Channel         string
	TargetStatus    string
	Error           string
}

// ListFilter is the history query. Scope is "" (every scope), "platform" or an
// Event id.
type ListFilter struct {
	Scope  string
	Cursor string
	Limit  int32
}
