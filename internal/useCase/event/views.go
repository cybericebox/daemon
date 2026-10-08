package event

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

type EventView struct {
	ID                    uuid.UUID
	Tag                   string
	Name                  string
	AvailableFrom         time.Time
	ArchiveAt             time.Time
	Status                eventModel.EventStatus
	LifecycleStatus       eventModel.LifecycleStatus
	InfrastructureAllowed bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type EventManagerView struct {
	UserID    uuid.UUID
	Role      int16
	CreatedAt time.Time
}

type EventsListResult struct {
	Events     []EventView
	NextCursor uuid.UUID
	HasMore    bool
	Total      int64
}

// EventLifecycleView is the manager-facing canonical runtime state. Status is
// derived at read time and cannot be edited directly.
type EventLifecycleView struct {
	Configured     bool
	JoinPolicy     eventModel.JoinPolicy
	PublishAt      time.Time
	StartAt        time.Time
	FinishAt       *time.Time
	WithdrawAt     *time.Time
	Status         eventModel.LifecycleStatus
	UpdatedAt      time.Time
	Infrastructure EventInfrastructurePlan
}

type EventScoringProfileView struct {
	Profile           eventModel.ScoringProfile
	ForceEventScoring bool
	StaticPoints      *int32
	UpdatedAt         time.Time
}

func toEventLifecycleView(e eventModel.Event, now time.Time, infrastructure EventInfrastructurePlan) EventLifecycleView {
	lifecycle := e.Lifecycle
	return EventLifecycleView{
		Configured:     lifecycle.Configured,
		JoinPolicy:     lifecycle.JoinPolicy,
		PublishAt:      lifecycle.PublishAt,
		StartAt:        lifecycle.StartAt,
		FinishAt:       lifecycle.FinishAt,
		WithdrawAt:     lifecycle.WithdrawAt,
		Status:         lifecycle.Status(now),
		UpdatedAt:      e.UpdatedAt,
		Infrastructure: infrastructure,
	}
}

// EventTenantView is the read model ResolveEventByTag hands to its caller —
// just enough of the Event identity/window for tenant resolution. It is a
// use-case-level type (not the delivery/middleware.EventTenant bundle) so
// this package never imports a delivery package; the middleware package
// adapts it (see middleware.NewEventTenantResolver).
type EventTenantView struct {
	EventID       uuid.UUID
	Tag           string
	Public        bool
	AvailableFrom time.Time
	ArchiveAt     time.Time
}

func toEventTenantView(e eventModel.Event, now time.Time) EventTenantView {
	return EventTenantView{
		EventID:       e.ID,
		Tag:           e.Tag,
		Public:        e.Lifecycle.Configured && !now.Before(e.Lifecycle.PublishAt) && (e.Lifecycle.WithdrawAt == nil || now.Before(*e.Lifecycle.WithdrawAt)),
		AvailableFrom: e.AvailableFrom,
		ArchiveAt:     e.ArchiveAt,
	}
}

func toEventView(e eventModel.Event, now time.Time) EventView {
	return EventView{
		ID:                    e.ID,
		Tag:                   e.Tag,
		Name:                  e.InternalName,
		AvailableFrom:         e.AvailableFrom,
		ArchiveAt:             e.ArchiveAt,
		Status:                e.Status(now),
		LifecycleStatus:       e.Lifecycle.Status(now),
		InfrastructureAllowed: e.InfrastructureAllowed,
		CreatedAt:             e.CreatedAt,
		UpdatedAt:             e.UpdatedAt,
	}
}

// EventConfigView is the admin-facing read model for an event's config: all
// mutable settings plus the identity/optimistic-lock fields.
type EventConfigView struct {
	EventID                uuid.UUID
	Participation          *eventConfigModel.Participation
	Registration           eventConfigModel.Registration
	ScoreboardVisibility   eventConfigModel.Visibility
	ParticipantsVisibility eventConfigModel.Visibility
	PreviewDescription     string
	PreviewPicture         string
	MaxTeamSize            int32
	MinTeamSize            *int32
	MaxTeams               *int32
	AllowPseudonyms        bool
	ShowDifficulty         bool
	HintsDisabled          bool
	HintChargeMode         eventConfigModel.HintChargeMode
	MaxFlagAttempts        *int32
	Countdown              eventConfigModel.CountdownSettings
	TaskRevealMode         eventConfigModel.TaskRevealMode
	LabPolicy              eventLabModel.Policy
	Theme                  eventConfigModel.Theme
	StandTiming            eventStandModel.Timing
	UpdatedAt              time.Time
}

// ParticipantEventInfoView contains only capabilities available after the
// caller's event participation has been approved.
type ParticipantEventInfoView struct {
	EventID             uuid.UUID
	UseVPN              bool
	CanViewResults      bool
	ResultsAvailability ResultsAvailability
	CanViewParticipants bool
	// Identity: RealName is the caller's profile name, DisplayName what other
	// participants see (pseudonym when allowed and set).
	Participation     *eventConfigModel.Participation
	RealName          string
	Pseudonym         *string
	DisplayName       string
	AllowPseudonyms   bool
	PseudonymEditable bool
	TeamID            *uuid.UUID
	TeamAdmitted      *bool
	MinTeamSize       int32
	MaxTeamSize       int32
	// Board presentation toggles and whether the event carries lab
	// challenges (infrastructure allowed AND an active exercise has devices).
	ShowDifficulty              bool
	HintsDisabled               bool
	HasInfrastructureChallenges bool
	HintChargeMode              eventConfigModel.HintChargeMode
}

type ParticipantFormView struct {
	Version           int32
	Enabled, Required bool
	Document          eventContentModel.Document
	// RequireExisting asks people who already answered to fill the required
	// fields too; BlockSubmissions also blocks their solution submissions
	// until they did. Answered counts the participants (teams) with answers,
	// filled for the manager editor.
	RequireExisting, BlockSubmissions bool
	Answered                          int64
}

// ForParticipant is the form as a participant may see it: no staff-only
// questions and no manager numbers.
func (v ParticipantFormView) ForParticipant() ParticipantFormView {
	v.Document = v.Document.WithoutStaffOnly()
	v.Answered = 0
	return v
}

type ParticipantFormAnswerView struct {
	UserID      uuid.UUID
	Name, Email string
	FormVersion int32
	Answers     map[string]any
	Document    eventContentModel.Document
	SubmittedAt time.Time
}

type EventFormView struct {
	ID, EventID          uuid.UUID
	Title                string
	Enabled, Required    bool
	CurrentVersionID     uuid.UUID
	Version              int32
	Document             eventContentModel.Document
	CreatedAt, UpdatedAt time.Time
}

type PendingEventFormView struct {
	Form          EventFormView
	FormVersionID uuid.UUID
	Presentation  eventFormModel.Presentation
	Dismissible   bool
	Gates         []eventFormModel.Capability
	CreatedAt     time.Time
}

type EventFormAnswerView struct {
	UserID        uuid.UUID
	Name, Email   string
	FormVersionID uuid.UUID
	Version       int32
	Answers       map[string]any
	Document      eventContentModel.Document
	SubmittedAt   time.Time
}

type EventFormDeliveryView struct {
	FormVersionID uuid.UUID
	UserID        uuid.UUID
	AssignmentID  uuid.UUID
	Presentation  eventFormModel.Presentation
	Dismissible   bool
	Gates         []eventFormModel.Capability
	CompletedAt   *time.Time
	CreatedAt     time.Time
}

// EventContentView is the renderer-neutral response for landing and static
// event pages. Values stay typed so the client owns localisation and live
// countdown rendering.
type EventContentView struct {
	Landing eventContentModel.Document
	// LandingDraft is the saved, unpublished landing; nil when none.
	LandingDraft *eventContentModel.Document
	Live         eventContentModel.LiveLayout
	Variables    map[string]any
}

// EventPageView: the top-level fields are the published version (for a page
// never published, its current content). Draft holds unpublished changes;
// PublishedAt is nil until the first publish.
type EventPageView struct {
	ID              uuid.UUID
	Slug            string
	Title           string
	Document        eventContentModel.Document
	Visibility      eventContentModel.PageVisibility
	Navigation      eventContentModel.PageNavigation
	NavigationOrder int32
	Draft           *eventContentModel.PageDraft
	PublishedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func toEventConfigView(c eventConfigModel.EventConfig) EventConfigView {
	return EventConfigView{
		EventID:                c.EventID,
		Participation:          c.Participation,
		Registration:           c.Registration,
		ScoreboardVisibility:   c.ScoreboardVisibility,
		ParticipantsVisibility: c.ParticipantsVisibility,
		PreviewDescription:     c.PreviewDescription,
		PreviewPicture:         c.PreviewPicture,
		MaxTeamSize:            c.MaxTeamSize,
		MinTeamSize:            c.MinTeamSize,
		MaxTeams:               c.MaxTeams,
		AllowPseudonyms:        c.AllowPseudonyms,
		ShowDifficulty:         c.ShowDifficulty,
		HintsDisabled:          c.HintsDisabled,
		HintChargeMode:         c.HintChargeMode,
		MaxFlagAttempts:        c.MaxFlagAttempts,
		Countdown:              c.Countdown,
		TaskRevealMode:         c.TaskRevealMode,
		LabPolicy:              c.EffectiveLabPolicy(),
		Theme:                  c.Theme,
		StandTiming:            c.StandTiming,
		UpdatedAt:              c.UpdatedAt,
	}
}

// EventInfoView is the participant-facing composition of the canonical event
// lifecycle and participant-visible settings — the "info" view a participant
// sees before/around an event. The legacy tenancy window stays internal to
// tenant routing and must not drive the participant clock.
type EventInfoView struct {
	EventID              uuid.UUID
	Tag                  string
	Name                 string
	StartTime            time.Time
	FinishTime           *time.Time
	Status               eventModel.LifecycleStatus
	Participation        *eventConfigModel.Participation
	Registration         eventConfigModel.Registration
	ScoreboardVisibility eventConfigModel.Visibility
	// ResultsAvailability is the guest view (no manager or participant
	// rights); approved participants read theirs from ParticipantEventInfoView.
	ResultsAvailability    ResultsAvailability
	ParticipantsVisibility eventConfigModel.Visibility
	PreviewDescription     string
	PreviewPicture         string
	LogoURL                string
	FaviconURL             string
	Infrastructure         EventInfrastructurePlan
	Theme                  eventConfigModel.Theme
	// Countdown is the participant countdown policy for the challenges and
	// results pages.
	Countdown eventConfigModel.CountdownSettings
}

func toEventInfoView(e eventModel.Event, now time.Time, c eventConfigModel.EventConfig, infrastructure EventInfrastructurePlan) EventInfoView {
	return EventInfoView{
		EventID:                e.ID,
		Tag:                    e.Tag,
		Name:                   e.Name,
		StartTime:              e.Lifecycle.StartAt,
		FinishTime:             e.Lifecycle.EffectiveFinishAt(),
		Status:                 e.Lifecycle.Status(now),
		Participation:          c.Participation,
		Registration:           c.Registration,
		ScoreboardVisibility:   c.ScoreboardVisibility,
		ResultsAvailability:    decideResultsAvailability(c.ScoreboardVisibility, e.Lifecycle.HasStarted(now), false, false),
		ParticipantsVisibility: c.ParticipantsVisibility,
		PreviewDescription:     c.PreviewDescription,
		PreviewPicture:         c.PreviewPicture,
		Infrastructure:         infrastructure,
		Theme:                  c.Theme,
		Countdown:              c.Countdown,
	}
}

// JoinInfoView reports a user's participation status for an event: the
// status alone is enough for the client to render "not joined" / "pending" /
// "approved" / "rejected".
type JoinInfoView struct {
	Status          participantModel.Status
	Invited         bool
	InvitedTeamName string
	InvitedTeamID   *uuid.UUID
	TeamUnavailable bool
	// InvitationExpired: the registration window (or, for a team invitation,
	// the roster) closed before the invitation was accepted.
	InvitationExpired bool
	// TeamID is the caller's current team (internal: not part of join info DTO).
	TeamID *uuid.UUID
	// Participation is the computed «what can this viewer do now» block the
	// event site renders instead of re-deriving the rules from dates.
	// Set by GetJoinInfo only.
	Participation *ParticipationState

	teamCaptain bool
	teamFormed  bool
	teamMembers int32
}

// OwnTeamView is the participant-facing representation of the caller's
// current competing unit. The join code is intentionally included only for a
// member of that team; list/scoreboard views use a separate public shape.
type OwnTeamView struct {
	ID   uuid.UUID
	Name string
	// JoinCode and JoinCodeExpiresAt are filled for the captain only.
	JoinCode          string
	JoinCodeExpiresAt *time.Time
	CaptainID         uuid.UUID
	MemberCount       int32
	ExtraFields       map[string]any
	Role              participantModel.TeamRole
	Admitted          bool
	MinTeamSize       int32
	MaxTeamSize       int32
	// MissingFields lists the required team fields still unfilled (the
	// organizer asked every team for them); BlockingFields: solutions cannot
	// be submitted until they are.
	MissingFields  []string
	BlockingFields bool
	// Formed and FormedAt: the roster is closed for good; tasks open then.
	Formed   bool
	FormedAt *time.Time
}

type TeamView struct {
	ID                 uuid.UUID
	Name               string
	CaptainID          uuid.UUID
	Hidden             bool
	MemberCount        int32
	ExtraFields        map[string]any
	FieldsMissing      int32 // required team fields left unfilled
	CreatedAt          time.Time
	Members            []TeamMemberView
	PendingInvitations []TeamInvitationView
	Admitted           bool
	AdmittedManually   bool
	MinTeamSize        int32
	// CaptainPending: the captain is an invitee who has not accepted yet.
	CaptainPending bool
	// FormedAt is when the roster closed for good (the start for events without
	// late join); Formed is false while the team is still open.
	FormedAt *time.Time
	Formed   bool
}

// TeamProfileView is the organizer's read-only team profile.
type TeamProfileView struct {
	Team    TeamView
	Results *ManageResultsTeamView
}

// TeamMemberView is the moderator view of a member: real name and pseudonym.
type TeamMemberView struct {
	UserID    uuid.UUID
	Name      string
	Email     string
	Pseudonym *string
	Role      participantModel.TeamRole
	// LastSeenAt is the last request on the event, LastLabAt the last
	// laboratory access over the VPN or the proxy; nil when never.
	LastSeenAt *time.Time
	LastLabAt  *time.Time
}

type TeamInvitationView struct {
	UserID           uuid.UUID
	Name             string
	Email            string
	CreatedAt        time.Time
	InvitationSentAt *time.Time
}

type TeamsListResult struct {
	Teams      []TeamView
	NextCursor uuid.UUID
	HasMore    bool
	Total      int64
}

type EventExerciseView struct {
	ID                uuid.UUID
	ExerciseID        uuid.UUID
	ExerciseName      string
	ExerciseVersionID uuid.UUID
	VariantMode       eventExerciseModel.VariantMode
	FixedVariantIndex *int32
	Revision          int32
	Status            eventExerciseModel.Status
	ReplacesID        *uuid.UUID
	SupersededAt      *time.Time
	DetachedAt        *time.Time
	CreatedAt         time.Time
	// StageID is the stage the set belongs to; nil lives for the whole event.
	StageID *uuid.UUID

	// W4: catalog versions and ownership (manage list).
	Scope               string // catalog | event
	VersionNumber       int32  // ordinal of the pinned version among published ones
	LatestVersionID     *uuid.UUID
	LatestVersionNumber int32
	UpdateAvailable     bool
	Fork                *EventExerciseForkView
	Infrastructure      bool
	VariantCount        int32
	ChallengeCount      int32
	PublishedCount      int32
	HasAttempts         bool
	// Resources is the total of the pinned version: the least and the most it needs over its variants (equal for
	// one variant); planning reserves the largest. ResourceHeavy: an approved elevation holds a device above the
	// platform frame (a badge in the task picker and list). NoAgentFits: no agent that is used can run it; it
	// never names an agent.
	Resources     resourcesModel.Range
	ResourceHeavy bool
	NoAgentFits   bool
}

// EventExerciseForkView describes the catalog source of an event fork.
type EventExerciseForkView struct {
	SourceExerciseID          uuid.UUID
	SourceExerciseName        string
	SourceVersionID           *uuid.UUID
	SourceVersionNumber       int32
	SourceLatestVersionID     *uuid.UUID
	SourceLatestVersionNumber int32
	SourceUpdateAvailable     bool
}

// EventCatalogItem is one published exercise an event may attach.
type EventCatalogItem struct {
	ID                 uuid.UUID
	Name               string
	Description        string
	Tags               []string
	Scope              string
	PublishedVersionID uuid.UUID
	Infrastructure     bool
	Attached           bool
	// Resources is the total of the published version (min and max over its variants); ResourceHeavy: an
	// approved elevation holds a device above the platform frame.
	Resources     resourcesModel.Range
	ResourceHeavy bool
}

// EventCatalogTag is a tag of the event's attachable exercises with how many
// of them carry it.
type EventCatalogTag struct {
	Tag           string
	ExerciseCount int64
}

// ChallengeHintView is a hint as moderators see it on the board settings.
// Cost is the event's price (0 when unset); Overridden says a price is set.
type ChallengeHintView struct {
	ID         uuid.UUID
	Text       string
	Level      string
	Cost       int32
	Overridden bool
}

// HintUnlockView is one team's unlock of one hint (moderators).
type HintUnlockView struct {
	TeamID           uuid.UUID
	TeamName         string
	EventChallengeID uuid.UUID
	ChallengeName    string
	HintID           uuid.UUID
	HintIndex        int
	UnlockedBy       *uuid.UUID
	UnlockedByName   string
	UnlockedAt       time.Time
	Cost             int32
}

// OwnHintView is a hint on the participant board: its text only once the team
// unlocked it.
type OwnHintView struct {
	ID             uuid.UUID
	Level          string
	Cost           int32
	Unlocked       bool
	Content        *string
	UnlockedAt     *time.Time
	UnlockedByName string
}

type EventChallengeView struct {
	ID              uuid.UUID
	TaskID          uuid.UUID
	GroupID         *uuid.UUID
	PrerequisiteIDs []uuid.UUID
	Order           int32
	BoardOrder      *int32
	// Points is the task's own value; EffectivePoints is what teams see and
	// score: the event's static value when the task follows a static event.
	Points          int32
	EffectivePoints int32
	ScoringOverride *eventModel.ScoringProfile
	HintsEnabled    bool
	// MaxFlagAttempts is the task's own limit; nil uses the event's.
	MaxFlagAttempts *int32
	Published       bool
	Availability    ChallengeAvailability
	Snapshot        json.RawMessage
	Hints           []ChallengeHintView
}

// ChallengeAvailability is a moderator-only aggregate. Published answers
// whether the board is configured; these counters answer whether each team's
// actual instance is still preparing, ready, available, or failed.
type ChallengeAvailability struct {
	Preparing int64
	Ready     int64
	Available int64
	Failed    int64
	Total     int64
}

// SubmitChallengeResult intentionally exposes only the participant-safe
// outcome; the expected flag remains exclusively in TeamChallenge storage.
type SubmitChallengeResult struct {
	Correct    bool
	FirstSolve bool
	// Practice: the answer came after a returnable stage closed; it was verified but is not rated.
	Practice bool
}

type OwnChallengeView struct {
	ID, EventChallengeID uuid.UUID
	// Snapshot is reduced to name and difficulty while Locked.
	Snapshot         json.RawMessage
	Readiness        teamChallengeModel.Readiness
	SolvedAt         *time.Time
	Points           int32
	Order            int32
	GroupID          *uuid.UUID
	GroupName        string
	GroupOrder       int32
	ContentUpdatedAt *time.Time
	Infrastructure   bool
	HintsEnabled     bool
	// MaxAttempts is the effective limit of wrong flag submissions for the team and AttemptsLeft what remains of
	// it; both nil when unlimited or already solved.
	MaxAttempts, AttemptsLeft *int32
	// Locked: some prerequisite is not solved by the team (never on the
	// moderators board).
	Locked        bool
	Prerequisites []ChallengePrerequisiteView
	// Files is empty while Locked.
	Files []ChallengeFileView
	// SolveCount is nil when event results are not available to the caller.
	SolveCount *int64
	// Hints (when hints are enabled for the challenge): texts only once
	// unlocked; the moderators board shows every text.
	Hints []OwnHintView
	// HintCostTotal is what the team paid for this challenge's hints.
	HintCostTotal int32
	// BoardPublished is the board publication (meaningful on the moderators
	// board, always true on the participant board).
	BoardPublished bool
	// StageID is the stage of the task's set, nil for a whole-event set. Closed: the stage closed and is not
	// returnable (visible, no submissions, hints or lab). Practice: solved after a returnable stage closed, which
	// the rating does not count.
	StageID  *uuid.UUID
	Closed   bool
	Practice bool
	// AwardedPoints, HintPenalty and SolvedBy are set only on a solved task (nil otherwise): the points the team
	// holds for it now (after the hint penalty; 0 for a practice solve), the hint cost charged to the solve and the
	// member who submitted the accepted answer.
	AwardedPoints, HintPenalty *int32
	SolvedBy                   *SolverView
}

// SolverView is the team member who solved a task.
type SolverView struct {
	UserID uuid.UUID
	Name   string
}

// BoardStageView is an opened stage on the participant board (an upcoming stage is never sent).
type BoardStageView struct {
	ID         uuid.UUID
	Name       string
	OpensAt    time.Time
	ClosesAt   time.Time
	Returnable bool
	State      eventModel.StageState
}

// CurrentStageView is the stage that is open now. EndsAt is set only while the stage countdown is visible (the
// last stage ends with the event, so it has none: its countdown is the event's; the mode and minutes of the
// event's countdown settings decide the rest).
type CurrentStageView struct {
	ID      uuid.UUID
	Name    string
	OpensAt time.Time
	EndsAt  *time.Time
	// Last: the stage ends with the event.
	Last bool
}

// OwnBoardView is the participant board with its stage context. The client refetches at NextChangeAt and counts
// down against ServerNow, so it holds no stage logic of its own.
type OwnBoardView struct {
	Challenges []OwnChallengeView
	Stages     []BoardStageView
	ServerNow  time.Time
	// CurrentStage is nil outside a stage (no stages, a break, before the first one).
	CurrentStage *CurrentStageView
	// NextOpensAt is the start of the next stage, only during a break.
	NextOpensAt *time.Time
	// NextChangeAt is the nearest stage boundary after ServerNow.
	NextChangeAt *time.Time
}

type ChallengePrerequisiteView struct {
	EventChallengeID uuid.UUID
	Name             string
	Solved           bool
}

type ChallengeFileView struct {
	FileID uuid.UUID
	Name   string
	Size   int64
}

// ChallengeSolveView is one team's solve of a board challenge; TeamName is
// the public scoreboard name.
type ChallengeSolveView struct {
	TeamName string
	// NameHidden: the participant's real name was withheld from this viewer (TeamName is empty).
	NameHidden bool
	SolvedAt   time.Time
	Own        bool
	// FirstBlood marks the earliest solve among the visible teams.
	FirstBlood bool
}

// ChallengeSolvesPage is one keyset page of the solvers list. Next is the id
// of the last solve on the page (the cursor of the following page) when
// HasMore is set. Total counts the solves the caller may see.
type ChallengeSolvesPage struct {
	Items   []ChallengeSolveView
	HasMore bool
	Next    uuid.UUID
	Total   int64
}

// TeamRosterMemberView is one member of the caller's team; DisplayName is the
// participant public name (pseudonym when allowed and set).
type TeamRosterMemberView struct {
	UserID      uuid.UUID
	DisplayName string
	Role        participantModel.TeamRole
	Own         bool
	// Pending marks an invitee who has not accepted the team invitation yet.
	Pending bool
}

// OwnParticipantAnswersView is the caller's registration answers with the
// latest participant form and whether they can still be edited.
type OwnParticipantAnswersView struct {
	Form     ParticipantFormView
	Answers  map[string]any
	Editable bool
	// Missing lists the required fields the caller must still fill (the
	// organizer asked everyone for them); Blocking: solutions cannot be
	// submitted until they do.
	Missing  []string
	Blocking bool
}

type ScoreboardEntryView struct {
	Rank     int32
	TeamID   uuid.UUID
	TeamName string
	// NameIsReal: TeamName is a participant's real name; NameHidden: it was withheld from this viewer (TeamName is empty).
	NameIsReal  bool
	NameHidden  bool
	Points      int64
	Solved      int64
	LastSolveAt *time.Time
}

type OwnScoreTimelineEntryView struct {
	EventChallengeID uuid.UUID
	Points           int32
	SolvedAt         time.Time
}

type EventScoreTimelineEntryView struct {
	EventTeamID      uuid.UUID
	EventChallengeID uuid.UUID
	ChallengeName    string
	Points           int32
	SolvedAt         time.Time
}

type ResultsSnapshotView struct {
	Revision    int64
	GeneratedAt time.Time
	Scoreboard  []ScoreboardEntryView
	Timeline    []EventScoreTimelineEntryView
	// TotalTeams counts the ranked teams before the page row limit.
	TotalTeams int
	Freeze     ResultsFreezeView
	Display    ResultsDisplayView
}

// ResultsFreezeView is the scoreboard freeze state. Applied tells whether the
// data returned to this viewer is the frozen table.
type ResultsFreezeView struct {
	Enabled  bool
	FrozenAt *time.Time
	FinishAt *time.Time
	OpenedAt *time.Time
	Active   bool
	Applied  bool
}

// ResultsDisplayView is the public page presentation (chart and rows).
type ResultsDisplayView struct {
	ChartEnabled bool
	ChartTeams   int32
	RowsLimit    *int32
}

// ResultsSettingsView is the moderator «Налаштування результатів» read model.
type ResultsSettingsView struct {
	ScoreboardVisibility eventConfigModel.Visibility
	FreezeEnabled        bool
	FreezeMinutes        int32
	LiveFreeze           bool
	ChartEnabled         bool
	ChartTeams           int32
	RowsLimit            *int32
	OpenedAt             *time.Time
	Freeze               ResultsFreezeView
}

// ManageResultsView is the moderators' own live results: every team except
// the moderators team, hidden and not admitted ones marked and unranked.
type ManageResultsView struct {
	Revision    int64
	GeneratedAt time.Time
	Freeze      ResultsFreezeView
	Counts      ManageResultsCountsView
	Teams       []ManageResultsTeamView
}

type ManageResultsCountsView struct {
	Ranked, Hidden, NotAdmitted int
}

type ManageResultsTeamView struct {
	Rank                         *int32
	TeamID                       uuid.UUID
	Name, RealName               string
	Pseudonym                    *string
	Individual, Hidden, Admitted bool
	// Moderators marks the always hidden team of the event managers; it has no
	// name of its own and is presented by the client.
	Moderators     bool
	Points, Solved int64
	LastSolveAt    *time.Time
	// Hints opened and the points they cost the team.
	Hints, HintPoints int64
	Solves            []ManageResultsSolveView
}

// ManageResultsSolveView is one solved challenge of a team, in solve order.
type ManageResultsSolveView struct {
	ChallengeID   uuid.UUID
	ChallengeName string
	Points        int32
	SolvedAt      time.Time
	FirstBlood    bool
}

// AnnulSolveView reports how many accepted attempts an annulment rejected.
type AnnulSolveView struct {
	TeamID, ChallengeID uuid.UUID
	Rejected            int
}

// SolutionAttemptsStampView changes whenever an attempt or a decision is
// added to the event's journal.
type SolutionAttemptsStampView struct {
	Attempts, Decisions, HintUnlocks int64
}

// OwnResultsView is the private, session-scoped results read model. It stays
// separate from the event-wide scoreboard so future chart/timeline endpoints
// can evolve independently.
type OwnResultsView struct {
	Entry    ScoreboardEntryView
	Timeline []OwnScoreTimelineEntryView
}

type TeamResultAttemptView struct {
	ID, EventTeamID, UserID, TeamChallengeID, EventChallengeID uuid.UUID
	ParticipantName, Answer                                    string
	AutomaticCorrect, Correct                                  bool
	Decision                                                   challengeAttemptModel.Decision
	ReceivedAt                                                 time.Time
	// Practice: made after a returnable stage closed; shown to the team, never rated.
	Practice bool
}

type OwnTeamResultsView struct {
	Entry    ScoreboardEntryView
	Timeline []OwnScoreTimelineEntryView
	Attempts []TeamResultAttemptView
}

type ChallengeGroupView struct {
	ID        uuid.UUID
	Name      string
	Order     int32
	CreatedAt time.Time
}

// ParticipantView is the moderation-facing read model for a single
// participation row.
type ParticipantView struct {
	UserID            uuid.UUID
	Name              string
	Email             string
	Pseudonym         *string
	DisplayName       string
	TeamID            *uuid.UUID
	TeamName          string
	Hidden            bool
	Invited           bool
	InvitedToTeam     bool
	InvitedTeamID     *uuid.UUID
	InvitedTeamName   string
	InvitationSentAt  *time.Time
	InvitationExpired bool
	Status            participantModel.Status
	CreatedAt         time.Time
	DecidedAt         *time.Time
	Answers           map[string]any
	// FieldsMissing is how many required registration fields are unfilled.
	FieldsMissing int32
	// LastSeenAt is the last request on the event, LastLabAt the last
	// laboratory access over the VPN or the proxy; nil when never.
	LastSeenAt *time.Time
	LastLabAt  *time.Time
}

// ParticipantCountsView feeds the moderation tabs; Applications counts only
// undecided requests.
type ParticipantCountsView struct {
	Participants, Applications, Invitations int64
}

type ParticipantsListResult struct {
	Participants []ParticipantView
	NextCursor   uuid.UUID
	HasMore      bool
	Total        int64
	Counts       ParticipantCountsView
}

type SolutionAttemptView struct {
	ID, EventTeamID, TeamChallengeID, EventChallengeID, EventExerciseID, UserID uuid.UUID
	TeamName, ChallengeName, ParticipantName, Answer, ExpectedFlag              string
	AutomaticCorrect                                                            bool
	Decision                                                                    challengeAttemptModel.Decision
	DecisionReason                                                              *string
	DecidedBy                                                                   *uuid.UUID
	DecidedAt                                                                   *time.Time
	Correct                                                                     bool
	ReceivedAt                                                                  time.Time
	Points                                                                      *int32
	// AttemptsAllowed is the task's effective flag attempt limit (nil = unlimited); AttemptsUsed the team's
	// counted wrong submissions on it.
	AttemptsAllowed *int32
	AttemptsUsed    int64
}

type SolutionAttemptsListResult struct {
	Items      []SolutionAttemptView
	NextCursor uuid.UUID
	HasMore    bool
	Total      int64
}

type SolutionAttemptDecisionView struct {
	AttemptID uuid.UUID
	Decision  challengeAttemptModel.Decision
	Reason    string
	DecidedBy uuid.UUID
	DecidedAt time.Time
	Correct   bool
}

func toParticipantView(p participantModel.Participant) ParticipantView {
	return ParticipantView{
		UserID:           p.UserID,
		TeamID:           p.TeamID,
		Status:           p.Status,
		Invited:          p.Invited,
		InvitedToTeam:    p.InvitedToTeam,
		InvitedTeamID:    p.InvitedTeamID,
		Pseudonym:        p.Pseudonym,
		CreatedAt:        p.CreatedAt,
		DecidedAt:        p.DecidedAt,
		InvitationSentAt: p.InvitationSentAt,
	}
}
