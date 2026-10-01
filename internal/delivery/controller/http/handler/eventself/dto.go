package eventself

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type ownChallengeResponse struct {
	ID               uuid.UUID       `json:"ID"`
	EventChallengeID uuid.UUID       `json:"EventChallengeID"`
	Snapshot         json.RawMessage `json:"Snapshot" swaggertype:"object"`
	Readiness        int16           `json:"Readiness"`
	SolvedAt         *time.Time      `json:"SolvedAt"`
	Points           int32           `json:"Points"`
	Order            int32           `json:"Order"`
	GroupID          *uuid.UUID      `json:"GroupID"`
	GroupName        string          `json:"GroupName"`
	GroupOrder       int32           `json:"GroupOrder"`
	ContentUpdatedAt *time.Time      `json:"ContentUpdatedAt"`
	Infrastructure   bool            `json:"Infrastructure"`
	HintsEnabled     bool            `json:"HintsEnabled"`
	// Locked: a prerequisite is unsolved; Snapshot then holds only name and
	// difficulty and Files is empty.
	Locked        bool                            `json:"Locked"`
	Prerequisites []challengePrerequisiteResponse `json:"Prerequisites"`
	Files         []challengeFileResponse         `json:"Files"`
	// SolveCount is null when event results are not available to the caller.
	SolveCount *int64 `json:"SolveCount"`
	// Hints (hints enabled): Content only once the team unlocked the hint.
	Hints         []ownHintResponse `json:"Hints"`
	HintCostTotal int32             `json:"HintCostTotal"`
}

// ownHintResponse: participants see the price, never the hint level (that is
// for organizers and moderators).
type ownHintResponse struct {
	ID             uuid.UUID  `json:"ID"`
	Cost           int32      `json:"Cost"`
	Unlocked       bool       `json:"Unlocked"`
	Content        *string    `json:"Content"`
	UnlockedAt     *time.Time `json:"UnlockedAt"`
	UnlockedByName string     `json:"UnlockedByName"`
}

func toOwnHintResponse(hint eventUseCase.OwnHintView) ownHintResponse {
	return ownHintResponse{ID: hint.ID, Cost: hint.Cost, Unlocked: hint.Unlocked, Content: hint.Content, UnlockedAt: hint.UnlockedAt, UnlockedByName: hint.UnlockedByName}
}

// ToOwnHintResponses maps a participant board's hints.
func ToOwnHintResponses(hints []eventUseCase.OwnHintView) []ownHintResponse {
	out := make([]ownHintResponse, 0, len(hints))
	for _, hint := range hints {
		out = append(out, toOwnHintResponse(hint))
	}
	return out
}

type challengePrerequisiteResponse struct {
	EventChallengeID uuid.UUID `json:"EventChallengeID"`
	Name             string    `json:"Name"`
	Solved           bool      `json:"Solved"`
}
type challengeFileResponse struct {
	FileID uuid.UUID `json:"FileID"`
	Name   string    `json:"Name"`
	Size   int64     `json:"Size"`
}
type challengeSolveResponse struct {
	TeamName string    `json:"TeamName"`
	SolvedAt time.Time `json:"SolvedAt"`
	Own      bool      `json:"Own"`
	// FirstBlood marks the earliest solve among the visible teams.
	FirstBlood bool `json:"FirstBlood"`
}

// teamMemberResponse.Role is participantModel.TeamRole: 0 captain, 1 member.
type teamMemberResponse struct {
	UserID      uuid.UUID `json:"UserID"`
	DisplayName string    `json:"DisplayName"`
	Role        int16     `json:"Role"`
	Own         bool      `json:"Own"`
	Pending     bool      `json:"Pending"`
}
type participantAnswersResponse struct {
	Form     participantFormResponse `json:"Form"`
	Answers  map[string]any          `json:"Answers"`
	Editable bool                    `json:"Editable"`
	// Missing lists the required fields the caller still has to fill;
	// Blocking: solution submissions wait until they do.
	Missing  []string `json:"Missing"`
	Blocking bool     `json:"Blocking"`
}
type updateParticipantAnswersRequest struct {
	Answers map[string]any `json:"Answers"`
}

