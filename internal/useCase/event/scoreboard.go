package event

import (
	"context"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/scoreboardRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// ResultsAccess carries optional request identity for an event-wide result
// read. A nil user means an anonymous caller, not an unapproved participant.
type ResultsAccess struct {
	UserID *uuid.UUID
	Role   rbac.Role
	// Screen: a live screen opened with this event's screen link — the
	// read-only staff view of this event's results and nothing else.
	Screen bool
}

// GetResultsSnapshot is the public results read. liveScreen marks the live
// screen (view=live): its presentation is not cut to the page settings and it
// honours the freeze only when the organizer chose so.
func (u *EventUseCase) GetResultsSnapshot(ctx context.Context, eventID uuid.UUID, access ResultsAccess, liveScreen bool) (ResultsSnapshotView, error) {
	now := time.Now()
	// The event-wide part is shared by all viewers; only the viewer is read
	// per request.
	shared, err := u.sharedResults(ctx, eventID)
	if err != nil {
		return ResultsSnapshotView{}, err
	}
	policy := shared.policy(u.loadResultsViewer(ctx, eventID, access))
	if err = policy.readErr(now, liveScreen); err != nil {
		return ResultsSnapshotView{}, err
	}
	// The cursor was read before the projections below. A result transaction
	// cannot expose a changed projection without committing its matching
	// revision/change record. If a new commit races the following reads, SSE
	// starts from this older cursor and replays its absolute, idempotent state
	// change; nothing can be missed. This is preferable to reading the cursor
	// last, which could acknowledge a revision whose projection was not
	// present in the snapshot. The projections are cached per revision, so
	// concurrent snapshots of one revision share one read.
	revision := shared.revision
	freeze := policy.freeze(now, liveScreen)
	cutoff := freeze.cutoff()
	own := policy.ownTeam()
	showRealNames := policy.mayShowRealNames()
	scores := revisionScores{u: u, revision: revision.Revision}
	entries, err := u.rankedScoreboard(ctx, scores, eventID, cutoff, own)
	if err != nil {
		return ResultsSnapshotView{}, err
	}
	timeline, err := u.scoreTimeline(ctx, scores, eventID, cutoff, own)
	if err != nil {
		return ResultsSnapshotView{}, err
	}
	generatedAt := revision.UpdatedAt
	if generatedAt.IsZero() {
		generatedAt = now.UTC()
	}
	settings := policy.cfg.Results
	view := ResultsSnapshotView{Revision: revision.Revision, GeneratedAt: generatedAt, Scoreboard: entries, Timeline: timeline, TotalTeams: len(entries), Freeze: freeze,
		Display: ResultsDisplayView{ChartEnabled: settings.ChartEnabled, ChartTeams: settings.ChartTeams, RowsLimit: settings.RowsLimit}}
	if !liveScreen {
		view.Scoreboard, view.Timeline = presentResults(entries, timeline, settings, own)
	}
	if !showRealNames {
		maskRealNames(view.Scoreboard, own)
	}
	return view, nil
}

// maskRealNames withholds the real names of individual participants (no
// pseudonym) from a viewer the participants visibility does not allow to see
// them; the viewer's own row keeps its name.
func maskRealNames(entries []ScoreboardEntryView, own *uuid.UUID) {
	for i := range entries {
		if entries[i].NameIsReal && (own == nil || *own != entries[i].TeamID) {
			entries[i].TeamName, entries[i].NameHidden = "", true
		}
	}
}

// presentResults applies the public page settings: the top RowsLimit rows
// (plus the viewer's own row below them) and the timeline of the charted
// teams only (top ChartTeams plus the own team), none when the chart is off.
func presentResults(entries []ScoreboardEntryView, timeline []EventScoreTimelineEntryView, settings eventConfigModel.ResultsSettings, own *uuid.UUID) ([]ScoreboardEntryView, []EventScoreTimelineEntryView) {
	isOwn := func(teamID uuid.UUID) bool { return own != nil && *own == teamID }
	rows := entries
	if settings.RowsLimit != nil && len(entries) > int(*settings.RowsLimit) {
		rows = append(make([]ScoreboardEntryView, 0, *settings.RowsLimit+1), entries[:*settings.RowsLimit]...)
		for _, entry := range entries[*settings.RowsLimit:] {
			if isOwn(entry.TeamID) {
				rows = append(rows, entry)
			}
		}
	}
	charted := make([]EventScoreTimelineEntryView, 0)
	if !settings.ChartEnabled {
		return rows, charted
	}
	onChart := make(map[uuid.UUID]bool, settings.ChartTeams+1)
	for i, entry := range entries {
		if i < int(settings.ChartTeams) || isOwn(entry.TeamID) {
			onChart[entry.TeamID] = true
		}
	}
	for _, item := range timeline {
		if onChart[item.EventTeamID] {
			charted = append(charted, item)
		}
	}
	return rows, charted
}

// ResultsAvailability tells a caller whether event-wide results are readable
// and, when they are not, why, so clients can show the matching message.
type ResultsAvailability string

const (
	ResultsAvailable        ResultsAvailability = "available"
	ResultsHidden           ResultsAvailability = "hidden"
	ResultsParticipantsOnly ResultsAvailability = "participants_only"
	ResultsNotStarted       ResultsAvailability = "not_started"
)

// decideResultsAvailability is the single results visibility policy shared by
// the result reads and the CanViewResults flags of the info endpoints.
// Managers always see results. For everyone else the permanent reasons
// (hidden, participants only) win over the temporary one (not started yet).
func decideResultsAvailability(visibility eventConfigModel.Visibility, started, manager, approved bool) ResultsAvailability {
	if manager {
		return ResultsAvailable
	}
	switch visibility {
	case eventConfigModel.VisibilityPublic:
	case eventConfigModel.VisibilityPrivate:
		if !approved {
			return ResultsParticipantsOnly
		}
	default:
		return ResultsHidden
	}
	if !started {
		return ResultsNotStarted
	}
	return ResultsAvailable
}

func (a ResultsAvailability) err() error {
	switch a {
	case ResultsAvailable:
		return nil
	case ResultsParticipantsOnly:
		return eventConfigModel.ErrResultsParticipantsOnly.Err()
	case ResultsNotStarted:
		return eventConfigModel.ErrResultsNotStarted.Err()
	default:
		return eventConfigModel.ErrResultsHidden.Err()
	}
}

func (u *EventUseCase) ListScoreboard(ctx context.Context, eventID uuid.UUID) ([]ScoreboardEntryView, error) {
	return u.rankedScoreboard(ctx, u.scoreboard, eventID, nil, nil)
}

// rankedScoreboard ranks the public table as of cutoff (nil = live). With a
// cutoff and an own team, that team's row carries its own later solves but
// keeps its frozen rank: the team sees its progress, not its new place.
func (u *EventUseCase) rankedScoreboard(ctx context.Context, scores scoreReader, eventID uuid.UUID, cutoff *time.Time, own *uuid.UUID) ([]ScoreboardEntryView, error) {
	rows, err := scores.List(ctx, eventID, scoreboardRepo.Cut{At: cutoff})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event scoreboard").Err()
	}
	out := make([]ScoreboardEntryView, 0, len(rows))
	for i, row := range rows {
		out = append(out, ScoreboardEntryView{Rank: int32(i + 1), TeamID: row.TeamID, TeamName: row.TeamName, NameIsReal: row.NameIsReal, Points: row.Points, Solved: row.Solved, LastSolveAt: row.LastSolveAt})
	}
	if cutoff == nil || own == nil {
		return out, nil
	}
	withOwn, err := scores.List(ctx, eventID, scoreboardRepo.Cut{At: cutoff, IncludeTeam: own})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list own team scores").Err()
	}
	for _, row := range withOwn {
		if row.TeamID != *own {
			continue
		}
		for i := range out {
			if out[i].TeamID == *own {
				out[i].Points, out[i].Solved, out[i].LastSolveAt = row.Points, row.Solved, row.LastSolveAt
			}
		}
	}
	return out, nil
}

