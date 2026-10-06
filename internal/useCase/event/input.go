package event

import (
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"time"

	"github.com/gofrs/uuid"

	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

type CreateEventInput struct {
	Tag           string
	Name          string
	AvailableFrom time.Time
	ArchiveAt     time.Time
	CreatedBy     uuid.UUID
	// InfrastructureAllowed nil defaults to whether Laboratory is available.
	InfrastructureAllowed *bool
}

type UpdateEventInput struct {
	Tag           string
	Name          string
	AvailableFrom time.Time
	ArchiveAt     time.Time
}

// UpdateLifecycleInput is the event-local runtime configuration. Its timing
// rules are validated by eventModel.NewLifecycle before persistence.
type UpdateLifecycleInput struct {
	JoinPolicy eventModel.JoinPolicy
	PublishAt  time.Time
	StartAt    time.Time
	FinishAt   *time.Time
	WithdrawAt *time.Time
}

type ConfigureParticipantFormInput struct {
	Enabled, Required bool
	Document          eventContentModel.Document
	// RequireExisting and BlockSubmissions choose the policy of a required
	// field added while answers exist; nil keeps the previous version's.
	RequireExisting, BlockSubmissions *bool
}

// EventPageInput is a page draft from the editor. NavigationAfter is the
// navbar placement applied on publish (eventContentModel.PlaceInNavigation).
type EventPageInput struct {
	Slug            string
	Title           string
	Document        eventContentModel.Document
	Visibility      eventContentModel.PageVisibility
	Navigation      eventContentModel.PageNavigation
	NavigationAfter string
}

type SubmitParticipantFormInput struct{ Answers map[string]any }

type CreateEventFormInput struct {
	Title    string
	Enabled  bool
	Required bool
	Document eventContentModel.Document
}

type CreateEventFormAssignmentInput struct {
	Rule          eventFormModel.Assignment
	IncludeFuture bool
	Enabled       bool
}

type SubmitEventFormResponseInput struct {
	FormVersionID uuid.UUID
	Answers       map[string]any
}

type UpdateEventScoringProfileInput struct {
	Profile           eventModel.ScoringProfile
	ForceEventScoring bool
	StaticPoints      *int32
}

type BulkUpdateChallengeScoringInput struct {
	ChallengeIDs []uuid.UUID
	Override     *eventModel.ScoringProfile
}

type ListEventsFilter struct {
	Search   string
	Status   string
	Cursor   uuid.UUID
	Page     int
	PageSize int
	SortBy   string
	SortDir  string
}

// UpdateConfigInput carries the general config fields plus Participation.
// Nil leaves the format unchanged; a different value is accepted only until
// the first publication.
type UpdateConfigInput struct {
	Participation          *eventConfigModel.Participation
	Registration           eventConfigModel.Registration
	ScoreboardVisibility   eventConfigModel.Visibility
	ParticipantsVisibility eventConfigModel.Visibility
	PreviewDescription     string
	PreviewPicture         string
	MaxTeamSize            int32
	MinTeamSize            *int32
	MaxTeams               *int32
	// AllowPseudonyms nil keeps the current value (older clients omit it).
	AllowPseudonyms *bool
	// ShowDifficulty / HintsDisabled nil keep the current value.
	ShowDifficulty *bool
	HintsDisabled  *bool
	// HintChargeMode nil keeps the current value.
	HintChargeMode *eventConfigModel.HintChargeMode
	// MaxFlagAttempts left unset keeps the current value; set to null clears it (unlimited).
	MaxFlagAttempts OptionalLimit
	// TaskRevealMode nil keeps the current value; a change is accepted until the event starts.
	TaskRevealMode *eventConfigModel.TaskRevealMode
	// Countdown fields nil keep the current value.
	ShowStartCountdown     *bool
	ShowFinishCountdown    *bool
	FinishCountdownMinutes *int32
	// FinishCountdownMode nil keeps the current value: before_end | from_start.
	FinishCountdownMode *eventConfigModel.FinishCountdownMode
}

func (in UpdateConfigInput) toConfigInput(current eventConfigModel.EventConfig) eventConfigModel.ConfigInput {
	allowPseudonyms := current.AllowPseudonyms
	if in.AllowPseudonyms != nil {
		allowPseudonyms = *in.AllowPseudonyms
	}
	showDifficulty, hintsDisabled := current.ShowDifficulty, current.HintsDisabled
	if in.ShowDifficulty != nil {
		showDifficulty = *in.ShowDifficulty
	}
	if in.HintsDisabled != nil {
		hintsDisabled = *in.HintsDisabled
	}
	hintChargeMode := current.HintChargeMode
	if in.HintChargeMode != nil {
		hintChargeMode = *in.HintChargeMode
	}
	maxFlagAttempts := in.MaxFlagAttempts.Or(current.MaxFlagAttempts)
	countdown := current.Countdown
	if in.ShowStartCountdown != nil {
		countdown.ShowStart = *in.ShowStartCountdown
	}
	if in.ShowFinishCountdown != nil {
		countdown.ShowFinish = *in.ShowFinishCountdown
	}
	if in.FinishCountdownMinutes != nil {
		countdown.FinishMinutes = *in.FinishCountdownMinutes
	}
	if in.FinishCountdownMode != nil {
		countdown.FinishMode = *in.FinishCountdownMode
	}
	return eventConfigModel.ConfigInput{
		Registration:           in.Registration,
		ScoreboardVisibility:   in.ScoreboardVisibility,
		ParticipantsVisibility: in.ParticipantsVisibility,
		PreviewDescription:     in.PreviewDescription,
		PreviewPicture:         in.PreviewPicture,
		MaxTeamSize:            in.MaxTeamSize,
		MinTeamSize:            in.MinTeamSize,
		MaxTeams:               in.MaxTeams,
		AllowPseudonyms:        allowPseudonyms,
		ShowDifficulty:         showDifficulty,
		HintsDisabled:          hintsDisabled,
		HintChargeMode:         hintChargeMode,
		MaxFlagAttempts:        maxFlagAttempts,
		Countdown:              countdown,
	}
}

// ListParticipantsFilter pages an event's participants, optionally narrowed
// to one status (nil means any status), a name/email/pseudonym search and
// registration answer filters.
type ListParticipantsFilter struct {
	EventID  uuid.UUID
	Status   *participantModel.Status
	Kind     participantRepo.Kind
	Search   string
	Fields   []AnswerFilter
	Cursor   uuid.UUID
	PageSize int
}

// ListTeamsFilter pages an event's teams, optionally narrowed by a team name
// search, by admission (nil means any) and by team answer filters.
type ListTeamsFilter struct {
	EventID  uuid.UUID
	Search   string
	Admitted *bool
	Fields   []AnswerFilter
	Cursor   uuid.UUID
	PageSize int
}

// CreateManagedTeamInput is the moderator-owned team creation shape. The
// captain must already be an approved, unassigned participant of this event.
type CreateManagedTeamInput struct {
	Name      string
	CaptainID uuid.UUID
	Fields    map[string]any
}

type UpdateManagedTeamInput struct {
	Name   string
	Hidden bool
	// Fields nil keeps the team's extra field answers unchanged.
	Fields map[string]any
}

type ListSolutionAttemptsFilter struct {
	EventID       uuid.UUID
	TeamID        *uuid.UUID
	ParticipantID *uuid.UUID
	ChallengeID   *uuid.UUID
	Correct       *bool
	FromAt, ToAt  *time.Time
	Cursor        uuid.UUID
	PageSize      int
}

type DecideSolutionAttemptInput struct {
	Decision  challengeAttemptModel.Decision
	Reason    string
	DecidedBy uuid.UUID
}

type AttachExerciseInput struct {
	ExerciseVersionID uuid.UUID
	VariantMode       eventExerciseModel.VariantMode
	FixedVariantIndex *int32
}

type ReplaceEventExerciseInput struct {
	ExerciseVersionID uuid.UUID
	RecreateStands    bool
}

// UpdateEventChallengeInput: visibility is per set, not per task.
type UpdateEventChallengeInput struct {
	Points       int32
	HintsEnabled bool
	// MaxFlagAttempts left unset keeps the current override; set to null clears it (the event value applies).
	MaxFlagAttempts OptionalLimit
}

// OptionalLimit is a nullable number that tells "not sent" from "sent as null": a PUT from an older client that
// omits the field must not clear a limit.
type OptionalLimit struct {
	Set   bool
	Value *int32
}

// UnmarshalJSON marks the value as sent; null leaves Value nil.
func (o *OptionalLimit) UnmarshalJSON(data []byte) error {
	o.Set = true
	return json.Unmarshal(data, &o.Value)
}

// Or is the sent value, or fallback when the field was not sent.
func (o OptionalLimit) Or(fallback *int32) *int32 {
	if !o.Set {
		return fallback
	}
	return o.Value
}

// HintCostInput overrides (Cost) or resets (nil) one hint's cost.
type HintCostInput struct {
	HintID uuid.UUID
	Cost   *int32
}

type ReorderEventChallengesInput struct {
	ChallengeIDs []uuid.UUID
}

type ReorderChallengeGroupsInput struct {
	GroupIDs []uuid.UUID
}

// ReorderGroupChallengesInput is the complete order of one group's
// challenges across the event's sets (GroupID nil = no group).
type ReorderGroupChallengesInput struct {
	GroupID      *uuid.UUID
	ChallengeIDs []uuid.UUID
}

type CreateChallengeGroupInput struct {
	Name  string
	Order int32
}

type UpdateChallengeGroupInput struct {
	Name  string
	Order int32
}

type SetEventManagerInput struct {
	UserID uuid.UUID
	Role   int16
}

type UpdateEventChallengeRelationsInput struct {
	GroupID         *uuid.UUID
	PrerequisiteIDs []uuid.UUID
}

// SubmitChallengeInput keeps the transport-captured arrival timestamp separate
// from processing time so rank tie-breaks remain reproducible.
type SubmitChallengeInput struct {
	Answer         string
	IdempotencyKey uuid.UUID
	ReceivedAt     time.Time
}