func toParticipantAnswersResponse(v eventUseCase.OwnParticipantAnswersView) participantAnswersResponse {
	missing := v.Missing
	if missing == nil {
		missing = []string{}
	}
	return participantAnswersResponse{Form: toParticipantFormResponse(v.Form), Answers: participantVisibleAnswers(v.Form.Document, v.Answers), Editable: v.Editable, Missing: missing, Blocking: v.Blocking}
}

type scoreboardEntryResponse struct {
	Rank        int32      `json:"Rank"`
	TeamID      uuid.UUID  `json:"TeamID"`
	TeamName    string     `json:"TeamName"`
	Points      int64      `json:"Points"`
	Solved      int64      `json:"Solved"`
	LastSolveAt *time.Time `json:"LastSolveAt"`
}
type ownScoreTimelineEntryResponse struct {
	EventChallengeID uuid.UUID `json:"EventChallengeID"`
	Points           int32     `json:"Points"`
	SolvedAt         time.Time `json:"SolvedAt"`
}
type ownResultsResponse struct {
	Entry    scoreboardEntryResponse         `json:"Entry"`
	Timeline []ownScoreTimelineEntryResponse `json:"Timeline"`
}
type teamResultAttemptResponse struct {
	ID, EventTeamID, UserID, TeamChallengeID, EventChallengeID uuid.UUID
	ParticipantName, Answer                                    string
	AutomaticCorrect, Correct                                  bool
	Decision                                                   string
	ReceivedAt                                                 time.Time
}
type ownTeamResultsResponse struct {
	Entry    scoreboardEntryResponse
	Timeline []ownScoreTimelineEntryResponse
	Attempts []teamResultAttemptResponse
}
type eventScoreTimelineEntryResponse struct {
	EventTeamID      uuid.UUID `json:"EventTeamID"`
	EventChallengeID uuid.UUID `json:"EventChallengeID"`
	ChallengeName    string    `json:"ChallengeName"`
	Points           int32     `json:"Points"`
	SolvedAt         time.Time `json:"SolvedAt"`
}
type resultsSnapshotResponse struct {
	Revision    int64                             `json:"Revision"`
	GeneratedAt time.Time                         `json:"GeneratedAt"`
	Scoreboard  []scoreboardEntryResponse         `json:"Scoreboard"`
	Timeline    []eventScoreTimelineEntryResponse `json:"Timeline"`
	TotalTeams  int                               `json:"TotalTeams"`
	Freeze      resultsFreezeResponse             `json:"Freeze"`
	Display     resultsDisplayResponse            `json:"Display"`
}

// resultsFreezeResponse: Applied tells whether this response is the frozen
// table (the viewer is not a moderator and the freeze is active).
type resultsFreezeResponse struct {
	Enabled  bool       `json:"Enabled"`
	FrozenAt *time.Time `json:"FrozenAt"`
	FinishAt *time.Time `json:"FinishAt"`
	OpenedAt *time.Time `json:"OpenedAt"`
	Active   bool       `json:"Active"`
	Applied  bool       `json:"Applied"`
}
type resultsDisplayResponse struct {
	ChartEnabled bool   `json:"ChartEnabled"`
	ChartTeams   int32  `json:"ChartTeams"`
	RowsLimit    *int32 `json:"RowsLimit"`
}
type participantFormResponse struct {
	Version  int32 `json:"Version"`
	Enabled  bool  `json:"Enabled"`
	Required bool  `json:"Required"`
	Document any   `json:"Document"`
}

