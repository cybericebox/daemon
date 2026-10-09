package event

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/scoreboardRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// ParticipationSolveView is one solved task of the caller's team. It carries no
// submitted answer.
type ParticipationSolveView struct {
	EventChallengeID uuid.UUID
	ChallengeName    string
	Category         string
	Points           int32
	SolvedAt         time.Time
	// SolvedByUserID is uuid.Nil when the solving attempt is unknown.
	SolvedByUserID uuid.UUID
	SolvedByName   string
	FirstBlood     bool
}

// ParticipationMemberView is a member's contribution to the team.
type ParticipationMemberView struct {
	UserID          uuid.UUID
	Name            string
	Role            int16
	JoinedAt        time.Time
	Points          int64
	Solves          int32
	FirstBloods     int32
	Attempts        int64
	CorrectAttempts int64
	Hints           int64
}

// ParticipationTeamStatsView is the caller's team side of the page. It is the
// same for every member, so it is cached per team.
type ParticipationTeamStatsView struct {
	TeamID          uuid.UUID
	TeamName        string
	Attempts        int64
	CorrectAttempts int64
	Hints           int64
	FirstBloods     int32
	Solves          []ParticipationSolveView
	Members         []ParticipationMemberView
}

// ParticipationStatsView is «Моя участь»: the place in the results, the team
// and the caller's own share of it.
type ParticipationStatsView struct {
	// Rank is 0 when the results are not shown to the caller (hidden, private
	// or not started) or the team is not ranked.
	Rank      int32
	Points    int64
	Solved    int64
	Team      ParticipationTeamStatsView
	Me        ParticipationMemberView
	Timeline  []OwnScoreTimelineEntryView
	Freeze    bool
	Generated time.Time
}

// GetParticipationStats reads only the caller's own team: who is on it, what it
// solved and how the members contributed. Other teams appear only as a fact
// about a solve (first blood), never as data; under an applied freeze a first
// blood after the cut-off is not disclosed and the rank stays frozen.
func (u *EventUseCase) GetParticipationStats(ctx context.Context, eventID, userID uuid.UUID) (ParticipationStatsView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		return ParticipationStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return ParticipationStatsView{}, participantModel.ErrParticipantNotApproved.Err()
	}
	teamID := *p.TeamID
	own, err := u.GetOwnResults(ctx, eventID, userID)
	if err != nil {
		return ParticipationStatsView{}, err
	}
	shown, cutoff, err := u.boardResultsCutoff(ctx, eventID, userID)
	if err != nil {
		return ParticipationStatsView{}, err
	}
	shared, err := u.sharedResults(ctx, eventID)
	if err != nil {
		return ParticipationStatsView{}, err
	}
	key := "participation:" + eventID.String() + ":" + teamID.String() + ":" + revisionKey(shared.revision.Revision, cutoff)
	team, err := cachedBoard(ctx, u.resultsCache, key, func(ctx context.Context) (ParticipationTeamStatsView, error) {
		return u.loadParticipationTeam(ctx, eventID, teamID, own.Entry.TeamName, cutoff)
	})
	if err != nil {
		return ParticipationStatsView{}, err
	}
	view := ParticipationStatsView{Points: own.Entry.Points, Solved: own.Entry.Solved, Team: team, Timeline: own.Timeline, Freeze: cutoff != nil, Generated: time.Now().UTC()}
	if shown {
		view.Rank = own.Entry.Rank
	}
	for _, member := range team.Members {
		if member.UserID == userID {
			view.Me = member
		}
	}
	if view.Me.UserID == uuid.Nil {
		view.Me = ParticipationMemberView{UserID: userID}
	}
	return view, nil
}

