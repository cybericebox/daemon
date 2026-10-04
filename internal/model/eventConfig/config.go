// internal/model/eventConfig/config.go

// Package eventConfigModel is the DOMAIN layer of a platform event's
// configuration: the participant-visible settings that hang off the tenancy
// Event (1:1). Participation can change until publication; other fields are mutable.
package eventConfigModel

import (
	"net/url"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

const previewDescriptionMaxLen = 1000

// defaultMaxTeamSize is the team size limit of a new event (EVENT_DEFAULT_MAX_TEAM_SIZE, set once at start).
var defaultMaxTeamSize int32 = 5

// SetDefaultMaxTeamSize sets the team size limit of a new event.
func SetDefaultMaxTeamSize(n int32) { defaultMaxTeamSize = n }

// DefaultMaxTeamSize is the team size limit of a new event, and what a legacy row without one gets.
func DefaultMaxTeamSize() int32 { return defaultMaxTeamSize }

type Participation int32

const (
	ParticipationIndividual Participation = iota
	ParticipationTeam
)

func (p Participation) valid() bool { return p == ParticipationIndividual || p == ParticipationTeam }

type Registration int32

const (
	RegistrationClose Registration = iota
	RegistrationApproval
	RegistrationOpen
)

func (r Registration) valid() bool {
	return r == RegistrationClose || r == RegistrationApproval || r == RegistrationOpen
}

type Visibility int32

const (
	VisibilityHidden Visibility = iota
	VisibilityPrivate
	VisibilityPublic
)

func (v Visibility) valid() bool {
	return v == VisibilityHidden || v == VisibilityPrivate || v == VisibilityPublic
}

// EventConfig is the 1:1 configuration aggregate for a tenancy Event. The
// availability window (StartTime/FinishTime) lives on the Event, not here.
type EventConfig struct {
	EventID uuid.UUID

	Participation          *Participation // nil until configured; locked after publication
	Registration           Registration
	ScoreboardVisibility   Visibility
	ParticipantsVisibility Visibility
	PreviewDescription     string
	PreviewPicture         string
	MaxTeamSize            int32
	MinTeamSize            *int32
	MaxTeams               *int32
	AllowPseudonyms        bool
	// ShowDifficulty is a participant presentation toggle. HintsDisabled hides
	// hints of every task (it can only hide them; each task decides otherwise).
	ShowDifficulty bool
	HintsDisabled  bool
	// HintChargeMode: how unlocked hint costs are charged (W4).
	HintChargeMode HintChargeMode
	// MaxFlagAttempts is the wrong flag submissions a team may make per task; nil is unlimited. A task can
	// override it.
	MaxFlagAttempts *int32
	Theme           Theme
	// StandTiming schedules team stands; it only matters when the admin
	// allowed infrastructure challenges on the event.
	StandTiming eventStandModel.Timing
	// Results is the public results page presentation and freeze policy.
	Results ResultsSettings
	// Countdown is the participant countdown to the start and the finish.
	Countdown CountdownSettings
	// TaskRevealMode is how infrastructure tasks are revealed; chosen before the event starts.
	TaskRevealMode TaskRevealMode

	CreatedAt time.Time
	UpdatedAt time.Time
	UpdatedBy uuid.NullUUID
}

// TaskRevealMode says how infrastructure tasks are revealed to the teams.
type TaskRevealMode string

const (
	// RevealAllReady reveals a task when it is ready for every team (the default, an olympiad): a task
	// that does not fit is skipped and its dependants are blocked.
	RevealAllReady TaskRevealMode = "all_ready"
	// RevealAsReady reveals a task to each team as soon as its own lab is ready.
	RevealAsReady TaskRevealMode = "as_ready"
)

// Valid reports whether the mode is one of the two.
func (m TaskRevealMode) Valid() bool { return m == RevealAllReady || m == RevealAsReady }

// ConfigInput carries the always-mutable fields for Update (never Participation).
type ConfigInput struct {
	Registration           Registration
	ScoreboardVisibility   Visibility
	ParticipantsVisibility Visibility
	PreviewDescription     string
	PreviewPicture         string
	MaxTeamSize            int32
	MinTeamSize            *int32
	MaxTeams               *int32
	AllowPseudonyms        bool
	ShowDifficulty         bool
	HintsDisabled          bool
	HintChargeMode         HintChargeMode
	MaxFlagAttempts        *int32
	Countdown              CountdownSettings
}

// HintChargeMode says how the cost of an unlocked hint is charged.
type HintChargeMode int16

const (
	// HintChargeReward (A, default): costs unlocked before the solve reduce
	// the reward of that solve, never below 0.
	HintChargeReward HintChargeMode = iota
	// HintChargeBalance (B): a paid unlock is deducted from the score at once.
	HintChargeBalance
)

func (m HintChargeMode) Valid() bool { return m == HintChargeReward || m == HintChargeBalance }

// NewEventConfig builds the 1:1 config row created alongside a new Event, with
// safe closed/hidden defaults and Participation unset.
func NewEventConfig(eventID uuid.UUID, now time.Time) EventConfig {
	return EventConfig{
		EventID:                eventID,
		Participation:          nil,
		Registration:           RegistrationClose,
		ScoreboardVisibility:   VisibilityHidden,
		ParticipantsVisibility: VisibilityHidden,
		MaxTeamSize:            defaultMaxTeamSize,
		ShowDifficulty:         true,
		Theme:                  DefaultTheme(),
		StandTiming:            eventStandModel.DefaultTiming(),
		Results:                DefaultResultsSettings(),
		Countdown:              DefaultCountdownSettings(),
		TaskRevealMode:         RevealAllReady,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

// SetParticipation changes the participation type before publication. The
// caller derives published from the event lifecycle, not from UI state.
func (c *EventConfig) SetParticipation(p Participation, published bool, now time.Time, by uuid.UUID) error {
	if !p.valid() {
		return ErrParticipationInvalid.Err()
	}
	if c.Participation != nil && *c.Participation == p {
		return nil
	}
	if published {
		return ErrParticipationLocked.Err()
	}
	c.Participation = &p
	c.touch(now, by)
	return nil
}

// Update applies the always-mutable fields in one place, validating each, and
// touches UpdatedAt/By. It never touches Participation.
func (c *EventConfig) Update(in ConfigInput, now time.Time, by uuid.UUID) error {
	// Zero is omitted by the legacy configuration API. Preserve the existing
	// limit until that API is extended with explicit team-limit fields.
	if in.MaxTeamSize == 0 {
		in.MaxTeamSize = c.MaxTeamSize
	}
	// A zero value means the caller does not manage countdowns: keep them.
	if in.Countdown == (CountdownSettings{}) {
		in.Countdown = c.Countdown
	}
	if !in.Registration.valid() {
		return ErrRegistrationInvalid.Err()
	}
	if !in.ScoreboardVisibility.valid() || !in.ParticipantsVisibility.valid() {
		return ErrVisibilityInvalid.Err()
	}
	if in.MaxTeamSize <= 0 || (in.MinTeamSize != nil && (*in.MinTeamSize <= 0 || *in.MinTeamSize > in.MaxTeamSize)) ||
		(in.MaxTeams != nil && *in.MaxTeams <= 0) {
		return ErrTeamLimitsInvalid.Err()
	}
	desc := strings.TrimSpace(in.PreviewDescription)
	if len(desc) > previewDescriptionMaxLen {
		return ErrPreviewDescriptionTooLong.Err()
	}
	pic := strings.TrimSpace(in.PreviewPicture)
	if pic != "" {
		u, uErr := url.Parse(pic)
		if uErr != nil || u.Scheme != "https" || u.Host == "" {
			return ErrPreviewPictureInvalid.Err()
		}
	}

	c.Registration = in.Registration
	c.ScoreboardVisibility = in.ScoreboardVisibility
	c.ParticipantsVisibility = in.ParticipantsVisibility
	c.PreviewDescription = desc
	c.PreviewPicture = pic
	c.MaxTeamSize = in.MaxTeamSize
	c.MinTeamSize = cloneLimit(in.MinTeamSize)
	c.MaxTeams = cloneLimit(in.MaxTeams)
	c.AllowPseudonyms = in.AllowPseudonyms
	c.ShowDifficulty = in.ShowDifficulty
	c.HintsDisabled = in.HintsDisabled
	if !in.HintChargeMode.Valid() {
		return ErrHintChargeModeInvalid.Err()
	}
	if !in.Countdown.valid() {
		return ErrCountdownSettingsInvalid.Err()
	}
	if err := challengeAttempt.CheckAttemptLimit(in.MaxFlagAttempts); err != nil {
		return err
	}
	c.HintChargeMode = in.HintChargeMode
	c.MaxFlagAttempts = cloneLimit(in.MaxFlagAttempts)
	c.Countdown = in.Countdown
	c.touch(now, by)
	return nil
}

// SetTaskRevealMode changes how infrastructure tasks are revealed. It is chosen before the event starts
// (started = false); after the start only the current value is accepted, as revealed tasks are never hidden.
func (c *EventConfig) SetTaskRevealMode(mode TaskRevealMode, started bool, now time.Time, by uuid.UUID) error {
	if !mode.Valid() {
		return ErrTaskRevealModeInvalid.Err()
	}
	if mode == c.TaskRevealMode {
		return nil
	}
	if started {
		return ErrTaskRevealModeLocked.Err()
	}
	c.TaskRevealMode = mode
	c.touch(now, by)
	return nil
}

// SetStandTiming changes only the stand schedule, so saving it from the
// «Стенди» page cannot overwrite concurrent general-setting changes.
func (c *EventConfig) SetStandTiming(timing eventStandModel.Timing, now time.Time, by uuid.UUID) error {
	validated, err := eventStandModel.NewTiming(timing.DeployLeadMinutes, timing.TeardownDelayMinutes)
	if err != nil {
		return err
	}
	c.StandTiming = validated
	c.touch(now, by)
	return nil
}

// SetResultsSettings changes the results page settings and the scoreboard
// visibility together (the «Налаштування результатів» page) without touching
// other fields. The moderator's early opening is kept.
func (c *EventConfig) SetResultsSettings(visibility Visibility, in ResultsSettings, now time.Time, by uuid.UUID) error {
	if !visibility.valid() {
		return ErrVisibilityInvalid.Err()
	}
	if !in.valid() {
		return ErrResultsSettingsInvalid.Err()
	}
	in.OpenedAt = c.Results.OpenedAt
	c.ScoreboardVisibility = visibility
	c.Results = in
	c.touch(now, by)
	return nil
}

// SetResultsOpened records («Відкрити підсумки») or withdraws a moderator's
// early end of the freeze. Opening twice keeps the first moment.
func (c *EventConfig) SetResultsOpened(opened bool, now time.Time, by uuid.UUID) {
	switch {
	case opened && c.Results.OpenedAt == nil:
		at := now
		c.Results.OpenedAt = &at
	case !opened:
		c.Results.OpenedAt = nil
	default:
		return
	}
	c.touch(now, by)
}

// SetPreviewPicture updates only the preview image, so uploading it cannot
// overwrite concurrent changes to registration, participation, or limits.
func (c *EventConfig) SetPreviewPicture(raw string, now time.Time, by uuid.UUID) error {
	pic := strings.TrimSpace(raw)
	if pic != "" {
		u, err := url.Parse(pic)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return ErrPreviewPictureInvalid.Err()
		}
	}
	c.PreviewPicture = pic
	c.touch(now, by)
	return nil
}

// DefaultMinTeamSize applies to team mode when no minimum is configured.
const DefaultMinTeamSize int32 = 2

// EffectiveMinTeamSize is the admission minimum: the configured value, else
// DefaultMinTeamSize (capped by the maximum) in team mode, else 1. The SQL
// function event_min_team_size mirrors this rule.
func (c EventConfig) EffectiveMinTeamSize() int32 {
	if c.MinTeamSize != nil {
		return *c.MinTeamSize
	}
	if c.Participation != nil && *c.Participation == ParticipationTeam {
		return min(DefaultMinTeamSize, c.MaxTeamSize)
	}
	return 1
}

// IsTeamMode reports team participation; unset participation is not team mode.
func (c EventConfig) IsTeamMode() bool {
	return c.Participation != nil && *c.Participation == ParticipationTeam
}

func cloneLimit(value *int32) *int32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (c *EventConfig) touch(now time.Time, by uuid.UUID) {
	c.UpdatedAt = now
	c.UpdatedBy = uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
}