// participantFormWithAnswersResponse adds the caller's stored answers
// (prefilled by organizers or submitted earlier) to the form.
type participantFormWithAnswersResponse struct {
	participantFormResponse
	Answers map[string]any `json:"Answers"`
}
type publicEventContentResponse struct {
	Landing   any            `json:"Landing"`
	Live      any            `json:"Live"`
	Variables map[string]any `json:"Variables"`
}
type publicEventPageResponse struct {
	Page      any            `json:"Page"`
	Variables map[string]any `json:"Variables"`
}

func toPublicEventContentResponse(v eventUseCase.PublicEventContentView) publicEventContentResponse {
	return publicEventContentResponse{Landing: v.Landing, Live: v.Live, Variables: v.Variables}
}
func toPublicEventPageResponse(v eventUseCase.PublicEventPageView) publicEventPageResponse {
	return publicEventPageResponse{Page: v.Page, Variables: v.Variables}
}

type submitParticipantFormRequest struct {
	Answers map[string]any `json:"Answers"`
}
type submitEventFormResponseRequest struct {
	FormVersionID uuid.UUID      `json:"FormVersionID"`
	Answers       map[string]any `json:"Answers"`
}

func (r submitEventFormResponseRequest) toInput() eventUseCase.SubmitEventFormResponseInput {
	return eventUseCase.SubmitEventFormResponseInput{FormVersionID: r.FormVersionID, Answers: r.Answers}
}

type participantFormAnswerResponse struct {
	FormVersion int32          `json:"FormVersion"`
	Answers     map[string]any `json:"Answers"`
	SubmittedAt time.Time      `json:"SubmittedAt"`
}

// toParticipantFormResponse is what a participant receives: staff-only
// questions are removed here whatever the use case handed over.
func toParticipantFormResponse(v eventUseCase.ParticipantFormView) participantFormResponse {
	v = v.ForParticipant()
	return participantFormResponse{Version: v.Version, Enabled: v.Enabled, Required: v.Required, Document: v.Document}
}
func toParticipantFormAnswerResponse(v eventUseCase.ParticipantFormAnswerView) participantFormAnswerResponse {
	return participantFormAnswerResponse{FormVersion: v.FormVersion, Answers: v.Answers, SubmittedAt: v.SubmittedAt}
}

type labStatusResponse struct {
	Phase        string              `json:"Phase"`
	Ready        bool                `json:"Ready"`
	VPNCIDR      string              `json:"VPNCIDR"`
	InternetCIDR string              `json:"InternetCIDR"`
	Access       []labAccessResponse `json:"Access"`
	// Queue is set while the Lab waits in the launch queue (Phase Queued); otherwise null.
	Queue *labview.QueueResponse `json:"Queue"`
}

type labVPNConfigResponse struct {
	Config string `json:"Config"`
}

type standStatusResponse struct {
	Status string `json:"Status"`
}
type labVPNStatusResponse struct {
	GatewayIP string `json:"GatewayIP"`
	ProbeURL  string `json:"ProbeURL"`
}
type labAccessResponse struct {
	Device   string `json:"Device"`
	Port     int32  `json:"Port"`
	Protocol string `json:"Protocol"`
	URL      string `json:"URL"`
}