// scoreTimeline lists the ranked teams' solves as of cutoff (nil = live). A
// frozen viewer's own team is read with its later solves included.
func (u *EventUseCase) scoreTimeline(ctx context.Context, scores scoreReader, eventID uuid.UUID, cutoff *time.Time, own *uuid.UUID) ([]EventScoreTimelineEntryView, error) {
	items, err := scores.EventTimeline(ctx, eventID, scoreboardRepo.Cut{At: cutoff})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event score timeline").Err()
	}
	out := make([]EventScoreTimelineEntryView, 0, len(items))
	for _, item := range items {
		if cutoff != nil && own != nil && item.EventTeamID == *own {
			continue
		}
		out = append(out, EventScoreTimelineEntryView{EventTeamID: item.EventTeamID, EventChallengeID: item.EventChallengeID, ChallengeName: item.ChallengeName, Points: item.Points, SolvedAt: item.SolvedAt})
	}
	if cutoff == nil || own == nil {
		return out, nil
	}
	withOwn, err := scores.EventTimeline(ctx, eventID, scoreboardRepo.Cut{At: cutoff, IncludeTeam: own})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list own team score timeline").Err()
	}
	for _, item := range withOwn {
		if item.EventTeamID == *own {
			out = append(out, EventScoreTimelineEntryView{EventTeamID: item.EventTeamID, EventChallengeID: item.EventChallengeID, ChallengeName: item.ChallengeName, Points: item.Points, SolvedAt: item.SolvedAt})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SolvedAt.Before(out[j].SolvedAt) })
	return out, nil
}

