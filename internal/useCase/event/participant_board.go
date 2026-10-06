package event

import (
	"context"
	"encoding/json"
	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	"io"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// ListOwnChallenges returns the caller team's board: published assignments
// with prerequisites (a locked challenge shows only its name and difficulty),
// file sizes, and solve counts when event results are available to the caller
// (other teams' solves only before an applied freeze).
func (u *EventUseCase) ListOwnChallenges(ctx context.Context, eventID, userID uuid.UUID) ([]OwnChallengeView, error) {
	teamID, views, err := u.ownBoard(ctx, eventID, userID, true)
	if err != nil {
		return nil, err
	}
	if err = u.fillFileSizes(ctx, views); err != nil {
		return nil, err
	}
	if err = u.fillAttemptsLeft(ctx, teamID, views); err != nil {
		return nil, err
	}
	if err = u.fillSolveAwards(ctx, teamID, views); err != nil {
		return nil, err
	}
	accessible, cutoff, err := u.boardResultsCutoff(ctx, eventID, userID)
	if err != nil || !accessible {
		return views, err
	}
	counts, err := u.teamChallenges.SolveCounts(ctx, eventID, teamID, cutoff)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to count challenge solves").Err()
	}
	for i := range views {
		count := counts[views[i].EventChallengeID]
		views[i].SolveCount = &count
	}
	return views, nil
}