func toOwnChallengeResponse(v eventUseCase.OwnChallengeView) ownChallengeResponse {
	out := ownChallengeResponse{ID: v.ID, EventChallengeID: v.EventChallengeID, Snapshot: v.Snapshot, Readiness: int16(v.Readiness), SolvedAt: v.SolvedAt, Points: v.Points, Order: v.Order, GroupID: v.GroupID, GroupName: v.GroupName, GroupOrder: v.GroupOrder,
		ContentUpdatedAt: v.ContentUpdatedAt, Infrastructure: v.Infrastructure, HintsEnabled: v.HintsEnabled, Locked: v.Locked, SolveCount: v.SolveCount,
		Prerequisites: make([]challengePrerequisiteResponse, 0, len(v.Prerequisites)), Files: make([]challengeFileResponse, 0, len(v.Files)),
		Hints: ToOwnHintResponses(v.Hints), HintCostTotal: v.HintCostTotal}
	for _, p := range v.Prerequisites {
		out.Prerequisites = append(out.Prerequisites, challengePrerequisiteResponse{EventChallengeID: p.EventChallengeID, Name: p.Name, Solved: p.Solved})
	}
	for _, f := range v.Files {
		out.Files = append(out.Files, challengeFileResponse{FileID: f.FileID, Name: f.Name, Size: f.Size})
	}
	return out
}
func toScoreboardEntryResponse(v eventUseCase.ScoreboardEntryView) scoreboardEntryResponse {
	return scoreboardEntryResponse{Rank: v.Rank, TeamID: v.TeamID, TeamName: v.TeamName, Points: v.Points, Solved: v.Solved, LastSolveAt: v.LastSolveAt}
}
func toOwnScoreTimelineEntryResponse(v eventUseCase.OwnScoreTimelineEntryView) ownScoreTimelineEntryResponse {
	return ownScoreTimelineEntryResponse{EventChallengeID: v.EventChallengeID, Points: v.Points, SolvedAt: v.SolvedAt}
}
func toOwnResultsResponse(v eventUseCase.OwnResultsView) ownResultsResponse {
	timeline := make([]ownScoreTimelineEntryResponse, 0, len(v.Timeline))
	for _, item := range v.Timeline {
		timeline = append(timeline, toOwnScoreTimelineEntryResponse(item))
	}
	return ownResultsResponse{Entry: toScoreboardEntryResponse(v.Entry), Timeline: timeline}
}
func toOwnTeamResultsResponse(v eventUseCase.OwnTeamResultsView) ownTeamResultsResponse {
	timeline := make([]ownScoreTimelineEntryResponse, 0, len(v.Timeline))
	for _, x := range v.Timeline {
		timeline = append(timeline, toOwnScoreTimelineEntryResponse(x))
	}
	attempts := make([]teamResultAttemptResponse, 0, len(v.Attempts))
	for _, x := range v.Attempts {
		attempts = append(attempts, teamResultAttemptResponse{ID: x.ID, EventTeamID: x.EventTeamID, UserID: x.UserID, TeamChallengeID: x.TeamChallengeID, EventChallengeID: x.EventChallengeID, ParticipantName: x.ParticipantName, Answer: x.Answer, AutomaticCorrect: x.AutomaticCorrect, Correct: x.Correct, Decision: x.Decision.String(), ReceivedAt: x.ReceivedAt})
	}
	return ownTeamResultsResponse{Entry: toScoreboardEntryResponse(v.Entry), Timeline: timeline, Attempts: attempts}
}
func toResultsSnapshotResponse(v eventUseCase.ResultsSnapshotView) resultsSnapshotResponse {
	entries := make([]scoreboardEntryResponse, 0, len(v.Scoreboard))
	for _, item := range v.Scoreboard {
		entries = append(entries, toScoreboardEntryResponse(item))
	}
	timeline := make([]eventScoreTimelineEntryResponse, 0, len(v.Timeline))
	for _, item := range v.Timeline {
		timeline = append(timeline, eventScoreTimelineEntryResponse{EventTeamID: item.EventTeamID, EventChallengeID: item.EventChallengeID, ChallengeName: item.ChallengeName, Points: item.Points, SolvedAt: item.SolvedAt})
	}
	f := v.Freeze
	return resultsSnapshotResponse{Revision: v.Revision, GeneratedAt: v.GeneratedAt, Scoreboard: entries, Timeline: timeline, TotalTeams: v.TotalTeams,
		Freeze:  resultsFreezeResponse{Enabled: f.Enabled, FrozenAt: f.FrozenAt, FinishAt: f.FinishAt, OpenedAt: f.OpenedAt, Active: f.Active, Applied: f.Applied},
		Display: resultsDisplayResponse{ChartEnabled: v.Display.ChartEnabled, ChartTeams: v.Display.ChartTeams, RowsLimit: v.Display.RowsLimit}}
}