// ListOwnScoreTimeline returns the caller team's resolved challenge points in
// chronological order. It contains no submitted answers or expected flags.
func (u *EventUseCase) ListOwnScoreTimeline(ctx context.Context, eventID, userID uuid.UUID) ([]OwnScoreTimelineEntryView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return nil, participantModel.ErrParticipantNotApproved.Err()
	}
	entries, err := u.scoreboard.Timeline(ctx, *p.TeamID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list team score timeline").Err()
	}
	out := make([]OwnScoreTimelineEntryView, 0, len(entries))
	for _, entry := range entries {
		out = append(out, OwnScoreTimelineEntryView{EventChallengeID: entry.EventChallengeID, Points: entry.Points, SolvedAt: entry.SolvedAt})
	}
	return out, nil
}

func (u *EventUseCase) GetOwnResults(ctx context.Context, eventID, userID uuid.UUID) (OwnResultsView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		return OwnResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return OwnResultsView{}, participantModel.ErrParticipantNotApproved.Err()
	}
	// Under an applied freeze the own rank stays frozen (points stay live).
	cutoff, err := u.ownResultsCutoff(ctx, eventID, userID)
	if err != nil {
		return OwnResultsView{}, err
	}
	entries, err := u.rankedScoreboard(ctx, u.scoreboard, eventID, cutoff, p.TeamID)
	if err != nil {
		return OwnResultsView{}, err
	}
	for _, entry := range entries {
		if entry.TeamID != *p.TeamID {
			continue
		}
		timeline, timelineErr := u.ListOwnScoreTimeline(ctx, eventID, userID)
		if timelineErr != nil {
			return OwnResultsView{}, timelineErr
		}
		return OwnResultsView{Entry: entry, Timeline: timeline}, nil
	}
	// Hidden or not admitted teams are absent from the public ranking but
	// still see their own results, without a rank.
	return u.unrankedOwnResults(ctx, eventID, userID, *p.TeamID)
}

func (u *EventUseCase) unrankedOwnResults(ctx context.Context, eventID, userID, teamID uuid.UUID) (OwnResultsView, error) {
	team, err := u.teams.GetByID(ctx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return OwnResultsView{}, participantModel.ErrParticipantNotApproved.Err()
		}
		return OwnResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get own team").Err()
	}
	name, err := u.teamPublicName(ctx, eventID, team)
	if err != nil {
		return OwnResultsView{}, err
	}
	timeline, err := u.ListOwnScoreTimeline(ctx, eventID, userID)
	if err != nil {
		return OwnResultsView{}, err
	}
	entry := ScoreboardEntryView{TeamID: team.ID, TeamName: name}
	for _, item := range timeline {
		entry.Points += int64(item.Points)
		entry.Solved++
		solvedAt := item.SolvedAt
		entry.LastSolveAt = &solvedAt
	}
	return OwnResultsView{Entry: entry, Timeline: timeline}, nil
}

// teamPublicName presents an individual team by its participant's public
// name; the stored Solo-xxxx name is technical and never shown.
func (u *EventUseCase) teamPublicName(ctx context.Context, eventID uuid.UUID, team eventTeamModel.EventTeam) (string, error) {
	if !team.Individual {
		return team.Name, nil
	}
	profile, err := u.participants.Profile(ctx, eventID, team.CaptainID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get participant name").Err()
	}
	return profile.DisplayName, nil
}

func (u *EventUseCase) GetOwnTeamResults(ctx context.Context, eventID, userID uuid.UUID) (OwnTeamResultsView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil || p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return OwnTeamResultsView{}, participantModel.ErrParticipantNotApproved.Err()
	}
	legacy, err := u.GetOwnResults(ctx, eventID, userID)
	if err != nil {
		return OwnTeamResultsView{}, err
	}
	attempts, err := u.attempts.ListTeamResults(ctx, eventID, *p.TeamID)
	if err != nil {
		return OwnTeamResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team result attempts").Err()
	}
	view := OwnTeamResultsView{Entry: legacy.Entry, Timeline: legacy.Timeline, Attempts: make([]TeamResultAttemptView, 0, len(attempts))}
	for _, a := range attempts {
		view.Attempts = append(view.Attempts, TeamResultAttemptView{ID: a.ID, EventTeamID: a.EventTeamID, UserID: a.UserID, ParticipantName: a.ParticipantName, TeamChallengeID: a.TeamChallengeID, EventChallengeID: a.EventChallengeID, Answer: a.Answer, AutomaticCorrect: a.AutomaticCorrect, Correct: a.Correct, Decision: a.Decision, ReceivedAt: a.ReceivedAt})
	}
	return view, nil
}
