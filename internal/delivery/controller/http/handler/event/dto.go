package event

import (
	"encoding/json"
	"github.com/cybericebox/daemon/pkg/pagination"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type eventResponse struct {
	ID              uuid.UUID  `json:"ID"`
	Tag             string     `json:"Tag"`
	Name            string     `json:"Name"`
	AvailableFrom   time.Time  `json:"AvailableFrom"`
	ArchiveAt       *time.Time `json:"ArchiveAt"`
	Status          string     `json:"Status"`
	LifecycleStatus string     `json:"LifecycleStatus"`
	// InfrastructureAllowed is set at creation; an administrator changes it only before publication.
	InfrastructureAllowed bool      `json:"InfrastructureAllowed"`
	CreatedAt             time.Time `json:"CreatedAt"`
	UpdatedAt             time.Time `json:"UpdatedAt"`
}

type createEventRequest struct {
	Tag           string     `json:"Tag"`
	Name          string     `json:"Name"`
	AvailableFrom time.Time  `json:"AvailableFrom"`
	ArchiveAt     *time.Time `json:"ArchiveAt"`
	// InfrastructureAllowed omitted defaults to Laboratory availability.
	InfrastructureAllowed *bool `json:"InfrastructureAllowed"`
}

type setInfrastructureRequest struct {
	InfrastructureAllowed bool `json:"InfrastructureAllowed"`
}

type updateEventRequest struct {
	Tag           string     `json:"Tag"`
	Name          string     `json:"Name"`
	AvailableFrom time.Time  `json:"AvailableFrom"`
	ArchiveAt     *time.Time `json:"ArchiveAt"`
}

func nullableRequestTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

type eventManagerResponse struct {
	UserID    uuid.UUID `json:"UserID"`
	Role      int16     `json:"Role"`
	CreatedAt time.Time `json:"CreatedAt"`
}

type setEventManagerRequest struct {
	Role int16 `json:"Role"`
}

type solutionAttemptResponse struct {
	ID               uuid.UUID  `json:"ID"`
	EventTeamID      uuid.UUID  `json:"EventTeamID"`
	TeamName         string     `json:"TeamName"`
	TeamChallengeID  uuid.UUID  `json:"TeamChallengeID"`
	EventChallengeID uuid.UUID  `json:"EventChallengeID"`
	ChallengeName    string     `json:"ChallengeName"`
	EventExerciseID  uuid.UUID  `json:"EventExerciseID"`
	UserID           uuid.UUID  `json:"UserID"`
	ParticipantName  string     `json:"ParticipantName"`
	Answer           *string    `json:"Answer"`
	ExpectedFlag     *string    `json:"ExpectedFlag"`
	AutomaticCorrect bool       `json:"AutomaticCorrect"`
	Decision         string     `json:"Decision"`
	DecisionReason   *string    `json:"DecisionReason"`
	DecidedBy        *uuid.UUID `json:"DecidedBy"`
	DecidedAt        *time.Time `json:"DecidedAt"`
	Correct          bool       `json:"Correct"`
	ReceivedAt       time.Time  `json:"ReceivedAt"`
	// Points the attempt brought: set only on the attempt that solved the task.
	Points *int32 `json:"Points"`
	// AttemptsAllowed is the task's flag attempt limit for the team (null = unlimited); AttemptsUsed its wrong
	// submissions counted against it.
	AttemptsAllowed *int32 `json:"AttemptsAllowed"`
	AttemptsUsed    int64  `json:"AttemptsUsed"`
}

// toSolutionAttemptResponse leaves Answer and ExpectedFlag null for callers
// who may not see them (event viewers).
func toSolutionAttemptResponse(v eventUseCase.SolutionAttemptView, withAnswers bool) solutionAttemptResponse {
	out := solutionAttemptResponse{ID: v.ID, EventTeamID: v.EventTeamID, TeamName: v.TeamName, TeamChallengeID: v.TeamChallengeID, EventChallengeID: v.EventChallengeID, ChallengeName: v.ChallengeName, EventExerciseID: v.EventExerciseID, UserID: v.UserID, ParticipantName: v.ParticipantName, AutomaticCorrect: v.AutomaticCorrect, Decision: v.Decision.String(), DecisionReason: v.DecisionReason, DecidedBy: v.DecidedBy, DecidedAt: v.DecidedAt, Correct: v.Correct, ReceivedAt: v.ReceivedAt, Points: v.Points, AttemptsAllowed: v.AttemptsAllowed, AttemptsUsed: v.AttemptsUsed}
	if withAnswers {
		answer, expected := v.Answer, v.ExpectedFlag
		out.Answer, out.ExpectedFlag = &answer, &expected
	}
	return out
}

type decideSolutionAttemptRequest struct {
	Decision string `json:"Decision"`
	Reason   string `json:"Reason"`
}

type solutionAttemptDecisionResponse struct {
	AttemptID uuid.UUID `json:"AttemptID"`
	Decision  string    `json:"Decision"`
	Reason    string    `json:"Reason"`
	DecidedBy uuid.UUID `json:"DecidedBy"`
	DecidedAt time.Time `json:"DecidedAt"`
	Correct   bool      `json:"Correct"`
}

func toSolutionAttemptDecisionResponse(v eventUseCase.SolutionAttemptDecisionView) solutionAttemptDecisionResponse {
	return solutionAttemptDecisionResponse{AttemptID: v.AttemptID, Decision: v.Decision.String(), Reason: v.Reason, DecidedBy: v.DecidedBy, DecidedAt: v.DecidedAt, Correct: v.Correct}
}

func toEventManagerResponse(v eventUseCase.EventManagerView) eventManagerResponse {
	return eventManagerResponse{UserID: v.UserID, Role: v.Role, CreatedAt: v.CreatedAt}
}

type lifecycleResponse struct {
	Configured     bool                       `json:"Configured"`
	JoinPolicy     int16                      `json:"JoinPolicy"`
	PublishAt      *time.Time                 `json:"PublishAt"`
	StartAt        *time.Time                 `json:"StartAt"`
	FinishAt       *time.Time                 `json:"FinishAt"`
	WithdrawAt     *time.Time                 `json:"WithdrawAt"`
	Status         string                     `json:"Status"`
	UpdatedAt      time.Time                  `json:"UpdatedAt"`
	Infrastructure infrastructurePlanResponse `json:"Infrastructure"`
}

type infrastructurePlanResponse struct {
	HasDynamicLabs        bool    `json:"HasDynamicLabs"`
	LaboratoriesAvailable bool    `json:"LaboratoriesAvailable"`
	RequiresVPN           bool    `json:"RequiresVPN"`
	CanStart              bool    `json:"CanStart"`
	Reason                *string `json:"Reason,omitempty"`
}

type updateLifecycleRequest struct {
	JoinPolicy int16      `json:"JoinPolicy"`
	PublishAt  time.Time  `json:"PublishAt"`
	StartAt    time.Time  `json:"StartAt"`
	FinishAt   *time.Time `json:"FinishAt"`
	WithdrawAt *time.Time `json:"WithdrawAt"`
}

type participantFormResponse struct {
	Version  int32                      `json:"Version"`
	Enabled  bool                       `json:"Enabled"`
	Required bool                       `json:"Required"`
	Document eventContentModel.Document `json:"Document"`
	// RequireExisting/BlockSubmissions: the policy for people who answered
	// before a required field was added; Answered counts them.
	RequireExisting  bool  `json:"RequireExisting"`
	BlockSubmissions bool  `json:"BlockSubmissions"`
	Answered         int64 `json:"Answered"`
}
type configureParticipantFormRequest struct {
	Enabled  bool                       `json:"Enabled"`
	Required bool                       `json:"Required"`
	Document eventContentModel.Document `json:"Document"`
	// RequireExisting and BlockSubmissions are omitted to keep the previous
	// version's policy.
	RequireExisting  *bool `json:"RequireExisting"`
	BlockSubmissions *bool `json:"BlockSubmissions"`
}
type participantFormAnswerResponse struct {
	UserID      uuid.UUID                  `json:"UserID"`
	Name        string                     `json:"Name"`
	Email       string                     `json:"Email"`
	FormVersion int32                      `json:"FormVersion"`
	Answers     map[string]any             `json:"Answers"`
	Document    eventContentModel.Document `json:"Document"`
	SubmittedAt time.Time                  `json:"SubmittedAt"`
}

type createEventFormRequest struct {
	Title    string                     `json:"Title"`
	Enabled  bool                       `json:"Enabled"`
	Required bool                       `json:"Required"`
	Document eventContentModel.Document `json:"Document"`
}

// eventFormSwaggerRequest deliberately keeps the block document opaque in the
// generated contract. Its actual shape is the shared content document, whose
// polymorphic blocks Swag cannot parse outside this package.
type eventFormSwaggerRequest struct {
	Title    string `json:"Title"`
	Enabled  bool   `json:"Enabled"`
	Required bool   `json:"Required"`
	Document any    `json:"Document"`
}

type assignEventFormRequest struct {
	Rule          eventFormModel.Assignment `json:"Rule"`
	IncludeFuture bool                      `json:"IncludeFuture"`
	Enabled       bool                      `json:"Enabled"`
}

// eventFormAssignmentSwaggerRequest keeps selector/gate polymorphism opaque
// for OpenAPI generation; transport validation still uses Assignment.
type eventFormAssignmentSwaggerRequest struct {
	Rule          any  `json:"Rule"`
	IncludeFuture bool `json:"IncludeFuture"`
	Enabled       bool `json:"Enabled"`
}

type scoringProfileResponse struct {
	Mode              int16     `json:"Mode"`
	MinPoints         int32     `json:"MinPoints"`
	MaxPoints         int32     `json:"MaxPoints"`
	FloorAtPercent    int32     `json:"FloorAtPercent"`
	ForceEventScoring bool      `json:"ForceEventScoring"`
	StaticPoints      *int32    `json:"StaticPoints"` // static scoring's one value; null = each task's own points
	UpdatedAt         time.Time `json:"UpdatedAt"`
}

type updateScoringProfileRequest struct {
	Mode              int16 `json:"Mode"`
	MinPoints         int32 `json:"MinPoints"`
	MaxPoints         int32 `json:"MaxPoints"`
	FloorAtPercent    int32 `json:"FloorAtPercent"`
	ForceEventScoring bool  `json:"ForceEventScoring"`
}

// updateEventScoringProfileRequest is the event profile plus the static value.
type updateEventScoringProfileRequest struct {
	updateScoringProfileRequest
	StaticPoints *int32 `json:"StaticPoints"`
}

type bulkUpdateChallengeScoringRequest struct {
	ChallengeIDs []uuid.UUID                  `json:"ChallengeIDs"`
	Override     *updateScoringProfileRequest `json:"Override"`
}

// eventStatusToString renders the domain's derived lifecycle position for the
// API. eventModel.EventStatus carries no Stringer of its own (int32 enum), so
// the delivery layer owns this label mapping, same way exercise's
// versionToResponse renders exerciseModel.VersionStatus via string(v.Status)
// (that one's a string-based domain type; this one is int32, hence the switch).
func eventStatusToString(s eventModel.EventStatus) string {
	switch s {
	case eventModel.EventPendingStatus:
		return "pending"
	case eventModel.EventActiveStatus:
		return "active"
	case eventModel.EventArchivedStatus:
		return "archived"
	default:
		return "unknown"
	}
}

func lifecycleStatusToString(s eventModel.LifecycleStatus) string {
	switch s {
	case eventModel.LifecycleNotPublished:
		return "not_published"
	case eventModel.LifecyclePublished:
		return "published"
	case eventModel.LifecycleStarted:
		return "started"
	case eventModel.LifecycleFinished:
		return "finished"
	case eventModel.LifecycleWithdrawn:
		return "withdrawn"
	default:
		return "unknown"
	}
}

func toLifecycleResponse(v eventUseCase.EventLifecycleView) lifecycleResponse {
	result := lifecycleResponse{
		Configured:     v.Configured,
		JoinPolicy:     int16(v.JoinPolicy),
		FinishAt:       v.FinishAt,
		WithdrawAt:     v.WithdrawAt,
		Status:         lifecycleStatusToString(v.Status),
		UpdatedAt:      v.UpdatedAt,
		Infrastructure: toInfrastructurePlanResponse(v.Infrastructure),
	}
	if v.Configured {
		result.PublishAt = &v.PublishAt
		result.StartAt = &v.StartAt
	}
	return result
}

func toInfrastructurePlanResponse(v eventUseCase.EventInfrastructurePlan) infrastructurePlanResponse {
	return infrastructurePlanResponse{HasDynamicLabs: v.HasDynamicLabs, LaboratoriesAvailable: v.LaboratoriesAvailable, RequiresVPN: v.RequiresVPN, CanStart: v.CanStart, Reason: v.Reason}
}

func (r updateLifecycleRequest) toInput() eventUseCase.UpdateLifecycleInput {
	return eventUseCase.UpdateLifecycleInput{
		JoinPolicy: eventModel.JoinPolicy(r.JoinPolicy),
		PublishAt:  r.PublishAt,
		StartAt:    r.StartAt,
		FinishAt:   r.FinishAt,
		WithdrawAt: r.WithdrawAt,
	}
}

func (r configureParticipantFormRequest) toInput() eventUseCase.ConfigureParticipantFormInput {
	return eventUseCase.ConfigureParticipantFormInput{Enabled: r.Enabled, Required: r.Required, Document: r.Document,
		RequireExisting: r.RequireExisting, BlockSubmissions: r.BlockSubmissions}
}
func toParticipantFormResponse(v eventUseCase.ParticipantFormView) participantFormResponse {
	return participantFormResponse{Version: v.Version, Enabled: v.Enabled, Required: v.Required, Document: v.Document,
		RequireExisting: v.RequireExisting, BlockSubmissions: v.BlockSubmissions, Answered: v.Answered}
}
func toParticipantFormAnswerResponse(v eventUseCase.ParticipantFormAnswerView) participantFormAnswerResponse {
	return participantFormAnswerResponse{UserID: v.UserID, Name: v.Name, Email: v.Email, FormVersion: v.FormVersion, Answers: v.Answers, Document: v.Document, SubmittedAt: v.SubmittedAt}
}

func (r createEventFormRequest) toInput() eventUseCase.CreateEventFormInput {
	return eventUseCase.CreateEventFormInput{Title: r.Title, Enabled: r.Enabled, Required: r.Required, Document: r.Document}
}
func (r assignEventFormRequest) toInput() eventUseCase.CreateEventFormAssignmentInput {
	return eventUseCase.CreateEventFormAssignmentInput{Rule: r.Rule, IncludeFuture: r.IncludeFuture, Enabled: r.Enabled}
}

func toScoringProfileResponse(v eventUseCase.EventScoringProfileView) scoringProfileResponse {
	p := v.Profile
	return scoringProfileResponse{Mode: int16(p.Mode), MinPoints: p.MinPoints, MaxPoints: p.MaxPoints, FloorAtPercent: p.FloorAtPercent, ForceEventScoring: v.ForceEventScoring, StaticPoints: v.StaticPoints, UpdatedAt: v.UpdatedAt}
}

func (r updateScoringProfileRequest) profile() eventModel.ScoringProfile {
	return eventModel.ScoringProfile{Mode: eventModel.ScoringMode(r.Mode), MinPoints: r.MinPoints, MaxPoints: r.MaxPoints, FloorAtPercent: r.FloorAtPercent}
}

func (r updateEventScoringProfileRequest) toInput() eventUseCase.UpdateEventScoringProfileInput {
	return eventUseCase.UpdateEventScoringProfileInput{Profile: r.profile(), ForceEventScoring: r.ForceEventScoring, StaticPoints: r.StaticPoints}
}

func toResponse(v eventUseCase.EventView) eventResponse {
	var archiveAt *time.Time
	if !v.ArchiveAt.IsZero() {
		archiveAt = &v.ArchiveAt
	}
	return eventResponse{
		ID: v.ID, Tag: v.Tag, Name: v.Name,
		AvailableFrom: v.AvailableFrom, ArchiveAt: archiveAt,
		Status:                eventStatusToString(v.Status),
		LifecycleStatus:       lifecycleStatusToString(v.LifecycleStatus),
		InfrastructureAllowed: v.InfrastructureAllowed,
		CreatedAt:             v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}
}

type configResponse struct {
	EventID                uuid.UUID `json:"EventID"`
	Participation          *int32    `json:"Participation"`
	Registration           int32     `json:"Registration"`
	ScoreboardVisibility   int32     `json:"ScoreboardVisibility"`
	ParticipantsVisibility int32     `json:"ParticipantsVisibility"`
	PreviewDescription     string    `json:"PreviewDescription"`
	PreviewPicture         string    `json:"PreviewPicture"`
	MaxTeamSize            int32     `json:"MaxTeamSize"`
	MinTeamSize            *int32    `json:"MinTeamSize"`
	MaxTeams               *int32    `json:"MaxTeams"`
	AllowPseudonyms        bool      `json:"AllowPseudonyms"`
	ShowDifficulty         bool      `json:"ShowDifficulty"`
	HintsDisabled          bool      `json:"HintsDisabled"`
	// HintChargeMode: reward (A, default) | balance (B).
	HintChargeMode string `json:"HintChargeMode"`
	// MaxFlagAttempts: wrong flag submissions allowed per team and task; null = unlimited. A task may override it.
	MaxFlagAttempts *int32 `json:"MaxFlagAttempts"`
	// Participant countdown on the challenges and results pages.
	ShowStartCountdown     bool  `json:"ShowStartCountdown"`
	ShowFinishCountdown    bool  `json:"ShowFinishCountdown"`
	FinishCountdownMinutes int32 `json:"FinishCountdownMinutes"`
	// FinishCountdownMode: before_end (the time-left countdown shows during the last FinishCountdownMinutes) |
	// from_start (from the start of the current stage, of the event without stages).
	FinishCountdownMode string `json:"FinishCountdownMode"`
	// TaskRevealMode: all_ready (a task is revealed when it is ready for every team, the default) |
	// as_ready (per team, as soon as its lab is ready). Changeable until the event starts.
	TaskRevealMode string `json:"TaskRevealMode"`
	// InfrastructureAllowed is the admin's creation-time decision (read-only).
	InfrastructureAllowed bool                   `json:"InfrastructureAllowed"`
	Theme                 eventConfigModel.Theme `json:"Theme"`
	UpdatedAt             time.Time              `json:"UpdatedAt"`
}

type updateThemeRequest struct {
	Brand  string `json:"Brand"`
	Accent string `json:"Accent"`
}

type updateConfigRequest struct {
	Participation          *int32 `json:"Participation"`
	Registration           int32  `json:"Registration"`
	ScoreboardVisibility   int32  `json:"ScoreboardVisibility"`
	ParticipantsVisibility int32  `json:"ParticipantsVisibility"`
	PreviewDescription     string `json:"PreviewDescription"`
	PreviewPicture         string `json:"PreviewPicture"`
	MaxTeamSize            int32  `json:"MaxTeamSize"`
	MinTeamSize            *int32 `json:"MinTeamSize"`
	MaxTeams               *int32 `json:"MaxTeams"`
	// AllowPseudonyms omitted keeps the current value.
	AllowPseudonyms *bool `json:"AllowPseudonyms"`
	// ShowDifficulty / HintsDisabled omitted keep the current value.
	ShowDifficulty *bool `json:"ShowDifficulty"`
	HintsDisabled  *bool `json:"HintsDisabled"`
	// HintChargeMode omitted keeps the current value: reward | balance.
	HintChargeMode *string `json:"HintChargeMode"`
	// MaxFlagAttempts omitted keeps the current value; null clears it (unlimited); otherwise 1..1000.
	MaxFlagAttempts eventUseCase.OptionalLimit `json:"MaxFlagAttempts" swaggertype:"integer"`
	// Countdown fields omitted keep the current value.
	ShowStartCountdown     *bool  `json:"ShowStartCountdown"`
	ShowFinishCountdown    *bool  `json:"ShowFinishCountdown"`
	FinishCountdownMinutes *int32 `json:"FinishCountdownMinutes"`
	// FinishCountdownMode omitted keeps the current value: before_end | from_start.
	FinishCountdownMode *string `json:"FinishCountdownMode"`
	// TaskRevealMode omitted keeps the current value: all_ready | as_ready. Changing it after the event
	// starts is refused (400, 21222); an unknown value is 400 (21221).
	TaskRevealMode *string `json:"TaskRevealMode"`
}

type participantResponse struct {
	UserID            uuid.UUID      `json:"UserID"`
	Name              string         `json:"Name"`
	Email             string         `json:"Email"`
	Pseudonym         *string        `json:"Pseudonym"`
	DisplayName       string         `json:"DisplayName"`
	TeamID            *uuid.UUID     `json:"TeamID"`
	TeamName          string         `json:"TeamName"`
	Hidden            bool           `json:"Hidden"`
	Invited           bool           `json:"Invited"`
	InvitedToTeam     bool           `json:"InvitedToTeam"`
	InvitedTeamID     *uuid.UUID     `json:"InvitedTeamID"`
	InvitedTeamName   string         `json:"InvitedTeamName"`
	InvitationSentAt  *time.Time     `json:"InvitationSentAt"`
	InvitationExpired bool           `json:"InvitationExpired"`
	Status            int32          `json:"Status"`
	CreatedAt         time.Time      `json:"CreatedAt"`
	DecidedAt         *time.Time     `json:"DecidedAt"`
	Answers           map[string]any `json:"Answers"`
	FieldsMissing     int32          `json:"FieldsMissing"`
}

type participantCountsResponse struct {
	Participants int64 `json:"Participants"`
	Applications int64 `json:"Applications"`
	Invitations  int64 `json:"Invitations"`
}

// participantsPageResponse is the cursor page plus the moderation tab counters.
type participantsPageResponse struct {
	pagination.CursorPage[participantResponse]
	Counts participantCountsResponse `json:"Counts"`
}

type invitationResendResponse struct {
	UserID           uuid.UUID  `json:"UserID"`
	Email            string     `json:"Email"`
	InvitationSentAt *time.Time `json:"InvitationSentAt"`
}

type teamMemberResponse struct {
	UserID    uuid.UUID `json:"UserID"`
	Name      string    `json:"Name"`
	Email     string    `json:"Email"`
	Pseudonym *string   `json:"Pseudonym"`
	Role      int16     `json:"Role"`
}

type teamInvitationResponse struct {
	UserID           uuid.UUID  `json:"UserID"`
	Name             string     `json:"Name"`
	Email            string     `json:"Email"`
	CreatedAt        time.Time  `json:"CreatedAt"`
	InvitationSentAt *time.Time `json:"InvitationSentAt"`
}

type teamResponse struct {
	ID                 uuid.UUID                `json:"ID"`
	Name               string                   `json:"Name"`
	CaptainID          uuid.UUID                `json:"CaptainID"`
	Hidden             bool                     `json:"Hidden"`
	MemberCount        int32                    `json:"MemberCount"`
	ExtraFields        map[string]any           `json:"ExtraFields"`
	FieldsMissing      int32                    `json:"FieldsMissing"`
	CreatedAt          time.Time                `json:"CreatedAt"`
	Members            []teamMemberResponse     `json:"Members"`
	PendingInvitations []teamInvitationResponse `json:"PendingInvitations"`
	Admitted           bool                     `json:"Admitted"`
	AdmittedManually   bool                     `json:"AdmittedManually"`
	MinTeamSize        int32                    `json:"MinTeamSize"`
	CaptainPending     bool                     `json:"CaptainPending"`
	// Formed: the roster is closed for good (FormedAt says when).
	Formed   bool       `json:"Formed"`
	FormedAt *time.Time `json:"FormedAt"`
}

type teamProfileResponse struct {
	Team    teamResponse               `json:"Team"`
	Results *manageResultsTeamResponse `json:"Results"`
}

type setTeamHiddenRequest struct {
	Hidden bool `json:"Hidden"`
}

type setTeamAdmissionRequest struct {
	AdmittedManually bool `json:"AdmittedManually"`
}

type listColumnDTO struct {
	Key     string `json:"Key"`
	Visible bool   `json:"Visible"`
}

type listColumnsDTO struct {
	List    string          `json:"List"`
	Columns []listColumnDTO `json:"Columns"`
}

type attachExerciseRequest struct {
	ExerciseVersionID uuid.UUID `json:"ExerciseVersionID"`
	VariantMode       int16     `json:"VariantMode"`
	FixedVariantIndex *int32    `json:"FixedVariantIndex"`
}

type replaceEventExerciseRequest struct {
	ExerciseVersionID uuid.UUID `json:"ExerciseVersionID"`
	// RecreateStands confirms recreating the stand Labs of teams whose stage is already running.
	RecreateStands bool `json:"RecreateStands"`
}

// recreateStandsRequest is the optional body of fork and revert.
type recreateStandsRequest struct {
	RecreateStands bool `json:"RecreateStands"`
}

type eventExerciseResponse struct {
	ID                uuid.UUID  `json:"ID"`
	ExerciseID        uuid.UUID  `json:"ExerciseID"`
	ExerciseName      string     `json:"ExerciseName"`
	ExerciseVersionID uuid.UUID  `json:"ExerciseVersionID"`
	VariantMode       int16      `json:"VariantMode"`
	FixedVariantIndex *int32     `json:"FixedVariantIndex"`
	Revision          int32      `json:"Revision"`
	Status            int16      `json:"Status"`
	ReplacesID        *uuid.UUID `json:"ReplacesID"`
	SupersededAt      *time.Time `json:"SupersededAt"`
	DetachedAt        *time.Time `json:"DetachedAt"`
	CreatedAt         time.Time  `json:"CreatedAt"`
	// StageID is the stage the set belongs to; null lives for the whole event.
	StageID *uuid.UUID `json:"StageID"`
	// W4: «версія N» is the catalog version ordinal, not the event revision.
	Scope               string                     `json:"Scope"`
	VersionNumber       int32                      `json:"VersionNumber"`
	LatestVersionID     *uuid.UUID                 `json:"LatestVersionID"`
	LatestVersionNumber int32                      `json:"LatestVersionNumber"`
	UpdateAvailable     bool                       `json:"UpdateAvailable"`
	Fork                *eventExerciseForkResponse `json:"Fork"`
	Infrastructure      bool                       `json:"Infrastructure"`
	VariantCount        int32                      `json:"VariantCount"`
	ChallengeCount      int32                      `json:"ChallengeCount"`
	PublishedCount      int32                      `json:"PublishedCount"`
	HasAttempts         bool                       `json:"HasAttempts"`
	// Resources is the total of the pinned version: the least and the most it needs over its variants (equal for
	// one variant); planning reserves the largest. ResourceHeavy: an approved elevation holds a device above the
	// platform frame (a badge). NoAgentFits: no laboratory that is used can run it (never names one).
	Resources     eventResourceRangeResponse `json:"Resources"`
	ResourceHeavy bool                       `json:"ResourceHeavy"`
	NoAgentFits   bool                       `json:"NoAgentFits"`
}

// eventResourceTotalsResponse: container devices, their blocks, CPU in millicores, memory in bytes.
type eventResourceTotalsResponse struct {
	Devices       int   `json:"Devices"`
	Blocks        int   `json:"Blocks"`
	CPUMillicores int64 `json:"CPUMillicores"`
	MemoryBytes   int64 `json:"MemoryBytes"`
}

type eventResourceRangeResponse struct {
	Min eventResourceTotalsResponse `json:"Min"`
	Max eventResourceTotalsResponse `json:"Max"`
}

func totalsResponse(t resourcesModel.Totals) eventResourceTotalsResponse {
	return eventResourceTotalsResponse{Devices: t.Devices, Blocks: t.Blocks, CPUMillicores: t.CPUMillicores, MemoryBytes: t.MemoryBytes}
}

func rangeResponse(r resourcesModel.Range) eventResourceRangeResponse {
	return eventResourceRangeResponse{Min: totalsResponse(r.Min), Max: totalsResponse(r.Max)}
}

type eventExerciseForkResponse struct {
	SourceExerciseID          uuid.UUID  `json:"SourceExerciseID"`
	SourceExerciseName        string     `json:"SourceExerciseName"`
	SourceVersionID           *uuid.UUID `json:"SourceVersionID"`
	SourceVersionNumber       int32      `json:"SourceVersionNumber"`
	SourceLatestVersionID     *uuid.UUID `json:"SourceLatestVersionID"`
	SourceLatestVersionNumber int32      `json:"SourceLatestVersionNumber"`
	SourceUpdateAvailable     bool       `json:"SourceUpdateAvailable"`
}

type updateEventExerciseRequest struct {
	// ExerciseVersionID nil = the exercise's latest published version.
	ExerciseVersionID *uuid.UUID `json:"ExerciseVersionID"`
	// RecreateStands confirms recreating the stand Labs of teams whose stage is already running.
	RecreateStands bool `json:"RecreateStands"`
}

type hintCostRequest struct {
	HintID uuid.UUID `json:"HintID"`
	Cost   *int32    `json:"Cost"`
}

type updateHintCostsRequest struct {
	Costs []hintCostRequest `json:"Costs"`
}

// challengeHintResponse: Level is the catalog hint level (nudge | direction |
// steps | near_solution); Cost is the event's price, 0 until one is set.
type challengeHintResponse struct {
	ID         uuid.UUID `json:"ID"`
	Text       string    `json:"Text"`
	Level      string    `json:"Level"`
	Cost       int32     `json:"Cost"`
	Overridden bool      `json:"Overridden"`
}

type hintUnlockResponse struct {
	TeamID           uuid.UUID  `json:"TeamID"`
	TeamName         string     `json:"TeamName"`
	EventChallengeID uuid.UUID  `json:"EventChallengeID"`
	ChallengeName    string     `json:"ChallengeName"`
	HintID           uuid.UUID  `json:"HintID"`
	HintIndex        int        `json:"HintIndex"`
	UnlockedBy       *uuid.UUID `json:"UnlockedBy"`
	UnlockedByName   string     `json:"UnlockedByName"`
	UnlockedAt       time.Time  `json:"UnlockedAt"`
	Cost             int32      `json:"Cost"`
}

// eventCatalogTagResponse is a tag of the event's attachable exercises.
type eventCatalogTagResponse struct {
	Tag           string `json:"Tag"`
	ExerciseCount int64  `json:"ExerciseCount"`
}

type publishedExerciseChoiceResponse struct {
	ID                 uuid.UUID `json:"ID"`
	Name               string    `json:"Name"`
	Description        string    `json:"Description"`
	PublishedVersionID uuid.UUID `json:"PublishedVersionID"`
	Tags               []string  `json:"Tags"`
	// Scope: catalog | event (the event's own exercise).
	Scope          string `json:"Scope"`
	Infrastructure bool   `json:"Infrastructure"`
	// Attached: the event already uses it (or its fork family).
	Attached bool `json:"Attached"`
	// Resources is the total of the published version (min and max over its variants); ResourceHeavy: an
	// approved elevation holds a device above the platform frame (a badge in the picker).
	Resources     eventResourceRangeResponse `json:"Resources"`
	ResourceHeavy bool                       `json:"ResourceHeavy"`
}

type publishedExerciseTaskPreviewResponse struct {
	Name       string `json:"Name"`
	Difficulty string `json:"Difficulty"`
	HintCount  int    `json:"HintCount"`
}

type publishedExercisePreviewResponse struct {
	ID           uuid.UUID                              `json:"ID"`
	Name         string                                 `json:"Name"`
	Description  string                                 `json:"Description"`
	VersionID    uuid.UUID                              `json:"VersionID"`
	VariantCount int                                    `json:"VariantCount"`
	Variant      int                                    `json:"Variant"`
	Tasks        []publishedExerciseTaskPreviewResponse `json:"Tasks"`
}

type eventChallengeResponse struct {
	ID              uuid.UUID   `json:"ID"`
	TaskID          uuid.UUID   `json:"TaskID"`
	GroupID         *uuid.UUID  `json:"GroupID"`
	PrerequisiteIDs []uuid.UUID `json:"PrerequisiteIDs"`
	Order           int32       `json:"Order"`
	BoardOrder      *int32      `json:"BoardOrder"`
	Points          int32       `json:"Points"`
	// EffectivePoints is what teams see and score: the event's static value when the task follows a static event, else Points.
	EffectivePoints int32                   `json:"EffectivePoints"`
	ScoringOverride *scoringProfileResponse `json:"ScoringOverride"`
	HintsEnabled    bool                    `json:"HintsEnabled"`
	// MaxFlagAttempts is the task's own limit of wrong submissions per team; null = the event's value.
	MaxFlagAttempts *int32                        `json:"MaxFlagAttempts"`
	Published       bool                          `json:"Published"`
	Availability    challengeAvailabilityResponse `json:"Availability"`
	Snapshot        json.RawMessage               `json:"Snapshot" swaggertype:"object"`
	Hints           []challengeHintResponse       `json:"Hints"`
}

type challengeAvailabilityResponse struct {
	Preparing int64 `json:"Preparing"`
	Ready     int64 `json:"Ready"`
	Available int64 `json:"Available"`
	Failed    int64 `json:"Failed"`
	Total     int64 `json:"Total"`
}

// updateEventChallengeRequest: visibility is per set (PUT …/visibility).
type updateEventChallengeRequest struct {
	Points       int32 `json:"Points"`
	HintsEnabled bool  `json:"HintsEnabled"`
	// MaxFlagAttempts omitted keeps the current override; null clears it (the event value applies); otherwise 1..1000.
	MaxFlagAttempts eventUseCase.OptionalLimit `json:"MaxFlagAttempts" swaggertype:"integer"`
}

type reorderEventChallengesRequest struct {
	ChallengeIDs []uuid.UUID `json:"ChallengeIDs"`
}

type reorderChallengeGroupsRequest struct {
	GroupIDs []uuid.UUID `json:"GroupIDs"`
}

type setEventExerciseVisibilityRequest struct {
	Published bool `json:"Published"`
}

type reorderGroupChallengesRequest struct {
	GroupID      *uuid.UUID  `json:"GroupID"`
	ChallengeIDs []uuid.UUID `json:"ChallengeIDs"`
}

type challengeGroupResponse struct {
	ID        uuid.UUID `json:"ID"`
	Name      string    `json:"Name"`
	Order     int32     `json:"Order"`
	CreatedAt time.Time `json:"CreatedAt"`
}

type createChallengeGroupRequest struct {
	Name  string `json:"Name"`
	Order int32  `json:"Order"`
}

type updateChallengeGroupRequest struct {
	Name  string `json:"Name"`
	Order int32  `json:"Order"`
}

type updateEventChallengeRelationsRequest struct {
	GroupID         *uuid.UUID  `json:"GroupID"`
	PrerequisiteIDs []uuid.UUID `json:"PrerequisiteIDs"`
}

func toConfigResponse(v eventUseCase.EventConfigView) configResponse {
	var participation *int32
	if v.Participation != nil {
		p := int32(*v.Participation)
		participation = &p
	}
	return configResponse{
		EventID:                v.EventID,
		Participation:          participation,
		Registration:           int32(v.Registration),
		ScoreboardVisibility:   int32(v.ScoreboardVisibility),
		ParticipantsVisibility: int32(v.ParticipantsVisibility),
		PreviewDescription:     v.PreviewDescription,
		PreviewPicture:         v.PreviewPicture,
		MaxTeamSize:            v.MaxTeamSize,
		MinTeamSize:            v.MinTeamSize,
		MaxTeams:               v.MaxTeams,
		AllowPseudonyms:        v.AllowPseudonyms,
		ShowDifficulty:         v.ShowDifficulty,
		HintsDisabled:          v.HintsDisabled,
		HintChargeMode:         HintChargeModeName(v.HintChargeMode),
		MaxFlagAttempts:        v.MaxFlagAttempts,
		ShowStartCountdown:     v.Countdown.ShowStart,
		ShowFinishCountdown:    v.Countdown.ShowFinish,
		FinishCountdownMinutes: v.Countdown.FinishMinutes,
		FinishCountdownMode:    string(v.Countdown.Mode()),
		TaskRevealMode:         string(v.TaskRevealMode),
		Theme:                  v.Theme,
		UpdatedAt:              v.UpdatedAt,
	}
}

func (r updateConfigRequest) toInput() eventUseCase.UpdateConfigInput {
	var participation *eventConfigModel.Participation
	if r.Participation != nil {
		p := eventConfigModel.Participation(*r.Participation)
		participation = &p
	}
	return eventUseCase.UpdateConfigInput{
		Participation:          participation,
		Registration:           eventConfigModel.Registration(r.Registration),
		ScoreboardVisibility:   eventConfigModel.Visibility(r.ScoreboardVisibility),
		ParticipantsVisibility: eventConfigModel.Visibility(r.ParticipantsVisibility),
		PreviewDescription:     r.PreviewDescription,
		PreviewPicture:         r.PreviewPicture,
		MaxTeamSize:            r.MaxTeamSize,
		MinTeamSize:            r.MinTeamSize,
		MaxTeams:               r.MaxTeams,
		AllowPseudonyms:        r.AllowPseudonyms,
		ShowDifficulty:         r.ShowDifficulty,
		HintsDisabled:          r.HintsDisabled,
		HintChargeMode:         parseHintChargeMode(r.HintChargeMode),
		MaxFlagAttempts:        r.MaxFlagAttempts,
		ShowStartCountdown:     r.ShowStartCountdown,
		ShowFinishCountdown:    r.ShowFinishCountdown,
		FinishCountdownMinutes: r.FinishCountdownMinutes,
		FinishCountdownMode:    parseFinishCountdownMode(r.FinishCountdownMode),
		TaskRevealMode:         parseTaskRevealMode(r.TaskRevealMode),
	}
}

// parseTaskRevealMode: nil keeps the current mode; an unknown name is passed on for the domain to reject.
func parseTaskRevealMode(name *string) *eventConfigModel.TaskRevealMode {
	if name == nil {
		return nil
	}
	mode := eventConfigModel.TaskRevealMode(*name)
	return &mode
}

// HintChargeModeName is the API name of a hint charge mode.
func HintChargeModeName(m eventConfigModel.HintChargeMode) string {
	if m == eventConfigModel.HintChargeBalance {
		return "balance"
	}
	return "reward"
}

// parseHintChargeMode: nil keeps the current mode; an unknown name becomes an
// invalid mode the domain rejects.
func parseHintChargeMode(name *string) *eventConfigModel.HintChargeMode {
	if name == nil {
		return nil
	}
	mode := eventConfigModel.HintChargeMode(-1)
	switch *name {
	case "reward":
		mode = eventConfigModel.HintChargeReward
	case "balance":
		mode = eventConfigModel.HintChargeBalance
	}
	return &mode
}

func toParticipantResponse(v eventUseCase.ParticipantView) participantResponse {
	return participantResponse{
		UserID:            v.UserID,
		Name:              v.Name,
		Email:             v.Email,
		Pseudonym:         v.Pseudonym,
		DisplayName:       v.DisplayName,
		TeamID:            v.TeamID,
		TeamName:          v.TeamName,
		Hidden:            v.Hidden,
		Invited:           v.Invited,
		InvitedToTeam:     v.InvitedToTeam,
		InvitedTeamID:     v.InvitedTeamID,
		InvitedTeamName:   v.InvitedTeamName,
		InvitationSentAt:  v.InvitationSentAt,
		InvitationExpired: v.InvitationExpired,
		Status:            int32(v.Status),
		CreatedAt:         v.CreatedAt,
		DecidedAt:         v.DecidedAt,
		Answers:           v.Answers,
		FieldsMissing:     v.FieldsMissing,
	}
}

func toTeamResponse(v eventUseCase.TeamView) teamResponse {
	members := make([]teamMemberResponse, 0, len(v.Members))
	for _, m := range v.Members {
		members = append(members, teamMemberResponse{UserID: m.UserID, Name: m.Name, Email: m.Email, Pseudonym: m.Pseudonym, Role: int16(m.Role)})
	}
	invitations := make([]teamInvitationResponse, 0, len(v.PendingInvitations))
	for _, i := range v.PendingInvitations {
		invitations = append(invitations, teamInvitationResponse{UserID: i.UserID, Name: i.Name, Email: i.Email, CreatedAt: i.CreatedAt, InvitationSentAt: i.InvitationSentAt})
	}
	extra := v.ExtraFields
	if extra == nil {
		extra = map[string]any{}
	}
	return teamResponse{
		ID: v.ID, Name: v.Name, CaptainID: v.CaptainID, Hidden: v.Hidden, MemberCount: v.MemberCount, ExtraFields: extra, FieldsMissing: v.FieldsMissing, CreatedAt: v.CreatedAt,
		Members: members, PendingInvitations: invitations, Admitted: v.Admitted, AdmittedManually: v.AdmittedManually, MinTeamSize: v.MinTeamSize,
		CaptainPending: v.CaptainPending, Formed: v.Formed, FormedAt: v.FormedAt,
	}
}

func toListColumnsDTO(list string, columns []eventUseCase.ListColumnView) listColumnsDTO {
	out := listColumnsDTO{List: list, Columns: make([]listColumnDTO, 0, len(columns))}
	for _, column := range columns {
		out.Columns = append(out.Columns, listColumnDTO{Key: column.Key, Visible: column.Visible})
	}
	return out
}

func (r attachExerciseRequest) toInput() eventUseCase.AttachExerciseInput {
	return eventUseCase.AttachExerciseInput{ExerciseVersionID: r.ExerciseVersionID, VariantMode: eventExerciseModel.VariantMode(r.VariantMode), FixedVariantIndex: r.FixedVariantIndex}
}

func (r replaceEventExerciseRequest) toInput() eventUseCase.ReplaceEventExerciseInput {
	return eventUseCase.ReplaceEventExerciseInput{ExerciseVersionID: r.ExerciseVersionID, RecreateStands: r.RecreateStands}
}

func (r updateEventChallengeRequest) toInput() eventUseCase.UpdateEventChallengeInput {
	return eventUseCase.UpdateEventChallengeInput{Points: r.Points, HintsEnabled: r.HintsEnabled, MaxFlagAttempts: r.MaxFlagAttempts}
}

func (r reorderEventChallengesRequest) toInput() eventUseCase.ReorderEventChallengesInput {
	return eventUseCase.ReorderEventChallengesInput{ChallengeIDs: r.ChallengeIDs}
}

func (r reorderChallengeGroupsRequest) toInput() eventUseCase.ReorderChallengeGroupsInput {
	return eventUseCase.ReorderChallengeGroupsInput{GroupIDs: r.GroupIDs}
}

func (r reorderGroupChallengesRequest) toInput() eventUseCase.ReorderGroupChallengesInput {
	return eventUseCase.ReorderGroupChallengesInput{GroupID: r.GroupID, ChallengeIDs: r.ChallengeIDs}
}

func (r createChallengeGroupRequest) toInput() eventUseCase.CreateChallengeGroupInput {
	return eventUseCase.CreateChallengeGroupInput{Name: r.Name, Order: r.Order}
}

func (r updateChallengeGroupRequest) toInput() eventUseCase.UpdateChallengeGroupInput {
	return eventUseCase.UpdateChallengeGroupInput{Name: r.Name, Order: r.Order}
}

func (r updateEventChallengeRelationsRequest) toInput() eventUseCase.UpdateEventChallengeRelationsInput {
	return eventUseCase.UpdateEventChallengeRelationsInput{GroupID: r.GroupID, PrerequisiteIDs: r.PrerequisiteIDs}
}

func toEventExerciseResponse(v eventUseCase.EventExerciseView) eventExerciseResponse {
	out := eventExerciseResponse{ID: v.ID, ExerciseID: v.ExerciseID, ExerciseName: v.ExerciseName, ExerciseVersionID: v.ExerciseVersionID, VariantMode: int16(v.VariantMode), FixedVariantIndex: v.FixedVariantIndex, Revision: v.Revision, Status: int16(v.Status), ReplacesID: v.ReplacesID, SupersededAt: v.SupersededAt, DetachedAt: v.DetachedAt, CreatedAt: v.CreatedAt, StageID: v.StageID,
		Scope: v.Scope, VersionNumber: v.VersionNumber, LatestVersionID: v.LatestVersionID, LatestVersionNumber: v.LatestVersionNumber, UpdateAvailable: v.UpdateAvailable,
		Infrastructure: v.Infrastructure, VariantCount: v.VariantCount, ChallengeCount: v.ChallengeCount, PublishedCount: v.PublishedCount, HasAttempts: v.HasAttempts}
	out.Resources, out.ResourceHeavy, out.NoAgentFits = rangeResponse(v.Resources), v.ResourceHeavy, v.NoAgentFits
	if out.Scope == "" {
		out.Scope = "catalog"
	}
	if f := v.Fork; f != nil {
		out.Fork = &eventExerciseForkResponse{SourceExerciseID: f.SourceExerciseID, SourceExerciseName: f.SourceExerciseName, SourceVersionID: f.SourceVersionID,
			SourceVersionNumber: f.SourceVersionNumber, SourceLatestVersionID: f.SourceLatestVersionID, SourceLatestVersionNumber: f.SourceLatestVersionNumber, SourceUpdateAvailable: f.SourceUpdateAvailable}
	}
	return out
}

func toEventChallengeResponse(v eventUseCase.EventChallengeView) eventChallengeResponse {
	var override *scoringProfileResponse
	if v.ScoringOverride != nil {
		p := *v.ScoringOverride
		override = &scoringProfileResponse{Mode: int16(p.Mode), MinPoints: p.MinPoints, MaxPoints: p.MaxPoints, FloorAtPercent: p.FloorAtPercent}
	}
	availability := v.Availability
	return eventChallengeResponse{ID: v.ID, TaskID: v.TaskID, GroupID: v.GroupID, PrerequisiteIDs: v.PrerequisiteIDs, Order: v.Order, BoardOrder: v.BoardOrder, Points: v.Points, EffectivePoints: v.EffectivePoints, ScoringOverride: override, HintsEnabled: v.HintsEnabled, MaxFlagAttempts: v.MaxFlagAttempts, Published: v.Published, Availability: challengeAvailabilityResponse{Preparing: availability.Preparing, Ready: availability.Ready, Available: availability.Available, Failed: availability.Failed, Total: availability.Total}, Snapshot: v.Snapshot, Hints: toChallengeHintResponses(v.Hints)}
}

func toChallengeHintResponses(hints []eventUseCase.ChallengeHintView) []challengeHintResponse {
	out := make([]challengeHintResponse, 0, len(hints))
	for _, hint := range hints {
		out = append(out, challengeHintResponse(hint))
	}
	return out
}

func toChallengeGroupResponse(v eventUseCase.ChallengeGroupView) challengeGroupResponse {
	return challengeGroupResponse{ID: v.ID, Name: v.Name, Order: v.Order, CreatedAt: v.CreatedAt}
}

// resourcePlanTaskResponse is one attached task in the event's resource plan.
type resourcePlanTaskResponse struct {
	EventExerciseID uuid.UUID                  `json:"EventExerciseID"`
	ExerciseID      uuid.UUID                  `json:"ExerciseID"`
	ExerciseName    string                     `json:"ExerciseName"`
	Range           eventResourceRangeResponse `json:"Range"`
	// Reserved is what planning reserves per team: the largest variant (the pinned one for a fixed variant).
	Reserved      eventResourceTotalsResponse `json:"Reserved"`
	ResourceHeavy bool                        `json:"ResourceHeavy"`
	InternetLab   bool                        `json:"InternetLab"`
	NoAgentFits   bool                        `json:"NoAgentFits"`
}

// resourceAmountResponse: CPU in millicores, memory in bytes.
type resourceAmountResponse struct {
	CPUMillicores int64 `json:"CPUMillicores"`
	MemoryBytes   int64 `json:"MemoryBytes"`
}

// resourcePodResponse is a group pod rounded up to whole blocks.
type resourcePodResponse struct {
	Blocks        int   `json:"Blocks"`
	CPUMillicores int64 `json:"CPUMillicores"`
	MemoryBytes   int64 `json:"MemoryBytes"`
}

// resourcePlanGroupResponse is a team's lab group's own pods, computed with the agents' formula: the VPN grows
// with the event's maximum team size, the gateway with the group's labs that use the internet. Known is false
// while no laboratory reported its sizing (the pods add nothing then).
type resourcePlanGroupResponse struct {
	MaxUsers     int                 `json:"MaxUsers"`
	InternetLabs int                 `json:"InternetLabs"`
	VPN          resourcePodResponse `json:"VPN"`
	Gateway      resourcePodResponse `json:"Gateway"`
	Known        bool                `json:"Known"`
	// TooLarge: the maximum team size (or the internet labs) is above what every used laboratory can size a group for.
	TooLarge bool `json:"TooLarge"`
}

// eventResourcePlanResponse is what the event reserves: per team the devices of its tasks plus the group
// overhead as a separate line, and the total for the teams.
type eventResourcePlanResponse struct {
	Tasks     []resourcePlanTaskResponse  `json:"Tasks"`
	TeamTasks eventResourceTotalsResponse `json:"TeamTasks"`
	Group     resourcePlanGroupResponse   `json:"Group"`
	PerTeam   eventResourceTotalsResponse `json:"PerTeam"`
	// Teams is the number of teams reserved for: MaxTeams when set, else the teams there are now (at least 1).
	Teams      int                         `json:"Teams"`
	TeamsBasis string                      `json:"TeamsBasis" enums:"max_teams,current"`
	Total      eventResourceTotalsResponse `json:"Total"`
	// NoAgentFits: some task cannot be placed on any laboratory that is used.
	NoAgentFits bool `json:"NoAgentFits"`
}

func toResourcePlanResponse(p eventUseCase.EventResourcePlan) eventResourcePlanResponse {
	out := eventResourcePlanResponse{
		Tasks:     make([]resourcePlanTaskResponse, 0, len(p.Tasks)),
		TeamTasks: totalsResponse(p.TeamTasks), PerTeam: totalsResponse(p.PerTeam), Total: totalsResponse(p.Total),
		Teams: p.Teams, TeamsBasis: p.TeamsBasis, NoAgentFits: p.NoAgentFits,
		Group: resourcePlanGroupResponse{
			MaxUsers: p.Group.MaxUsers, InternetLabs: p.Group.InternetLabs, Known: p.Group.Known, TooLarge: p.Group.TooLarge,
			VPN:     resourcePodResponse{Blocks: p.Group.VPNBlocks, CPUMillicores: p.Group.VPN.CPUMillicores, MemoryBytes: p.Group.VPN.MemoryBytes},
			Gateway: resourcePodResponse{Blocks: p.Group.GatewayBlocks, CPUMillicores: p.Group.Gateway.CPUMillicores, MemoryBytes: p.Group.Gateway.MemoryBytes},
		},
	}
	for _, t := range p.Tasks {
		out.Tasks = append(out.Tasks, resourcePlanTaskResponse{
			EventExerciseID: t.EventExerciseID, ExerciseID: t.ExerciseID, ExerciseName: t.ExerciseName, Range: rangeResponse(t.Range),
			Reserved: totalsResponse(t.Reserved), ResourceHeavy: t.Heavy, InternetLab: t.InternetLab, NoAgentFits: t.NoAgentFits,
		})
	}
	return out
}

// parseFinishCountdownMode keeps an omitted mode as nil (the current value); an unknown name stays as sent and is
// refused by the config validation.
func parseFinishCountdownMode(raw *string) *eventConfigModel.FinishCountdownMode {
	if raw == nil {
		return nil
	}
	mode := eventConfigModel.FinishCountdownMode(*raw)
	return &mode
}