// eventInfoResponse is the participant-facing view of an event: the tenancy
// window plus the participant-visible config settings. Deliberately NOT the
// landing-page content (that lives in the pages slice, not here).
type eventInfoResponse struct {
	EventID             uuid.UUID              `json:"EventID"`
	Tag                 string                 `json:"Tag"`
	Name                string                 `json:"Name"`
	StartTime           time.Time              `json:"StartTime"`
	FinishTime          *time.Time             `json:"FinishTime"`
	Status              int32                  `json:"Status"`
	Participation       *int32                 `json:"Participation"`
	Registration        int32                  `json:"Registration"`
	CanViewResults      bool                   `json:"CanViewResults"`
	ResultsAvailability string                 `json:"ResultsAvailability"`
	CanViewParticipants bool                   `json:"CanViewParticipants"`
	PreviewDescription  string                 `json:"PreviewDescription"`
	PreviewPicture      string                 `json:"PreviewPicture"`
	LogoURL             string                 `json:"LogoURL"`
	Theme               eventConfigModel.Theme `json:"Theme"`
	// Countdown policy for the challenges and results pages.
	ShowStartCountdown     bool  `json:"ShowStartCountdown"`
	ShowFinishCountdown    bool  `json:"ShowFinishCountdown"`
	FinishCountdownMinutes int32 `json:"FinishCountdownMinutes"`
}

type participantEventInfoResponse struct {
	EventID             uuid.UUID  `json:"EventID"`
	UseVPN              bool       `json:"UseVPN"`
	CanViewResults      bool       `json:"CanViewResults"`
	ResultsAvailability string     `json:"ResultsAvailability"`
	CanViewParticipants bool       `json:"CanViewParticipants"`
	Participation       *int32     `json:"Participation"`
	RealName            string     `json:"RealName"`
	Pseudonym           *string    `json:"Pseudonym"`
	DisplayName         string     `json:"DisplayName"`
	AllowPseudonyms     bool       `json:"AllowPseudonyms"`
	PseudonymEditable   bool       `json:"PseudonymEditable"`
	TeamID              *uuid.UUID `json:"TeamID"`
	TeamAdmitted        *bool      `json:"TeamAdmitted"`
	MinTeamSize         int32      `json:"MinTeamSize"`
	MaxTeamSize         int32      `json:"MaxTeamSize"`
	// ShowDifficulty and HintsDisabled (hints hidden for every task).
	ShowDifficulty bool `json:"ShowDifficulty"`
	HintsDisabled  bool `json:"HintsDisabled"`
	// HasInfrastructureChallenges: infrastructure allowed AND an active
	// exercise has a lab topology.
	HasInfrastructureChallenges bool `json:"HasInfrastructureChallenges"`
	// HintChargeMode: reward (hint costs reduce the solve) | balance.
	HintChargeMode string `json:"HintChargeMode"`
}

func toParticipantEventInfoResponse(v eventUseCase.ParticipantEventInfoView) participantEventInfoResponse {
	var participation *int32
	if v.Participation != nil {
		value := int32(*v.Participation)
		participation = &value
	}
	return participantEventInfoResponse{
		EventID: v.EventID, UseVPN: v.UseVPN, CanViewResults: v.CanViewResults, ResultsAvailability: string(v.ResultsAvailability), CanViewParticipants: v.CanViewParticipants,
		Participation: participation, RealName: v.RealName, Pseudonym: v.Pseudonym, DisplayName: v.DisplayName,
		AllowPseudonyms: v.AllowPseudonyms, PseudonymEditable: v.PseudonymEditable, TeamID: v.TeamID, TeamAdmitted: v.TeamAdmitted,
		MinTeamSize: v.MinTeamSize, MaxTeamSize: v.MaxTeamSize,
		ShowDifficulty: v.ShowDifficulty, HintsDisabled: v.HintsDisabled, HasInfrastructureChallenges: v.HasInfrastructureChallenges,
		HintChargeMode: hintChargeModeName(v.HintChargeMode),
	}
}