// GetModeratorsParticipationStats is the «Моя участь» of the organizers' preview:
// the real results of the hidden moderators team (roster, solves, counters,
// timeline) and the caller's own share as a member of it. Any event manager
// may read it; there is no rank because the team is not in the results.
func (u *EventUseCase) GetModeratorsParticipationStats(ctx context.Context, eventID, userID uuid.UUID) (ParticipationStatsView, error) {
	if _, err := u.managers.Get(ctx, eventID, userID); err != nil {
		if errors.Is(err, eventManagerModel.ErrEventManagerNotFound.Err()) || repositoryTools.IsObjectNotFoundError(err) {
			return ParticipationStatsView{}, eventManagerModel.ErrEventManagementForbidden.Err()
		}
		return ParticipationStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event manager").Err()
	}
	teamID, err := u.moderatorsBoardTeam(ctx, eventID)
	if err != nil {
		return ParticipationStatsView{}, err
	}
	solves, err := u.scoreboard.ParticipationSolves(ctx, eventID, teamID)
	if err != nil {
		return ParticipationStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team solves").Err()
	}
	members, err := u.scoreboard.ModeratorsParticipationMembers(ctx, eventID, teamID)
	if err != nil {
		return ParticipationStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team members").Err()
	}
	totals, err := u.scoreboard.ParticipationTotals(ctx, eventID, teamID)
	if err != nil {
		return ParticipationStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read team totals").Err()
	}
	entries, err := u.scoreboard.Timeline(ctx, teamID)
	if err != nil {
		return ParticipationStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team score timeline").Err()
	}
	view := ParticipationStatsView{Team: buildParticipationTeam(teamID, "", solves, members, totals, nil), Generated: time.Now().UTC(),
		Timeline: make([]OwnScoreTimelineEntryView, 0, len(entries))}
	for _, entry := range entries {
		view.Timeline = append(view.Timeline, OwnScoreTimelineEntryView{EventChallengeID: entry.EventChallengeID, Points: entry.Points, SolvedAt: entry.SolvedAt})
		view.Points += int64(entry.Points)
	}
	view.Solved = int64(len(view.Team.Solves))
	for _, member := range view.Team.Members {
		if member.UserID == userID {
			view.Me = member
		}
	}
	return view, nil
}

func revisionKey(revision int64, cutoff *time.Time) string {
	if cutoff == nil {
		return "live:" + strconv.FormatInt(revision, 10)
	}
	return cutoff.UTC().Format(time.RFC3339Nano) + ":" + strconv.FormatInt(revision, 10)
}

func (u *EventUseCase) loadParticipationTeam(ctx context.Context, eventID, teamID uuid.UUID, name string, cutoff *time.Time) (ParticipationTeamStatsView, error) {
	solves, err := u.scoreboard.ParticipationSolves(ctx, eventID, teamID)
	if err != nil {
		return ParticipationTeamStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team solves").Err()
	}
	members, err := u.scoreboard.ParticipationMembers(ctx, eventID, teamID)
	if err != nil {
		return ParticipationTeamStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team members").Err()
	}
	totals, err := u.scoreboard.ParticipationTotals(ctx, eventID, teamID)
	if err != nil {
		return ParticipationTeamStatsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read team totals").Err()
	}
	return buildParticipationTeam(teamID, name, solves, members, totals, cutoff), nil
}

// buildParticipationTeam joins the three reads: the solves are attributed to
// members (points, solves, first bloods) and the totals stay team-wide.
func buildParticipationTeam(teamID uuid.UUID, name string, solves []scoreboardRepo.ParticipationSolve, members []scoreboardRepo.ParticipationMember, totals scoreboardRepo.ParticipationTotals, cutoff *time.Time) ParticipationTeamStatsView {
	view := ParticipationTeamStatsView{TeamID: teamID, TeamName: name, Attempts: totals.Attempts, CorrectAttempts: totals.CorrectAttempts, Hints: totals.Hints,
		Solves: make([]ParticipationSolveView, 0, len(solves)), Members: make([]ParticipationMemberView, 0, len(members))}
	index := make(map[uuid.UUID]int, len(members))
	for _, m := range members {
		index[m.UserID] = len(view.Members)
		view.Members = append(view.Members, ParticipationMemberView{UserID: m.UserID, Name: m.Name, Role: m.Role, JoinedAt: m.JoinedAt, Attempts: m.Attempts, CorrectAttempts: m.CorrectAttempts, Hints: m.Hints})
	}
	for _, s := range solves {
		firstBlood := s.FirstBlood && (cutoff == nil || s.SolvedAt.Before(*cutoff))
		view.Solves = append(view.Solves, ParticipationSolveView{EventChallengeID: s.EventChallengeID, ChallengeName: s.ChallengeName, Category: s.Category, Points: s.Points,
			SolvedAt: s.SolvedAt, SolvedByUserID: s.SolvedBy, SolvedByName: s.SolvedByName, FirstBlood: firstBlood})
		if firstBlood {
			view.FirstBloods++
		}
		if i, ok := index[s.SolvedBy]; ok {
			view.Members[i].Points += int64(s.Points)
			view.Members[i].Solves++
			if firstBlood {
				view.Members[i].FirstBloods++
			}
		}
	}
	return view
}
