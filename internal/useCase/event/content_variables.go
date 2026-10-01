package event

import (
	"time"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// resolveContentVariables builds native values for a content renderer. Dates
// remain time.Time values so JSON serialization and frontend formatting keep
// timezone information intact; unset optional settings are represented by nil.
func resolveContentVariables(event eventModel.Event, config eventConfigModel.EventConfig, statistics eventContentModel.Statistics, now time.Time) map[string]any {
	phase := event.Lifecycle.Status(now)
	values := map[string]any{
		"event.name":                     event.Name,
		"event.tag":                      event.Tag,
		"event.previewDescription":       config.PreviewDescription,
		"event.publishAt":                event.Lifecycle.PublishAt,
		"event.startAt":                  event.Lifecycle.StartAt,
		"event.finishAt":                 optionalTime(event.Lifecycle.FinishAt),
		"event.withdrawAt":               optionalTime(event.Lifecycle.WithdrawAt),
		"event.manualFinishAt":           optionalTime(event.Lifecycle.ManualFinishAt),
		"event.effectiveFinishAt":        optionalTime(event.Lifecycle.EffectiveFinishAt()),
		"event.phase":                    lifecyclePhase(phase),
		"event.isPublished":              phase != eventModel.LifecycleNotPublished,
		"event.isStarted":                phase == eventModel.LifecycleStarted,
		"event.isFinished":               phase == eventModel.LifecycleFinished,
		"event.isWithdrawn":              phase == eventModel.LifecycleWithdrawn,
		"event.runtimeOpen":              event.Lifecycle.RuntimeOpen(now),
		"event.registrationOpen":         config.Registration != eventConfigModel.RegistrationClose && event.Lifecycle.RegistrationOpen(now),
		"event.rosterOpen":               event.Lifecycle.RosterOpen(now),
		"event.participation":            participationName(config.Participation),
		"event.registration":             registrationName(config.Registration),
		"event.joinPolicy":               joinPolicyName(event.Lifecycle.JoinPolicy),
		"event.maxTeamSize":              int64(config.MaxTeamSize),
		"event.minTeamSize":              optionalInt32(config.MinTeamSize),
		"event.maxTeams":                 optionalInt32(config.MaxTeams),
		"event.scoreboardVisibility":     visibilityName(config.ScoreboardVisibility),
		"event.participantsVisibility":   visibilityName(config.ParticipantsVisibility),
		"event.scoringProfile":           scoringProfileName(event.ScoringProfile.Mode),
		"event.forceEventScoring":        event.ForceEventScoring,
		"event.teamCount":                statistics.TeamCount,
		"event.approvedTeamCount":        statistics.ApprovedTeamCount,
		"event.participantCount":         statistics.ParticipantCount,
		"event.approvedParticipantCount": statistics.ApprovedParticipantCount,
		"event.challengeCount":           statistics.ChallengeCount,
		"event.availableChallengeCount":  statistics.ChallengeCount,
		"event.solvedChallengeCount":     statistics.SolvedChallengeCount,
		"event.solveCount":               statistics.SolveCount,
	}
	if config.Participation != nil && *config.Participation == eventConfigModel.ParticipationIndividual {
		values["event.registrationUnitCount"] = statistics.ApprovedParticipantCount
	} else if config.Participation != nil {
		values["event.registrationUnitCount"] = statistics.ApprovedTeamCount
	} else {
		values["event.registrationUnitCount"] = nil
	}
	return values
}

func optionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalInt32(value *int32) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func lifecyclePhase(value eventModel.LifecycleStatus) string {
	switch value {
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

func participationName(value *eventConfigModel.Participation) any {
	if value == nil {
		return nil
	}
	if *value == eventConfigModel.ParticipationIndividual {
		return "individual"
	}
	return "team"
}

func registrationName(value eventConfigModel.Registration) string {
	switch value {
	case eventConfigModel.RegistrationApproval:
		return "approval"
	case eventConfigModel.RegistrationOpen:
		return "open"
	default:
		return "closed"
	}
}

func joinPolicyName(value eventModel.JoinPolicy) string {
	if value == eventModel.JoinPolicyRolling {
		return "rolling"
	}
	return "locked_at_start"
}

func visibilityName(value eventConfigModel.Visibility) string {
	switch value {
	case eventConfigModel.VisibilityPrivate:
		return "private"
	case eventConfigModel.VisibilityPublic:
		return "public"
	default:
		return "hidden"
	}
}

func scoringProfileName(value eventModel.ScoringMode) string {
	switch value {
	case eventModel.ScoringPopularityCurve:
		return "popularity_curve"
	case eventModel.ScoringFirstSolvesLadder:
		return "first_solves_ladder"
	case eventModel.ScoringTimeDecay:
		return "time_decay"
	default:
		return "static"
	}
}