type setPseudonymRequest struct {
	Pseudonym *string `json:"Pseudonym"`
}

type participantNameResponse struct {
	Pseudonym   *string `json:"Pseudonym"`
	DisplayName string  `json:"DisplayName"`
}

type updateTeamFieldsRequest struct {
	Fields map[string]any `json:"Fields" binding:"required"`
}

// publicEventInfoResponse is the allowlisted event identity/theme needed by
// anonymous event pages. Team and infrastructure internals stay out of it.
type publicEventInfoResponse struct {
	EventID             uuid.UUID              `json:"EventID"`
	Tag                 string                 `json:"Tag"`
	Name                string                 `json:"Name"`
	StartTime           time.Time              `json:"StartTime"`
	FinishTime          *time.Time             `json:"FinishTime"`
	Status              int32                  `json:"Status"`
	Participation       *int32                 `json:"Participation"`
	Registration        int32                  `json:"Registration"`
	CanViewResults      bool                   `json:"CanViewResults"`
	ResultsAvailability string                 `json:"ResultsAvailability"`
	CanViewParticipants bool                   `json:"CanViewParticipants"`
	PreviewDescription  string                 `json:"PreviewDescription"`
	PreviewPicture      string                 `json:"PreviewPicture"`
	LogoURL             string                 `json:"LogoURL"`
	Theme               eventConfigModel.Theme `json:"Theme"`
	// Countdown policy for the challenges and results pages.
	ShowStartCountdown     bool  `json:"ShowStartCountdown"`
	ShowFinishCountdown    bool  `json:"ShowFinishCountdown"`
	FinishCountdownMinutes int32 `json:"FinishCountdownMinutes"`
}

func toPublicEventInfoResponse(v eventUseCase.EventInfoView) publicEventInfoResponse {
	var participation *int32
	if v.Participation != nil {
		value := int32(*v.Participation)
		participation = &value
	}
	return publicEventInfoResponse{
		EventID: v.EventID, Tag: v.Tag, Name: v.Name,
		StartTime: v.StartTime, FinishTime: v.FinishTime, Status: int32(v.Status),
		Participation:       participation,
		Registration:        int32(v.Registration),
		CanViewResults:      v.ResultsAvailability == eventUseCase.ResultsAvailable,
		ResultsAvailability: string(v.ResultsAvailability),
		CanViewParticipants: v.ParticipantsVisibility == eventConfigModel.VisibilityPublic,
		PreviewDescription:  v.PreviewDescription, PreviewPicture: v.PreviewPicture,
		LogoURL:            v.LogoURL,
		Theme:              v.Theme,
		ShowStartCountdown: v.Countdown.ShowStart, ShowFinishCountdown: v.Countdown.ShowFinish, FinishCountdownMinutes: v.Countdown.FinishMinutes,
	}
}

func toEventInfoResponse(v eventUseCase.EventInfoView) eventInfoResponse {
	var participation *int32
	if v.Participation != nil {
		p := int32(*v.Participation)
		participation = &p
	}
	return eventInfoResponse{
		EventID:             v.EventID,
		Tag:                 v.Tag,
		Name:                v.Name,
		StartTime:           v.StartTime,
		FinishTime:          v.FinishTime,
		Status:              int32(v.Status),
		Participation:       participation,
		Registration:        int32(v.Registration),
		CanViewResults:      v.ResultsAvailability == eventUseCase.ResultsAvailable,
		ResultsAvailability: string(v.ResultsAvailability),
		CanViewParticipants: v.ParticipantsVisibility == eventConfigModel.VisibilityPublic,
		PreviewDescription:  v.PreviewDescription,
		PreviewPicture:      v.PreviewPicture,
		LogoURL:             v.LogoURL,
		Theme:               v.Theme,
		ShowStartCountdown:  v.Countdown.ShowStart, ShowFinishCountdown: v.Countdown.ShowFinish, FinishCountdownMinutes: v.Countdown.FinishMinutes,
	}
}

