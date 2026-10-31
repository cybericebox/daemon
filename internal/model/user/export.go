package userModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

// DataExport is everything the platform keeps about one person, as the «Завантажити мої дані» file. It holds
// only the person's own data: no secrets (no password hash, key material or tokens), and other people appear
// nowhere — a team is shown by its title alone.
type DataExport struct {
	ExportedAt time.Time `json:"ExportedAt"`
	Profile    ExportProfile
	// SignInMethods lists how the person can sign in: the kind, the name they gave it, and dates.
	SignInMethods        []ExportSignInMethod
	Sessions             []ExportSession
	Participations       []ExportParticipation
	EventStaffRoles      []ExportEventRole
	FlagSubmissions      []ExportFlagSubmission
	FormAnswers          []ExportFormAnswer
	Inbox                []ExportInboxItem
	NotificationSettings []ExportNotificationSetting
}

type ExportProfile struct {
	ID             uuid.UUID
	Email          string
	FirstName      string
	LastName       string
	Role           string
	Status         string
	EmailConfirmed bool
	CreatedAt      time.Time
	LastSeen       time.Time
	TosAcceptedAt  *time.Time
	TosVersion     *int32
}

type ExportSignInMethod struct {
	// Type is "password", "google" or "passkey".
	Type       string
	Name       string `json:",omitempty"`
	CreatedAt  *time.Time
	LastUsedAt *time.Time `json:",omitempty"`
}

// ExportSession is one of the person's active sessions; the address is theirs (the session IP rule).
type ExportSession struct {
	UserAgent string
	IP        string `json:",omitempty"`
	CreatedAt time.Time
	LastSeen  time.Time
}

type ExportParticipation struct {
	EventID         uuid.UUID
	EventName       string
	EventTag        string
	Status          int16
	TeamName        string `json:",omitempty"`
	TeamRole        *int16 `json:",omitempty"`
	IsCaptain       bool
	Pseudonym       string `json:",omitempty"`
	Invited         bool
	InvitedToTeam   bool
	InvitedTeamName string `json:",omitempty"`
	InvitationSent  *time.Time
	JoinedAt        time.Time
	DecidedAt       *time.Time
}

type ExportEventRole struct {
	EventID   uuid.UUID
	EventName string
	Role      int16
	GrantedAt time.Time
}

type ExportFlagSubmission struct {
	EventID       uuid.UUID
	EventName     string
	TeamName      string
	ChallengeName string
	Answer        string
	Correct       bool
	Practice      bool
	ReceivedAt    time.Time
}

type ExportFormAnswer struct {
	EventID       uuid.UUID
	EventName     string
	FormVersionID uuid.UUID
	Answers       json.RawMessage
	SubmittedAt   time.Time
}

type ExportInboxItem struct {
	Title     string
	Body      string
	Link      string `json:",omitempty"`
	Type      string
	Category  string
	EventName string `json:",omitempty"`
	CreatedAt time.Time
	ReadAt    *time.Time
}

type ExportNotificationSetting struct {
	Type    string
	Channel string
	Enabled bool
}