// ListOwnBoard is the participant board with its stage context: the stages that have opened, the one that is open
// now with its countdown, the break countdown and the next boundary to refetch at.
func (u *EventUseCase) ListOwnBoard(ctx context.Context, eventID, userID uuid.UUID) (OwnBoardView, error) {
	now := time.Now()
	challenges, err := u.ListOwnChallenges(ctx, eventID, userID)
	if err != nil {
		return OwnBoardView{}, err
	}
	stages, err := u.stages.List(ctx, eventID)
	if err != nil {
		return OwnBoardView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return OwnBoardView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	return buildOwnBoard(challenges, stages, config.Countdown, now), nil
}

// buildOwnBoard derives the stage context of the board at a moment (see OwnBoardView).
func buildOwnBoard(challenges []OwnChallengeView, stages []eventModel.Stage, countdown eventConfigModel.CountdownSettings, now time.Time) OwnBoardView {
	view := OwnBoardView{Challenges: challenges, ServerNow: now, Stages: make([]BoardStageView, 0, len(stages))}
	opened := 0
	for i, stage := range stages {
		if boundary := stage.NextChangeAt(now); boundary != nil && (view.NextChangeAt == nil || boundary.Before(*view.NextChangeAt)) {
			view.NextChangeAt = boundary
		}
		if !stage.Opened(now) {
			continue
		}
		opened++
		view.Stages = append(view.Stages, BoardStageView{ID: stage.ID, Name: stage.Name, OpensAt: stage.OpensAt, ClosesAt: stage.ClosesAt, Returnable: stage.Returnable, State: stage.State(now)})
		if stage.State(now) != eventModel.StageOpen {
			continue
		}
		last := i == len(stages)-1
		current := &CurrentStageView{ID: stage.ID, Name: stage.Name, OpensAt: stage.OpensAt, Last: last}
		if !last && countdown.ShowFinish && stageCountdownVisible(countdown, stage, now) {
			ends := stage.ClosesAt
			current.EndsAt = &ends
		}
		view.CurrentStage = current
	}
	if view.CurrentStage == nil && opened > 0 {
		for _, stage := range stages {
			if stage.OpensAt.After(now) {
				next := stage.OpensAt
				view.NextOpensAt = &next
				break
			}
		}
	}
	return view
}

// stageCountdownVisible resolves the finish countdown mode for the end of a stage: before_end shows it during the
// last FinishMinutes, from_start from the stage's beginning.
func stageCountdownVisible(countdown eventConfigModel.CountdownSettings, stage eventModel.Stage, now time.Time) bool {
	if countdown.Mode() == eventConfigModel.FinishFromStart {
		return true
	}
	return !now.Before(stage.ClosesAt.Add(-time.Duration(countdown.FinishMinutes) * time.Minute))
}

// fillAttemptsLeft sets the flag attempts the team has left on each limited, unsolved task of its board.
func (u *EventUseCase) fillAttemptsLeft(ctx context.Context, teamID uuid.UUID, views []OwnChallengeView) error {
	limits, err := u.attempts.AttemptLimits(ctx, teamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get flag attempt limits").Err()
	}
	for i := range views {
		if state, limited := limits[views[i].ID]; limited && !state.Solved {
			views[i].MaxAttempts, views[i].AttemptsLeft = state.Max, challengeAttempt.AttemptsLeft(state.Max, state.Wrong)
		}
	}
	return nil
}

// fillSolveAwards sets what the team holds for each solved task of its board and who of its members solved it.
// Only the caller's own team is read, so no other team's member is ever named.
func (u *EventUseCase) fillSolveAwards(ctx context.Context, teamID uuid.UUID, views []OwnChallengeView) error {
	awards, err := u.teamChallenges.SolveAwards(ctx, teamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get challenge awards").Err()
	}
	for i := range views {
		if award, solved := awards[views[i].ID]; solved {
			points, penalty := award.AwardedPoints, award.HintPenalty
			views[i].AwardedPoints, views[i].HintPenalty = &points, &penalty
			views[i].SolvedBy = &SolverView{UserID: award.SolverID, Name: award.SolverName}
		}
	}
	return nil
}

// ListChallengeSolves lists one page of who solved one challenge of the
// caller's board, oldest first (first blood on top). It needs the board gates
// plus event results access; team names are the public scoreboard names. The
// list holds only visible teams plus the caller's own team.
func (u *EventUseCase) ListChallengeSolves(ctx context.Context, eventID, userID, challengeID uuid.UUID, cursor uuid.UUID, pageSize int) (ChallengeSolvesPage, error) {
	teamID, views, err := u.ownBoard(ctx, eventID, userID, false)
	if err != nil {
		return ChallengeSolvesPage{}, err
	}
	now := time.Now()
	policy, err := u.loadResultsPolicy(ctx, eventID, ResultsAccess{UserID: &userID})
	if err != nil {
		return ChallengeSolvesPage{}, err
	}
	if err = policy.availability(now).err(); err != nil {
		return ChallengeSolvesPage{}, err
	}
	if _, found := boardChallenge(views, challengeID); !found {
		return ChallengeSolvesPage{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	return u.challengeSolvesPage(ctx, eventID, teamID, challengeID, policy.freeze(now, false).cutoff(), cursor, pageSize, !policy.mayShowRealNames())
}

// ListModeratorsChallengeSolves is the solvers list on the moderators board:
// live (no freeze), the visible teams plus the moderators team's own solves.
func (u *EventUseCase) ListModeratorsChallengeSolves(ctx context.Context, eventID, challengeID uuid.UUID, cursor uuid.UUID, pageSize int) (ChallengeSolvesPage, error) {
	teamID, err := u.moderatorsBoardTeam(ctx, eventID)
	if err != nil {
		return ChallengeSolvesPage{}, err
	}
	views, err := u.moderatorsBoard(ctx, teamID)
	if err != nil {
		return ChallengeSolvesPage{}, err
	}
	if _, found := boardChallenge(views, challengeID); !found {
		return ChallengeSolvesPage{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	return u.challengeSolvesPage(ctx, eventID, teamID, challengeID, nil, cursor, pageSize, false)
}

func (u *EventUseCase) challengeSolvesPage(ctx context.Context, eventID, ownTeamID, challengeID uuid.UUID, cutoff *time.Time, cursor uuid.UUID, pageSize int, maskRealNames bool) (ChallengeSolvesPage, error) {
	rows, err := u.teamChallenges.Solves(ctx, eventID, challengeID, ownTeamID, cutoff, cursor, int32(pageSize+1))
	if err != nil {
		return ChallengeSolvesPage{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list challenge solves").Err()
	}
	page := ChallengeSolvesPage{}
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		page.HasMore = true
		page.Next = rows[len(rows)-1].TeamChallengeID
	}
	counts, err := u.teamChallenges.SolveCounts(ctx, eventID, ownTeamID, cutoff)
	if err != nil {
		return ChallengeSolvesPage{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count challenge solves").Err()
	}
	page.Total = counts[challengeID]
	page.Items = make([]ChallengeSolveView, 0, len(rows))
	for _, row := range rows {
		// A first blood after the freeze cut-off is never disclosed.
		firstBlood := row.FirstBlood && (cutoff == nil || row.SolvedAt.Before(*cutoff))
		item := ChallengeSolveView{TeamName: row.TeamName, SolvedAt: row.SolvedAt, Own: row.TeamID == ownTeamID, FirstBlood: firstBlood}
		if maskRealNames && row.NameIsReal && !item.Own {
			item.TeamName, item.NameHidden = "", true
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

// StreamOwnChallengeAttachment checks the participant's published challenge
// snapshot before opening a file. A catalog file ID alone never grants access,
// and a locked challenge exposes no files.
// A served download is logged for analytics.
func (u *EventUseCase) StreamOwnChallengeAttachment(ctx context.Context, eventID, userID, challengeID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	teamID, views, err := u.ownBoard(ctx, eventID, userID, false)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	stream, file, err := u.streamBoardFile(ctx, views, challengeID, fileID)
	if err == nil {
		u.recordActivity(ctx, eventActivityModel.AttachmentDownloaded(eventID, userID, teamID, challengeID, fileID, time.Now()))
	}
	return stream, file, err
}

// ListOwnTeamMembers is the caller's team roster: captain first, then by
// public name.
func (u *EventUseCase) ListOwnTeamMembers(ctx context.Context, eventID, userID uuid.UUID) ([]TeamRosterMemberView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, participantModel.ErrParticipantNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.TeamID == nil {
		return nil, eventTeamModel.ErrEventTeamNotFound.Err()
	}
	members, err := u.participants.TeamRoster(ctx, eventID, *p.TeamID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list team members").Err()
	}
	out := make([]TeamRosterMemberView, 0, len(members))
	for _, member := range members {
		out = append(out, TeamRosterMemberView{UserID: member.UserID, DisplayName: member.DisplayName, Role: member.Role, Own: member.UserID == userID})
	}
	pending, err := u.participants.TeamPendingInvitees(ctx, eventID, *p.TeamID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list pending team invitees").Err()
	}
	for _, invitee := range pending {
		out = append(out, TeamRosterMemberView{UserID: invitee.UserID, DisplayName: invitee.DisplayName, Role: invitee.Role, Own: invitee.UserID == userID, Pending: true})
	}
	return out, nil
}

// ListModeratorsBoard shows every Ready or Published assignment of the
// moderators team, regardless of board publication and event start. Nothing
// is locked: moderators see full snapshots and files.
func (u *EventUseCase) ListModeratorsBoard(ctx context.Context, eventID uuid.UUID) ([]OwnChallengeView, error) {
	teamID, err := u.moderatorsBoardTeam(ctx, eventID)
	if err != nil {
		return nil, err
	}
	views, err := u.moderatorsBoard(ctx, teamID)
	if err != nil {
		return nil, err
	}
	if err = u.fillFileSizes(ctx, views); err != nil {
		return nil, err
	}
	if err = u.fillSolveAwards(ctx, teamID, views); err != nil {
		return nil, err
	}
	return views, nil
}

// StreamModeratorsChallengeAttachment opens a file of a moderators board
// challenge.
func (u *EventUseCase) StreamModeratorsChallengeAttachment(ctx context.Context, eventID, challengeID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	teamID, err := u.moderatorsBoardTeam(ctx, eventID)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	views, err := u.moderatorsBoard(ctx, teamID)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	return u.streamBoardFile(ctx, views, challengeID, fileID)
}

// ownBoard applies the participant board gates (approved, admitted team,
// board visible) and returns the caller's team with its board views.
// withHints loads the team's hint unlocks (the board list only).
func (u *EventUseCase) ownBoard(ctx context.Context, eventID, userID uuid.UUID, withHints bool) (uuid.UUID, []OwnChallengeView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		return uuid.Nil, nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return uuid.Nil, nil, participantModel.ErrParticipantNotApproved.Err()
	}
	if err = requireTeamAdmitted(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return uuid.Nil, nil, err
	}
	if err = u.requireChallengeBoardVisible(ctx, eventID); err != nil {
		return uuid.Nil, nil, err
	}
	rows, err := u.teamChallenges.ListPublished(ctx, *p.TeamID, time.Now())
	if err != nil {
		return uuid.Nil, nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list team challenges").Err()
	}
	prerequisites, err := u.teamChallenges.Prerequisites(ctx, *p.TeamID)
	if err != nil {
		return uuid.Nil, nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list challenge prerequisites").Err()
	}
	var unlocks map[hintUnlockKey]teamChallengeRepo.HintUnlock
	if withHints {
		if unlocks, err = u.teamUnlocks(ctx, *p.TeamID); err != nil {
			return uuid.Nil, nil, err
		}
		// Hints show only where the task enables them and the event does not
		// disable them for every task.
		config, configErr := u.configs.Get(ctx, eventID)
		if configErr != nil {
			return uuid.Nil, nil, model.ErrPlatform.WithError(configErr).WithMessage("Failed to get event config").Err()
		}
		if config.HintsDisabled {
			for i := range rows {
				rows[i].HintsEnabled = false
			}
		}
	}
	onBoard := func(r teamChallengeModel.Readiness) bool { return r == teamChallengeModel.ReadinessPublished }
	return *p.TeamID, boardViews(rows, prerequisites, unlocks, onBoard, true), nil
}

func (u *EventUseCase) moderatorsBoard(ctx context.Context, teamID uuid.UUID) ([]OwnChallengeView, error) {
	rows, err := u.teamChallenges.ListBoard(ctx, teamID, time.Now())
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list moderators team challenges").Err()
	}
	prerequisites, err := u.teamChallenges.Prerequisites(ctx, teamID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list challenge prerequisites").Err()
	}
	return boardViews(rows, prerequisites, nil, onModeratorsBoard, false), nil
}

// onModeratorsBoard: moderators see prepared assignments (not preparing or
// failed ones) before and regardless of their publication.
func onModeratorsBoard(r teamChallengeModel.Readiness) bool {
	return r == teamChallengeModel.ReadinessReady || r == teamChallengeModel.ReadinessPublished
}

// boardViews projects board rows. With lockByPrerequisites a challenge with
// an unsolved prerequisite is locked: its snapshot shrinks to name and
// difficulty and it lists no files. Corrupt snapshots are forgiven on read.
// Hints: a participant board (lockByPrerequisites) shows texts only once the
// team unlocked them; the moderators board reveals every text.
func boardViews(rows []teamChallengeRepo.PublishedChallenge, prerequisites map[uuid.UUID][]teamChallengeRepo.Prerequisite, unlocks map[hintUnlockKey]teamChallengeRepo.HintUnlock, onBoard func(teamChallengeModel.Readiness) bool, lockByPrerequisites bool) []OwnChallengeView {
	out := make([]OwnChallengeView, 0, len(rows))
	for _, row := range rows {
		challenge := row.Challenge
		if !onBoard(challenge.Readiness) {
			continue
		}
		view := OwnChallengeView{
			ID: challenge.ID, EventChallengeID: challenge.EventChallengeID, Snapshot: challenge.Snapshot, Readiness: challenge.Readiness,
			SolvedAt: challenge.SolvedAt, Points: row.Points, Order: row.Order, GroupID: row.GroupID, GroupName: row.GroupName, GroupOrder: row.GroupOrder,
			ContentUpdatedAt: row.ContentUpdatedAt, Infrastructure: row.Infrastructure, HintsEnabled: row.HintsEnabled, BoardPublished: row.Published,
			StageID: row.StageID, Closed: row.StagePhase == eventModel.StagePhaseClosed, Practice: row.PracticeSolved,
			Prerequisites: make([]ChallengePrerequisiteView, 0), Files: make([]ChallengeFileView, 0),
		}
		solved := make([]bool, 0, len(prerequisites[challenge.EventChallengeID]))
		for _, prerequisite := range prerequisites[challenge.EventChallengeID] {
			view.Prerequisites = append(view.Prerequisites, ChallengePrerequisiteView{EventChallengeID: prerequisite.EventChallengeID, Name: prerequisite.Name, Solved: prerequisite.Solved})
			solved = append(solved, prerequisite.Solved)
		}
		view.Locked = lockByPrerequisites && teamChallengeModel.PrerequisitesLock(solved)
		view.Hints, view.HintCostTotal = boardHints(row, unlocks, !lockByPrerequisites, view.Locked)
		if view.Locked {
			locked, err := teamChallengeModel.LockedSnapshot(challenge.Snapshot)
			if err != nil {
				locked = json.RawMessage(`{}`)
			}
			view.Snapshot = locked
		} else if attachments, err := teamChallengeModel.SnapshotAttachments(challenge.Snapshot); err == nil {
			for _, attachment := range attachments {
				view.Files = append(view.Files, ChallengeFileView{FileID: attachment.FileID, Name: attachment.Name})
			}
		}
		out = append(out, view)
	}
	return out
}

// fillFileSizes resolves every listed attachment size in one query; a
// missing media row leaves the size at 0.
func (u *EventUseCase) fillFileSizes(ctx context.Context, views []OwnChallengeView) error {
	ids := make([]uuid.UUID, 0)
	for _, view := range views {
		for _, file := range view.Files {
			ids = append(ids, file.FileID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sizes, err := u.teamChallenges.FileSizes(ctx, ids)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get attachment sizes").Err()
	}
	for i := range views {
		for j := range views[i].Files {
			views[i].Files[j].Size = sizes[views[i].Files[j].FileID]
		}
	}
	return nil
}

func boardChallenge(views []OwnChallengeView, challengeID uuid.UUID) (OwnChallengeView, bool) {
	for _, view := range views {
		if view.EventChallengeID == challengeID {
			return view, true
		}
	}
	return OwnChallengeView{}, false
}

func (u *EventUseCase) streamBoardFile(ctx context.Context, views []OwnChallengeView, challengeID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	view, found := boardChallenge(views, challengeID)
	if !found {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	for _, file := range view.Files {
		if file.FileID != fileID {
			continue
		}
		if u.brandMedia == nil {
			return nil, mediaModel.File{}, mediaModel.ErrMediaStorageNotConfigured.Err()
		}
		return u.brandMedia.StreamFile(ctx, fileID)
	}
	return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
}

// moderatorsBoardTeam resolves the moderators team for the board routes. It
// does not require infrastructure: the team also exists on events without
// it (created on demand here and by the stand engine).
func (u *EventUseCase) moderatorsBoardTeam(ctx context.Context, eventID uuid.UUID) (uuid.UUID, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return uuid.Nil, eventModel.ErrEventNotFound.Err()
		}
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return u.resolveModeratorsTeam(ctx, e)
}

// resolveModeratorsTeam returns the hidden moderators team, creating it on
// demand (without a laboratory access sync unless infrastructure is allowed).
func (u *EventUseCase) resolveModeratorsTeam(ctx context.Context, e eventModel.Event) (uuid.UUID, error) {
	if err := u.ensureModeratorsTeam(ctx, e.ID, time.Now(), e.InfrastructureAllowed); err != nil {
		return uuid.Nil, err
	}
	team, err := u.stands.GetModeratorsTeam(ctx, e.ID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return uuid.Nil, eventStandModel.ErrStandModeratorsTeamUnavailable.Err()
		}
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get moderators team").Err()
	}
	return team.ID, nil
}