// joinInfoResponse reports the caller's participation status for an event —
// enough for the client to render "not joined" / "pending" / "approved" /
// "rejected".
type joinInfoResponse struct {
	Status            int32      `json:"Status"`
	Invited           bool       `json:"Invited"`
	InvitedTeamName   string     `json:"InvitedTeamName"`
	InvitedTeamID     *uuid.UUID `json:"InvitedTeamID"`
	TeamUnavailable   bool       `json:"TeamUnavailable"`
	InvitationExpired bool       `json:"InvitationExpired"`
	// Participation is what the caller can do right now and why not; the event
	// site renders it instead of deriving the rules from dates. Only the
	// join/info read fills it.
	Participation *participationResponse `json:"Participation,omitempty"`
}

// capabilityResponse is one participation action: allowed, or a reason code.
type capabilityResponse struct {
	Allowed bool   `json:"Allowed"`
	Reason  string `json:"Reason"`
}

type participationResponse struct {
	// Phase: not_published, published, started, finished or withdrawn.
	Phase string `json:"Phase"`
	// RegistrationClosesAt is when registration and the team roster close together (null: not on its own).
	RegistrationClosesAt   *time.Time `json:"RegistrationClosesAt"`
	Staff                  bool       `json:"Staff"`
	RegistrationWindowOpen bool       `json:"RegistrationWindowOpen"`
	RosterOpen             bool       `json:"RosterOpen"`
	// RegistrationReason and RosterReason: why registration or the roster is closed ("" while open).
	RegistrationReason string             `json:"RegistrationReason"`
	RosterReason       string             `json:"RosterReason"`
	Register           capabilityResponse `json:"Register"`
	CreateTeam         capabilityResponse `json:"CreateTeam"`
	JoinTeam           capabilityResponse `json:"JoinTeam"`
	LeaveTeam          capabilityResponse `json:"LeaveTeam"`
	ManageTeam         capabilityResponse `json:"ManageTeam"`
	RemoveMember       capabilityResponse `json:"RemoveMember"`
	DisbandTeam        capabilityResponse `json:"DisbandTeam"`
	FormTeam           capabilityResponse `json:"FormTeam"`
	// TeamFormed: the caller's team roster is closed for good.
	TeamFormed  bool               `json:"TeamFormed"`
	EditAnswers capabilityResponse `json:"EditAnswers"`
	SeeTasks    capabilityResponse `json:"SeeTasks"`
	Submit      capabilityResponse `json:"Submit"`
}

func toCapabilityResponse(c eventUseCase.Capability) capabilityResponse {
	return capabilityResponse{Allowed: c.Allowed, Reason: string(c.Reason)}
}

func toParticipationResponse(s *eventUseCase.ParticipationState) *participationResponse {
	if s == nil {
		return nil
	}
	return &participationResponse{
		Phase: participationPhase(s.Phase), RegistrationClosesAt: s.RegistrationClosesAt, Staff: s.Staff,
		RegistrationWindowOpen: s.RegistrationWindowOpen, RosterOpen: s.RosterOpen,
		RegistrationReason: string(s.RegistrationReason), RosterReason: string(s.RosterReason),
		Register: toCapabilityResponse(s.Register), CreateTeam: toCapabilityResponse(s.CreateTeam), JoinTeam: toCapabilityResponse(s.JoinTeam),
		LeaveTeam: toCapabilityResponse(s.LeaveTeam), ManageTeam: toCapabilityResponse(s.ManageTeam),
		RemoveMember: toCapabilityResponse(s.RemoveMember), DisbandTeam: toCapabilityResponse(s.DisbandTeam),
		FormTeam: toCapabilityResponse(s.FormTeam), TeamFormed: s.TeamFormed,
		EditAnswers: toCapabilityResponse(s.EditAnswers), SeeTasks: toCapabilityResponse(s.SeeTasks), Submit: toCapabilityResponse(s.Submit),
	}
}

func participationPhase(s eventModel.LifecycleStatus) string {
	switch s {
	case eventModel.LifecyclePublished:
		return "published"
	case eventModel.LifecycleStarted:
		return "started"
	case eventModel.LifecycleFinished:
		return "finished"
	case eventModel.LifecycleWithdrawn:
		return "withdrawn"
	default:
		return "not_published"
	}
}

func toJoinInfoResponse(v eventUseCase.JoinInfoView) joinInfoResponse {
	return joinInfoResponse{Status: int32(v.Status), Invited: v.Invited, InvitedTeamName: v.InvitedTeamName, InvitedTeamID: v.InvitedTeamID, TeamUnavailable: v.TeamUnavailable, InvitationExpired: v.InvitationExpired, Participation: toParticipationResponse(v.Participation)}
}

type createTeamRequest struct {
	Name   string         `json:"Name"`
	Fields map[string]any `json:"Fields"`
}

type joinTeamRequest struct {
	JoinCode string `json:"JoinCode"`
}

type regenerateJoinCodeRequest struct {
	Expiry string `json:"Expiry"`
}

type updateTeamRequest struct {
	Name string `json:"Name"`
}

type transferCaptainRequest struct {
	UserID uuid.UUID `json:"UserID"`
}

type submitChallengeRequest struct {
	Answer string `json:"Answer"`
}
type submitChallengeResponse struct {
	Correct    bool `json:"Correct"`
	FirstSolve bool `json:"FirstSolve"`
}

type ownTeamResponse struct {
	ID       uuid.UUID `json:"ID"`
	Name     string    `json:"Name"`
	JoinCode string    `json:"JoinCode"`
	// JoinCode and JoinCodeExpiresAt are sent to the captain only.
	JoinCodeExpiresAt *time.Time     `json:"JoinCodeExpiresAt"`
	CaptainID         uuid.UUID      `json:"CaptainID"`
	MemberCount       int32          `json:"MemberCount"`
	ExtraFields       map[string]any `json:"ExtraFields"`
	Role              int16          `json:"Role"`
	Admitted          bool           `json:"Admitted"`
	MinTeamSize       int32          `json:"MinTeamSize"`
	MaxTeamSize       int32          `json:"MaxTeamSize"`
	// MissingFields lists the required team fields still unfilled;
	// BlockingFields: solution submissions wait until they are.
	MissingFields  []string `json:"MissingFields"`
	BlockingFields bool     `json:"BlockingFields"`
	// Formed: the roster is closed for good (FormedAt says when).
	Formed   bool       `json:"Formed"`
	FormedAt *time.Time `json:"FormedAt"`
}

func toOwnTeamResponse(v eventUseCase.OwnTeamView) ownTeamResponse {
	return ownTeamResponse{ID: v.ID, Name: v.Name, JoinCode: v.JoinCode, JoinCodeExpiresAt: v.JoinCodeExpiresAt, CaptainID: v.CaptainID, MemberCount: v.MemberCount, ExtraFields: v.ExtraFields, Role: int16(v.Role),
		Admitted: v.Admitted, MinTeamSize: v.MinTeamSize, MaxTeamSize: v.MaxTeamSize,
		MissingFields: nonNilKeys(v.MissingFields), BlockingFields: v.BlockingFields, Formed: v.Formed, FormedAt: v.FormedAt}
}

func hintChargeModeName(m eventConfigModel.HintChargeMode) string {
	if m == eventConfigModel.HintChargeBalance {
		return "balance"
	}
	return "reward"
}
