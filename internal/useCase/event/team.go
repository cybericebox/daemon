package event

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnswerFileRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// JoinTeam joins an approved, currently unassigned participant to a team by
// its regenerable code. Capacity reservation and membership assignment share
// one unit of work, so a failed assignment cannot leave a phantom member.
func (u *EventUseCase) JoinTeam(ctx context.Context, eventID, userID uuid.UUID, joinCode string) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()

	configs := eventConfigRepo.New(txRepo)
	teams := eventTeamRepo.New(txRepo)
	participants := participantRepo.New(txRepo)
	config, err := configs.Get(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationTeam {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	rosterEvent, err := openRosterEvent(txCtx, txRepo, eventID, time.Now())
	if err != nil {
		return err
	}
	team, err := teams.GetByJoinCode(txCtx, eventID, joinCode)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	if err = team.CheckJoinCodeActive(time.Now()); err != nil {
		return err
	}
	if team.Formed(rosterEvent.Lifecycle.FormsTeamsAtStart(time.Now())) {
		return eventTeamModel.ErrEventTeamFormed.Err()
	}
	participant, err := participants.Get(txCtx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, userID, eventFormModel.CapabilityTeamJoinOrCreate); err != nil {
		return err
	}
	if err = participant.AssignTeam(team.ID, participantModel.TeamRoleMember); err != nil {
		return err
	}
	now := time.Now()
	if affected, err := teams.TryAddMember(txCtx, eventID, team.ID, config.MaxTeamSize, now); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reserve team membership").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamFull.Err()
	}
	if affected, err := participants.AssignTeam(txCtx, participant); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to join event team").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, team.ID, now); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to join event team").Err()
	}
	return nil
}

// AssignParticipantToTeam is the moderator counterpart to JoinTeam. Access is
// enforced by the management handler; this method preserves the same capacity
// and participant-state invariants without requiring a participant join code.
func (u *EventUseCase) AssignParticipantToTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationTeam {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	teams, participants := eventTeamRepo.New(txRepo), participantRepo.New(txRepo)
	if _, err = teams.GetByID(txCtx, eventID, teamID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	participant, err := participants.Get(txCtx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if err = participant.AssignTeam(teamID, participantModel.TeamRoleMember); err != nil {
		return err
	}
	if affected, err := teams.TryAddMember(txCtx, eventID, teamID, config.MaxTeamSize, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reserve team membership").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamFull.Err()
	}
	if affected, err := participants.AssignTeam(txCtx, participant); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to assign event team").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, teamID, time.Now()); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to assign event team").Err()
	}
	return nil
}

// CreateManagedTeam creates a team on behalf of an event manager. It retains
// the participation, capacity and approved-captain rules, but intentionally
// does not apply self-service roster/form gates: management is needed to fix
// registrations and rosters after those windows close.
func (u *EventUseCase) CreateManagedTeam(ctx context.Context, eventID uuid.UUID, in CreateManagedTeamInput) (TeamView, error) {
	if u.uow == nil {
		return TeamView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return TeamView{}, err
	}
	defer unit.Restore()
	if _, err = txRepo.LockEventForTeamChange(txCtx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return TeamView{}, eventModel.ErrEventNotFound.Err()
		}
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to lock event for team creation").Err()
	}
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationTeam {
		return TeamView{}, eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	if config.MaxTeams != nil {
		count, countErr := txRepo.CountEventTeams(txCtx, eventID)
		if countErr != nil {
			return TeamView{}, model.ErrPlatform.WithError(countErr).WithMessage("Failed to count event teams").Err()
		}
		if count >= int64(*config.MaxTeams) {
			return TeamView{}, eventTeamModel.ErrEventTeamFull.Err()
		}
	}
	participants := participantRepo.New(txRepo)
	captain, err := participants.Get(txCtx, eventID, in.CaptainID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return TeamView{}, participantModel.ErrParticipantNotFound.Err()
		}
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team captain").Err()
	}
	if captain.Status != participantModel.StatusApproved {
		return TeamView{}, participantModel.ErrParticipantNotApproved.Err()
	}
	if captain.TeamID != nil {
		return TeamView{}, participantModel.ErrParticipantTeamInvalid.Err()
	}
	fields, err := validateTeamFieldAnswers(txCtx, txRepo, eventID, uuid.Nil, uuid.Nil, in.Fields, false)
	if err != nil {
		return TeamView{}, err
	}
	now := time.Now()
	team, err := eventTeamModel.New(eventID, in.CaptainID, in.Name, uuid.Must(uuid.NewV4()).String(), now)
	if err != nil {
		return TeamView{}, err
	}
	teams := eventTeamRepo.New(txRepo)
	created, err := teams.Create(txCtx, team)
	if err != nil {
		if creator, ok := repositoryTools.UniqueViolationError(err, eventTeamModel.ErrEventTeamExists); ok {
			return TeamView{}, creator.Err()
		}
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event team").Err()
	}
	if len(fields.values) > 0 {
		if err = saveTeamFields(txCtx, txRepo, eventID, created.ID, fields); err != nil {
			return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save team fields").Err()
		}
		if err = fields.attach(txCtx, eventAnswerFileRepo.New(txRepo), eventID, eventFormModel.AnswerScopeTeam, created.ID); err != nil {
			return TeamView{}, err
		}
	}
	if err = captain.AssignTeam(created.ID, participantModel.TeamRoleCaptain); err != nil {
		return TeamView{}, err
	}
	if affected, assignErr := participants.AssignTeam(txCtx, captain); assignErr != nil {
		return TeamView{}, model.ErrPlatform.WithError(assignErr).WithMessage("Failed to assign team captain").Err()
	} else if affected == 0 {
		return TeamView{}, participantModel.ErrParticipantTeamInvalid.Err()
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, created.ID, now); err != nil {
		return TeamView{}, err
	}
	view, err := managedTeamView(txCtx, txRepo, eventID, created)
	if err != nil {
		return TeamView{}, err
	}
	if err = unit.Save(); err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event team").Err()
	}
	return view, nil
}

// UpdateManagedTeam changes a team's display state after event-level access
// is verified by the handler. It uses the aggregate's moderator mutation,
// retaining name validation and optimistic-lock protection.
func (u *EventUseCase) UpdateManagedTeam(ctx context.Context, eventID, teamID uuid.UUID, in UpdateManagedTeamInput) (TeamView, error) {
	if u.uow == nil {
		return TeamView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return TeamView{}, err
	}
	defer unit.Restore()
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return TeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	expected := team.UpdatedAt
	hiddenChanged := team.Hidden != in.Hidden
	if err = team.UpdateByModerator(in.Name, in.Hidden, time.Now()); err != nil {
		return TeamView{}, err
	}
	if affected, updateErr := teams.Update(txCtx, team, expected); updateErr != nil {
		if creator, ok := repositoryTools.UniqueViolationError(updateErr, eventTeamModel.ErrEventTeamExists); ok {
			return TeamView{}, creator.Err()
		}
		return TeamView{}, model.ErrPlatform.WithError(updateErr).WithMessage("Failed to update event team").Err()
	} else if affected == 0 {
		return TeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
	}
	// Moderators may change any team field at any time; answers are still
	// validated against the configured team fields.
	if in.Fields != nil {
		fields, fieldErr := validateTeamFieldAnswers(txCtx, txRepo, eventID, uuid.Nil, teamID, in.Fields, false)
		if fieldErr != nil {
			return TeamView{}, fieldErr
		}
		// Staff-only values are edited through their own endpoint (audited):
		// this form never rewrites them.
		if fields.values, err = keepTeamStaffValues(txCtx, teams, eventID, teamID, fields.values); err != nil {
			return TeamView{}, err
		}
		if err = saveTeamFields(txCtx, txRepo, eventID, teamID, fields); err != nil {
			return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save team fields").Err()
		}
		if err = fields.attach(txCtx, eventAnswerFileRepo.New(txRepo), eventID, eventFormModel.AnswerScopeTeam, teamID); err != nil {
			return TeamView{}, err
		}
	}
	view, err := managedTeamView(txCtx, txRepo, eventID, team)
	if err != nil {
		return TeamView{}, err
	}
	if hiddenChanged {
		if err = recordScoreboardRecalculation(txCtx, txRepo, eventID); err != nil {
			return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event team").Err()
	}
	return view, nil
}

// SetTeamHidden hides a team from the results or shows it again. Visibility is
// applied when results are read, so nothing is recomputed here: the result
// revision advances and every reader (scoreboard, dynamic points, first blood,
// counters, solvers list, live) refreshes. The moderators team is always
// hidden and cannot be toggled.
func (u *EventUseCase) SetTeamHidden(ctx context.Context, eventID, teamID uuid.UUID, hidden bool) (TeamView, error) {
	if u.uow == nil {
		return TeamView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return TeamView{}, err
	}
	defer unit.Restore()
	if moderators, mErr := u.stands.GetModeratorsTeam(txCtx, eventID); mErr == nil && moderators.ID == teamID {
		return TeamView{}, eventTeamModel.ErrEventTeamModeratorsLocked.Err()
	}
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return TeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	changed, err := teams.SetHidden(txCtx, eventID, teamID, hidden, time.Now())
	if err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update team visibility").Err()
	}
	if changed {
		team.Hidden = hidden
		if err = recordScoreboardRecalculation(txCtx, txRepo, eventID); err != nil {
			return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
		}
	}
	view, err := managedTeamView(txCtx, txRepo, eventID, team)
	if err != nil {
		return TeamView{}, err
	}
	if err = unit.Save(); err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update team visibility").Err()
	}
	return view, nil
}

// RemoveParticipantFromTeam is the moderation counterpart to leave/kick. A
// captain cannot be removed silently: transfer the captaincy or disband the
// team first, which keeps event_teams.captain_id and participant roles aligned.
func (u *EventUseCase) RemoveParticipantFromTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	teams, participants := eventTeamRepo.New(txRepo), participantRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	participant, err := participants.Get(txCtx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if participant.TeamID == nil || *participant.TeamID != teamID {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if team.IsCaptain(userID) {
		return eventTeamModel.ErrEventTeamCaptainMustTransfer.Err()
	}
	// Moderators and administrators obey the same rule as everyone: taking a
	// member out of a formed team removes them from the event, never back to a
	// teamless state where they could join another team.
	formed, err := teamFormedNow(txCtx, txRepo, eventID, team, time.Now())
	if err != nil {
		return err
	}
	if affected, removeErr := teams.TryRemoveMember(txCtx, eventID, teamID, time.Now()); removeErr != nil {
		return model.ErrPlatform.WithError(removeErr).WithMessage("Failed to remove team member").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if affected, clearErr := participants.ClearTeam(txCtx, eventID, userID, teamID); clearErr != nil {
		return model.ErrPlatform.WithError(clearErr).WithMessage("Failed to clear participant team").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if formed {
		if err = removeFromEvent(txCtx, participants, participant, time.Now()); err != nil {
			return err
		}
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, teamID, time.Now()); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove team member").Err()
	}
	u.dropMemberLabClient(ctx, eventID, teamID, userID)
	return nil
}

// DeleteManagedTeam disbands a team at the moderator's request. The
// database's foreign key detaches every member atomically. Individual events
// retain their universal single-member teams and cannot use this operation.
func (u *EventUseCase) DeleteManagedTeam(ctx context.Context, eventID, teamID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation != nil && *config.Participation == eventConfigModel.ParticipationIndividual {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	// Disbanding detaches every member, which would free them to join another
	// team, so it follows the member lock (an empty team may still go).
	team, err := eventTeamRepo.New(txRepo).GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	if team.MemberCount > 0 {
		if formed, formedErr := teamFormedNow(txCtx, txRepo, eventID, team, time.Now()); formedErr != nil {
			return formedErr
		} else if formed {
			return eventTeamModel.ErrEventTeamSwitchLocked.Err()
		}
	}
	if _, err = labBindingRepo.New(txRepo).QueueTeamCleanup(txCtx, teamID, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to queue laboratory group cleanup").Err()
	}
	if affected, deleteErr := eventTeamRepo.New(txRepo).Delete(txCtx, eventID, teamID); deleteErr != nil {
		return model.ErrPlatform.WithError(deleteErr).WithMessage("Failed to delete event team").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event team").Err()
	}
	return nil
}

// TransferManagedTeamCaptaincy moves captaincy without requiring the prior
// captain to be the caller. The new captain must already be a team member.
func (u *EventUseCase) TransferManagedTeamCaptaincy(ctx context.Context, eventID, teamID, newCaptainID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	teams, participants := eventTeamRepo.New(txRepo), participantRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	newCaptain, err := participants.Get(txCtx, eventID, newCaptainID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get new team captain").Err()
	}
	if newCaptain.TeamID == nil || *newCaptain.TeamID != teamID || newCaptain.TeamRole == nil || *newCaptain.TeamRole != participantModel.TeamRoleMember {
		return eventTeamModel.ErrEventTeamCaptainInvalid.Err()
	}
	previousCaptainID, expected := team.CaptainID, team.UpdatedAt
	if err = team.TransferCaptain(newCaptainID, previousCaptainID, time.Now()); err != nil {
		return err
	}
	if affected, updateErr := teams.Update(txCtx, team, expected); updateErr != nil {
		return model.ErrPlatform.WithError(updateErr).WithMessage("Failed to transfer team captaincy").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	if affected, roleErr := participants.SetTeamRole(txCtx, eventID, previousCaptainID, teamID, participantModel.TeamRoleMember); roleErr != nil {
		return model.ErrPlatform.WithError(roleErr).WithMessage("Failed to demote previous team captain").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamCaptainRequired.Err()
	}
	if affected, roleErr := participants.SetTeamRole(txCtx, eventID, newCaptainID, teamID, participantModel.TeamRoleCaptain); roleErr != nil {
		return model.ErrPlatform.WithError(roleErr).WithMessage("Failed to promote new team captain").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamCaptainInvalid.Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to transfer team captaincy").Err()
	}
	return nil
}

// GetOwnTeam returns the caller's team and their role in an explicitly
// selected event. A participant without a team receives the ordinary domain
// not-found result rather than another event's team.
func (u *EventUseCase) GetOwnTeam(ctx context.Context, eventID, userID uuid.UUID) (OwnTeamView, error) {
	participant, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return OwnTeamView{}, participantModel.ErrParticipantNotFound.Err()
		}
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if participant.TeamID == nil || participant.TeamRole == nil {
		return OwnTeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
	}
	team, err := u.teams.GetForParticipant(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return OwnTeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant event team").Err()
	}
	fields, err := u.teams.GetExtraFields(ctx, eventID, team.ID)
	if err != nil {
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get own team fields").Err()
	}
	// Participants see no staff-only fields; the required ones they still
	// owe are listed when the organizer asked every team for them.
	missing := []string{}
	teamForm, formErr := u.teams.GetFieldConfig(ctx, eventID)
	if formErr == nil {
		fields = teamForm.WithoutStaffAnswers(fields)
		missing = missingRequired(teamForm, fields)
	} else if !repositoryTools.IsObjectNotFoundError(formErr) && formErr != pgx.ErrNoRows {
		return OwnTeamView{}, model.ErrPlatform.WithError(formErr).WithMessage("Failed to get team fields").Err()
	}
	admitted, err := u.teams.Admitted(ctx, eventID, team.ID)
	if err != nil {
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team admission").Err()
	}
	config, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return OwnTeamView{}, err
	}
	minTeamSize, err := u.teams.MinTeamSize(ctx, eventID)
	if err != nil {
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get minimum team size").Err()
	}
	name, err := u.teamPublicName(ctx, eventID, team)
	if err != nil {
		return OwnTeamView{}, err
	}
	// Only the captain shares the join link; members never receive the code.
	joinCode, joinCodeExpiresAt := "", (*time.Time)(nil)
	if team.IsCaptain(userID) {
		joinCode, joinCodeExpiresAt = team.JoinCode, team.JoinCodeExpiresAt
	}
	formedAt := team.FormedAt
	if formedAt == nil {
		if formedAt, err = u.autoFormedAt(ctx, eventID); err != nil {
			return OwnTeamView{}, err
		}
	}
	return OwnTeamView{Formed: formedAt != nil, FormedAt: formedAt, ID: team.ID, Name: name, JoinCode: joinCode, JoinCodeExpiresAt: joinCodeExpiresAt, CaptainID: team.CaptainID, MemberCount: team.MemberCount, ExtraFields: fields, Role: *participant.TeamRole,
		Admitted: admitted, MinTeamSize: minTeamSize, MaxTeamSize: config.MaxTeamSize,
		MissingFields: missing, BlockingFields: blocksSubmissions(teamForm, missing)}, nil
}

// ListTeams returns an event-scoped cursor page for administration. Join
// codes never appear in this management view. Members and pending
// invitations of every listed team are loaded in two batch queries.
func (u *EventUseCase) ListTeams(ctx context.Context, filter ListTeamsFilter) (TeamsListResult, error) {
	limit := filter.PageSize
	if limit <= 0 || limit > pagination.MaxPageSize {
		limit = pagination.DefaultPageSize
	}
	cursorCreatedAt, cursorID := cursorSentinelTime, maxUUID
	if filter.Cursor != uuid.Nil {
		if team, err := u.teams.GetByID(ctx, filter.EventID, filter.Cursor); err == nil {
			cursorCreatedAt, cursorID = team.CreatedAt, team.ID
		}
	}
	admission := int32(-1)
	if filter.Admitted != nil {
		admission = 0
		if *filter.Admitted {
			admission = 1
		}
	}
	rows, err := u.teams.List(ctx, filter.EventID, filter.Search, admission, answerFiltersJSON(filter.Fields), cursorCreatedAt, cursorID, int32(limit+1))
	if err != nil {
		return TeamsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event teams").Err()
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	minTeamSize, err := u.teams.MinTeamSize(ctx, filter.EventID)
	if err != nil {
		return TeamsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get minimum team size").Err()
	}
	autoFormed, err := u.autoFormedAt(ctx, filter.EventID)
	if err != nil {
		return TeamsListResult{}, err
	}
	items, err := buildTeamViews(ctx, u.participants, filter.EventID, rows, minTeamSize, autoFormed)
	if err != nil {
		return TeamsListResult{}, err
	}
	var next uuid.UUID
	if hasMore && len(items) > 0 {
		next = items[len(items)-1].ID
	}
	total, err := u.teams.CountMatching(ctx, filter.EventID, filter.Search, admission, answerFiltersJSON(filter.Fields))
	if err != nil {
		return TeamsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count event teams").Err()
	}
	return TeamsListResult{Teams: items, NextCursor: next, HasMore: hasMore, Total: total}, nil
}

// buildTeamViews composes the moderator team views: aggregate, answers,
// admission, members (real name + pseudonym) and pending invitations.
func buildTeamViews(ctx context.Context, participants *participantRepo.Repository, eventID uuid.UUID, rows []eventTeamRepo.ListedTeam, minTeamSize int32, autoFormedAt *time.Time) ([]TeamView, error) {
	teamIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		teamIDs = append(teamIDs, row.Team.ID)
	}
	members := map[uuid.UUID][]TeamMemberView{}
	invitations := map[uuid.UUID][]TeamInvitationView{}
	if len(teamIDs) > 0 {
		memberRows, err := participants.TeamMembers(ctx, eventID, teamIDs)
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list team members").Err()
		}
		for _, m := range memberRows {
			members[m.TeamID] = append(members[m.TeamID], TeamMemberView{UserID: m.UserID, Name: strings.TrimSpace(m.FirstName + " " + m.LastName), Email: m.Email, Pseudonym: m.Pseudonym, Role: m.Role, LastSeenAt: m.LastSeenAt, LastLabAt: m.LastLabAt})
		}
		invitationRows, err := participants.PendingTeamInvitations(ctx, eventID, teamIDs)
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list team invitations").Err()
		}
		for _, i := range invitationRows {
			invitations[i.TeamID] = append(invitations[i.TeamID], TeamInvitationView{UserID: i.UserID, Email: i.Email, CreatedAt: i.CreatedAt, InvitationSentAt: i.InvitationSentAt})
		}
	}
	items := make([]TeamView, 0, len(rows))
	for _, row := range rows {
		team := row.Team
		teamMembers, teamInvitations := members[team.ID], invitations[team.ID]
		if teamMembers == nil {
			teamMembers = []TeamMemberView{}
		}
		if teamInvitations == nil {
			teamInvitations = []TeamInvitationView{}
		}
		formedAt := team.FormedAt
		if formedAt == nil {
			formedAt = autoFormedAt
		}
		items = append(items, TeamView{
			FormedAt: formedAt, Formed: formedAt != nil,
			ID: team.ID, Name: team.Name, CaptainID: team.CaptainID, Hidden: team.Hidden, MemberCount: team.MemberCount,
			ExtraFields: row.ExtraFields, FieldsMissing: row.FieldsMissing, CreatedAt: team.CreatedAt, Members: teamMembers, PendingInvitations: teamInvitations,
			Admitted: row.Admitted, AdmittedManually: team.AdmittedManually, MinTeamSize: minTeamSize,
			CaptainPending: captainPending(team.CaptainID, teamInvitations),
		})
	}
	return items, nil
}

// captainPending reports a captain named from a still pending invitation.
func captainPending(captainID uuid.UUID, invitations []TeamInvitationView) bool {
	for _, invitation := range invitations {
		if invitation.UserID == captainID {
			return true
		}
	}
	return false
}

// managedTeamView re-reads one team inside the caller's unit of work and
// returns the same shape as ListTeams.
func managedTeamView(ctx context.Context, repo IRepository, eventID uuid.UUID, team eventTeamModel.EventTeam) (TeamView, error) {
	teams := eventTeamRepo.New(repo)
	fields, err := teams.GetExtraFields(ctx, eventID, team.ID)
	if err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	admitted, err := teams.Admitted(ctx, eventID, team.ID)
	if err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team admission").Err()
	}
	minTeamSize, err := teams.MinTeamSize(ctx, eventID)
	if err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get minimum team size").Err()
	}
	if fields == nil {
		fields = map[string]any{}
	}
	autoFormed, err := autoFormedAt(ctx, repo, eventID)
	if err != nil {
		return TeamView{}, err
	}
	views, err := buildTeamViews(ctx, participantRepo.New(repo), eventID, []eventTeamRepo.ListedTeam{{Team: team, ExtraFields: fields, Admitted: admitted}}, minTeamSize, autoFormed)
	if err != nil {
		return TeamView{}, err
	}
	return views[0], nil
}

// GetTeamProfile is the organizer's read-only view of one team: roster,
// captain, admission status, form answers and its results. Results are nil
// for a team the scoreboard does not list.
func (u *EventUseCase) GetTeamProfile(ctx context.Context, eventID, teamID uuid.UUID) (TeamProfileView, error) {
	team, err := u.teams.GetByID(ctx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return TeamProfileView{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return TeamProfileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	fields, err := u.teams.GetExtraFields(ctx, eventID, team.ID)
	if err != nil {
		return TeamProfileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	if fields == nil {
		fields = map[string]any{}
	}
	admitted, err := u.teams.Admitted(ctx, eventID, team.ID)
	if err != nil {
		return TeamProfileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team admission").Err()
	}
	minTeamSize, err := u.teams.MinTeamSize(ctx, eventID)
	if err != nil {
		return TeamProfileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get minimum team size").Err()
	}
	autoFormed, err := u.autoFormedAt(ctx, eventID)
	if err != nil {
		return TeamProfileView{}, err
	}
	views, err := buildTeamViews(ctx, u.participants, eventID, []eventTeamRepo.ListedTeam{{Team: team, ExtraFields: fields, Admitted: admitted}}, minTeamSize, autoFormed)
	if err != nil {
		return TeamProfileView{}, err
	}
	results, err := u.GetManageResults(ctx, eventID)
	if err != nil {
		return TeamProfileView{}, err
	}
	profile := TeamProfileView{Team: views[0]}
	for i := range results.Teams {
		if results.Teams[i].TeamID == team.ID {
			profile.Results = &results.Teams[i]
			break
		}
	}
	return profile, nil
}

// SetTeamAdmission records the moderator's manual admission («допущена
// вручну»). It is valid at any time, including after the start.
func (u *EventUseCase) SetTeamAdmission(ctx context.Context, eventID, teamID uuid.UUID, admittedManually bool) (TeamView, error) {
	if u.uow == nil {
		return TeamView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return TeamView{}, err
	}
	defer unit.Restore()
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return TeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	if team.Individual {
		return TeamView{}, eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	expected := team.UpdatedAt
	team.SetAdmittedManually(admittedManually, time.Now())
	if affected, updateErr := teams.Update(txCtx, team, expected); updateErr != nil {
		return TeamView{}, model.ErrPlatform.WithError(updateErr).WithMessage("Failed to update team admission").Err()
	} else if affected == 0 {
		return TeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
	}
	view, err := managedTeamView(txCtx, txRepo, eventID, team)
	if err != nil {
		return TeamView{}, err
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, teamID, time.Now()); err != nil {
		return TeamView{}, err
	}
	// Admission is part of the results visibility rule.
	if err = recordScoreboardRecalculation(txCtx, txRepo, eventID); err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
	}
	if err = unit.Save(); err != nil {
		return TeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update team admission").Err()
	}
	return view, nil
}

// UpdateOwnTeamFields lets the captain change the team-field answers that the
// organizer marked editable, until the event finishes.
func (u *EventUseCase) UpdateOwnTeamFields(ctx context.Context, eventID, teamID, userID uuid.UUID, fields map[string]any) (OwnTeamView, error) {
	if u.uow == nil {
		return OwnTeamView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return OwnTeamView{}, err
	}
	defer unit.Restore()
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return OwnTeamView{}, eventModel.ErrEventNotFound.Err()
		}
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	status := e.Lifecycle.Status(time.Now())
	if status == eventModel.LifecycleFinished || status == eventModel.LifecycleWithdrawn {
		return OwnTeamView{}, eventTeamModel.ErrEventTeamFieldsLocked.Err()
	}
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return OwnTeamView{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	if err = requireCaptainOfTeam(txCtx, participantRepo.New(txRepo), team, eventID, userID); err != nil {
		return OwnTeamView{}, err
	}
	form, err := teams.GetFieldConfig(txCtx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return OwnTeamView{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage("Team fields are not configured").Err()
		}
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	editable := map[string]bool{}
	for _, block := range form.Document.Blocks {
		if block.Type == "field" {
			editable[block.Key] = block.Editable
		}
	}
	current, err := teams.GetExtraFields(txCtx, eventID, teamID)
	if err != nil {
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	// A field the team has not filled yet (a new required one) can be filled
	// once even when it is not editable; staff-only fields never.
	staff := form.StaffKeys()
	for key := range editable {
		if !staff[key] && !editable[key] && emptyAnswer(current[key]) {
			editable[key] = true
		}
	}
	merged := make(map[string]any, len(current)+len(fields))
	for key, value := range current {
		merged[key] = value
	}
	for key, value := range fields {
		allowed, known := editable[key]
		if !known {
			return OwnTeamView{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage("Unknown team field: " + key).Err()
		}
		if !allowed {
			return OwnTeamView{}, eventTeamModel.ErrEventTeamFieldNotEditable.Err()
		}
		merged[key] = value
	}
	bound, err := validateTeamFieldAnswersKeeping(txCtx, txRepo, eventID, userID, teamID, merged, false, current)
	if err != nil {
		return OwnTeamView{}, err
	}
	bound.values = form.KeepStaffAnswers(current, bound.values)
	if err = saveTeamFields(txCtx, txRepo, eventID, teamID, bound); err != nil {
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save team fields").Err()
	}
	if err = bound.attach(txCtx, eventAnswerFileRepo.New(txRepo), eventID, eventFormModel.AnswerScopeTeam, teamID); err != nil {
		return OwnTeamView{}, err
	}
	if err = unit.Save(); err != nil {
		return OwnTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save team fields").Err()
	}
	return u.GetOwnTeam(ctx, eventID, userID)
}

// CreateTeam creates a competing unit and assigns its approved creator as
// captain. LockEventForTeamChange serializes the maxTeams check per event.
func (u *EventUseCase) CreateTeam(ctx context.Context, eventID, userID uuid.UUID, name string) error {
	return u.CreateTeamWithFields(ctx, eventID, userID, name, nil)
}

func (u *EventUseCase) CreateTeamWithFields(ctx context.Context, eventID, userID uuid.UUID, name string, fields map[string]any) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if _, err = txRepo.LockEventForTeamChange(txCtx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to lock event for team creation").Err()
	}
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationTeam {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	if err := rosterOpen(txCtx, txRepo, eventID, time.Now()); err != nil {
		return err
	}
	if config.MaxTeams != nil {
		count, countErr := txRepo.CountEventTeams(txCtx, eventID)
		if countErr != nil {
			return model.ErrPlatform.WithError(countErr).WithMessage("Failed to count event teams").Err()
		}
		if count >= int64(*config.MaxTeams) {
			return eventTeamModel.ErrEventTeamFull.Err()
		}
	}
	participant, err := participantRepo.New(txRepo).Get(txCtx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if participant.Status != participantModel.StatusApproved {
		return participantModel.ErrParticipantNotApproved.Err()
	}
	if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, userID, eventFormModel.CapabilityTeamJoinOrCreate); err != nil {
		return err
	}
	if participant.TeamID != nil {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	bound, err := validateTeamFieldAnswers(txCtx, txRepo, eventID, userID, uuid.Nil, fields, false)
	if err != nil {
		return err
	}
	now := time.Now()
	team, err := eventTeamModel.New(eventID, userID, name, uuid.Must(uuid.NewV4()).String(), now)
	if err != nil {
		return err
	}
	created, err := eventTeamRepo.New(txRepo).Create(txCtx, team)
	if err != nil {
		if creator, ok := repositoryTools.UniqueViolationError(err, eventTeamModel.ErrEventTeamExists); ok {
			return creator.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create event team").Err()
	}
	if len(bound.values) > 0 {
		if err = saveTeamFields(txCtx, txRepo, eventID, created.ID, bound); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to save team fields").Err()
		}
		if err = bound.attach(txCtx, eventAnswerFileRepo.New(txRepo), eventID, eventFormModel.AnswerScopeTeam, created.ID); err != nil {
			return err
		}
	}
	if err = participant.AssignTeam(created.ID, participantModel.TeamRoleCaptain); err != nil {
		return err
	}
	if affected, assignErr := participantRepo.New(txRepo).AssignTeam(txCtx, participant); assignErr != nil || affected == 0 {
		if assignErr != nil {
			return model.ErrPlatform.WithError(assignErr).WithMessage("Failed to assign team captain").Err()
		}
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, created.ID, now); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create event team").Err()
	}
	return nil
}

// LeaveTeam removes a non-captain participant from their own team. A captain
// must first transfer captaincy, which prevents a team from losing its only
// authority while preserving the universal team aggregate.
func (u *EventUseCase) LeaveTeam(ctx context.Context, eventID, userID uuid.UUID) error {
	return u.removeTeamMember(ctx, eventID, userID, userID, false)
}

// KickTeamMember lets the captain remove another non-captain participant from
// the same team. Both the caller and target are resolved in the requested
// event, so neither an arbitrary team ID nor a foreign event can be used.
func (u *EventUseCase) KickTeamMember(ctx context.Context, eventID, captainID, userID uuid.UUID) error {
	return u.removeTeamMember(ctx, eventID, captainID, userID, true)
}

// DisbandTeam removes a team at the captain's explicit request. PostgreSQL's
// ON DELETE SET NULL detaches all members in the same transaction. Individual
// participation deliberately cannot disband: its team is the participant's
// required universal competing unit.
func (u *EventUseCase) DisbandTeam(ctx context.Context, eventID, teamID, captainID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation != nil && *config.Participation == eventConfigModel.ParticipationIndividual {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	rosterEvent, err := openRosterEvent(txCtx, txRepo, eventID, time.Now())
	if err != nil {
		return err
	}
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	if err = requireCaptainOfTeam(txCtx, participantRepo.New(txRepo), team, eventID, captainID); err != nil {
		return err
	}
	// Disbanding detaches every member, which would free them to join another
	// team, so a formed team cannot be disbanded.
	if team.Formed(rosterEvent.Lifecycle.FormsTeamsAtStart(time.Now())) {
		return eventTeamModel.ErrEventTeamSwitchLocked.Err()
	}
	if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, captainID, eventFormModel.CapabilityTeamManage); err != nil {
		return err
	}
	if _, err = labBindingRepo.New(txRepo).QueueTeamCleanup(txCtx, teamID, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to queue laboratory group cleanup").Err()
	}
	if affected, deleteErr := teams.Delete(txCtx, eventID, teamID); deleteErr != nil {
		return model.ErrPlatform.WithError(deleteErr).WithMessage("Failed to disband event team").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to disband event team").Err()
	}
	return nil
}

// RenameTeam lets only the current captain change their team's display name.
func (u *EventUseCase) RenameTeam(ctx context.Context, eventID, teamID, userID uuid.UUID, name string) error {
	return u.mutateCaptainTeam(ctx, eventID, teamID, userID, func(team *eventTeamModel.EventTeam, _ eventModel.Event, now time.Time) error {
		return team.Rename(name, userID, now)
	})
}

// RegenerateTeamJoinCode invalidates the old join link and replaces it with a
// fresh UUID-backed value that lives as long as the captain chose. The caller
// never supplies the secret.
func (u *EventUseCase) RegenerateTeamJoinCode(ctx context.Context, eventID, teamID, userID uuid.UUID, expiry eventTeamModel.JoinCodeExpiry) error {
	return u.mutateCaptainTeam(ctx, eventID, teamID, userID, func(team *eventTeamModel.EventTeam, event eventModel.Event, now time.Time) error {
		expiresAt, err := expiry.ExpiresAt(now, event.Lifecycle.StartAt)
		if err != nil {
			return err
		}
		return team.RegenerateJoinCode(uuid.Must(uuid.NewV4()).String(), expiresAt, userID, now)
	})
}

// TransferTeamCaptaincy moves captain authority to another member of the same
// team. Team ownership and both membership roles are written in one unit of
// work, so readers cannot observe a permanent split-brain captain state.
func (u *EventUseCase) TransferTeamCaptaincy(ctx context.Context, eventID, teamID, captainID, newCaptainID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if err = rosterOpen(txCtx, txRepo, eventID, time.Now()); err != nil {
		return err
	}
	if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, captainID, eventFormModel.CapabilityTeamManage); err != nil {
		return err
	}
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	members := participantRepo.New(txRepo)
	newCaptain, err := members.Get(txCtx, eventID, newCaptainID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get new team captain").Err()
	}
	if newCaptain.TeamID == nil || *newCaptain.TeamID != teamID || newCaptain.TeamRole == nil || *newCaptain.TeamRole != participantModel.TeamRoleMember {
		return eventTeamModel.ErrEventTeamCaptainInvalid.Err()
	}
	expectedUpdatedAt := team.UpdatedAt
	if err = team.TransferCaptain(newCaptainID, captainID, time.Now()); err != nil {
		return err
	}
	if affected, updateErr := teams.Update(txCtx, team, expectedUpdatedAt); updateErr != nil {
		return model.ErrPlatform.WithError(updateErr).WithMessage("Failed to transfer team captaincy").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	if affected, roleErr := members.SetTeamRole(txCtx, eventID, captainID, teamID, participantModel.TeamRoleMember); roleErr != nil {
		return model.ErrPlatform.WithError(roleErr).WithMessage("Failed to demote previous team captain").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamCaptainRequired.Err()
	}
	if affected, roleErr := members.SetTeamRole(txCtx, eventID, newCaptainID, teamID, participantModel.TeamRoleCaptain); roleErr != nil {
		return model.ErrPlatform.WithError(roleErr).WithMessage("Failed to promote new team captain").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamCaptainInvalid.Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to transfer team captaincy").Err()
	}
	return nil
}

// requireCaptainOfTeam is the captain gate of every captain-only action: the
// team names the person as its captain AND the person is still an approved
// participant of that very team. A captain a moderator rejected or moved keeps
// the captain_id on the team but none of the rights.
func requireCaptainOfTeam(ctx context.Context, participants *participantRepo.Repository, team eventTeamModel.EventTeam, eventID, userID uuid.UUID) error {
	if !team.IsCaptain(userID) {
		return eventTeamModel.ErrEventTeamCaptainRequired.Err()
	}
	p, err := participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamCaptainRequired.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil || *p.TeamID != team.ID {
		return eventTeamModel.ErrEventTeamCaptainRequired.Err()
	}
	return nil
}

func (u *EventUseCase) mutateCaptainTeam(ctx context.Context, eventID, teamID, userID uuid.UUID, mutate func(*eventTeamModel.EventTeam, eventModel.Event, time.Time) error) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	event, err := openRosterEvent(txCtx, txRepo, eventID, time.Now())
	if err != nil {
		return err
	}
	if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, userID, eventFormModel.CapabilityTeamManage); err != nil {
		return err
	}
	team, err := eventTeamRepo.New(txRepo).GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	if err = requireCaptainOfTeam(txCtx, participantRepo.New(txRepo), team, eventID, userID); err != nil {
		return err
	}
	expectedUpdatedAt := team.UpdatedAt
	if err = mutate(&team, event, time.Now()); err != nil {
		return err
	}
	if affected, updateErr := eventTeamRepo.New(txRepo).Update(txCtx, team, expectedUpdatedAt); updateErr != nil {
		return model.ErrPlatform.WithError(updateErr).WithMessage("Failed to update event team").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update event team").Err()
	}
	return nil
}

func (u *EventUseCase) removeTeamMember(ctx context.Context, eventID, actorID, targetID uuid.UUID, kicked bool) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	rosterEvent, err := openRosterEvent(txCtx, txRepo, eventID, time.Now())
	if err != nil {
		return err
	}
	if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, actorID, eventFormModel.CapabilityTeamManage); err != nil {
		return err
	}
	participants := participantRepo.New(txRepo)
	target, err := participants.Get(txCtx, eventID, targetID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if target.TeamID == nil || target.TeamRole == nil {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	team, err := eventTeamRepo.New(txRepo).GetByID(txCtx, eventID, *target.TeamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	if target.TeamRole != nil && *target.TeamRole == participantModel.TeamRoleCaptain {
		return eventTeamModel.ErrEventTeamCaptainMustTransfer.Err()
	}
	if kicked {
		actor, actorErr := participants.Get(txCtx, eventID, actorID)
		if actorErr != nil {
			if repositoryTools.IsObjectNotFoundError(actorErr) {
				return participantModel.ErrParticipantNotFound.Err()
			}
			return model.ErrPlatform.WithError(actorErr).WithMessage("Failed to get team captain").Err()
		}
		if actor.TeamID == nil || *actor.TeamID != team.ID || !team.IsCaptain(actorID) {
			return eventTeamModel.ErrEventTeamCaptainRequired.Err()
		}
	}
	if affected, removeErr := eventTeamRepo.New(txRepo).TryRemoveMember(txCtx, eventID, team.ID, time.Now()); removeErr != nil {
		return model.ErrPlatform.WithError(removeErr).WithMessage("Failed to update event team size").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if affected, clearErr := participants.ClearTeam(txCtx, eventID, targetID, team.ID); clearErr != nil {
		return model.ErrPlatform.WithError(clearErr).WithMessage("Failed to remove event team member").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	// Leaving or being kicked from a formed team is leaving the event: the
	// person cannot end up in another team.
	if team.Formed(rosterEvent.Lifecycle.FormsTeamsAtStart(time.Now())) {
		if err = removeFromEvent(txCtx, participants, target, time.Now()); err != nil {
			return err
		}
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, team.ID, time.Now()); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove event team member").Err()
	}
	u.dropMemberLabClient(ctx, eventID, team.ID, targetID)
	return nil
}

// teamFormedNow is the formation rule for a loaded team: an explicit
// formation, or the start of an event without late join.
func teamFormedNow(ctx context.Context, repo IRepository, eventID uuid.UUID, team eventTeamModel.EventTeam, now time.Time) (bool, error) {
	event, err := eventRepo.New(repo).GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return false, eventModel.ErrEventNotFound.Err()
		}
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to get event for team formation").Err()
	}
	return team.Formed(event.Lifecycle.FormsTeamsAtStart(now)), nil
}

// removeFromEvent turns the removal of a member of a formed team into leaving
// the event: the approval is revoked, so the person cannot register again or
// join another team; only staff can bring them back.
func removeFromEvent(ctx context.Context, participants *participantRepo.Repository, p participantModel.Participant, now time.Time) error {
	p.RemoveFromEvent(now)
	if affected, err := participants.RemoveFromEvent(ctx, p); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove participant from the event").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantNotFound.Err()
	}
	return nil
}

func rosterOpen(ctx context.Context, repo IRepository, eventID uuid.UUID, now time.Time) error {
	_, err := openRosterEvent(ctx, repo, eventID, now)
	return err
}

// openRosterEvent is rosterOpen that also hands back the event it checked.
func openRosterEvent(ctx context.Context, repo IRepository, eventID uuid.UUID, now time.Time) (eventModel.Event, error) {
	event, err := eventRepo.New(repo).GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.Event{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return eventModel.Event{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event for team change").Err()
	}
	if !event.Lifecycle.RosterOpen(now) {
		return eventModel.Event{}, eventTeamModel.ErrEventTeamRosterLocked.Err()
	}
	return event, nil
}

// createIndividualTeam provides the universal competing unit for an approved
// individual participant. It deliberately runs as one transaction for the
// team and membership rows, and is only called after the participant record
// itself has been created or approved.
func (u *EventUseCase) createIndividualTeam(ctx context.Context, eventID, userID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if err = u.createIndividualTeamInTransaction(txCtx, txRepo, eventID, userID); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create individual event team").Err()
	}
	return nil
}

func (u *EventUseCase) createIndividualTeamInTransaction(txCtx context.Context, txRepo IRepository, eventID, userID uuid.UUID) error {
	if _, err := txRepo.LockEventForTeamChange(txCtx, eventID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to lock event for individual team").Err()
	}
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationIndividual {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	participant, err := participantRepo.New(txRepo).Get(txCtx, eventID, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get individual participant").Err()
	}
	if participant.Status != participantModel.StatusApproved {
		return participantModel.ErrParticipantNotApproved.Err()
	}
	if participant.TeamID != nil {
		return nil
	}
	// MaxTeams limits team mode only (hidden in individual settings), so it
	// never blocks an individual approval. The stored name is technical:
	// every read presents the participant's public name instead.
	now := time.Now()
	team, err := eventTeamModel.NewIndividual(eventID, userID, fmt.Sprintf("Solo-%s", userID.String()[:8]), uuid.Must(uuid.NewV4()).String(), now)
	if err != nil {
		return err
	}
	created, err := eventTeamRepo.New(txRepo).Create(txCtx, team)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create individual event team").Err()
	}
	if err = participant.AssignTeam(created.ID, participantModel.TeamRoleCaptain); err != nil {
		return err
	}
	if affected, assignErr := participantRepo.New(txRepo).AssignTeam(txCtx, participant); assignErr != nil {
		return model.ErrPlatform.WithError(assignErr).WithMessage("Failed to assign individual event team").Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, created.ID, now); err != nil {
		return err
	}
	return nil
}

// FormOwnTeam is the captain's «confirm the roster»: from now on the roster is
// closed for good and the team gets its tasks, labs and VPN. It needs the
// event's minimum team size.
func (u *EventUseCase) FormOwnTeam(ctx context.Context, eventID, captainID uuid.UUID) error {
	p, err := u.participants.Get(ctx, eventID, captainID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	return u.formTeam(ctx, eventID, *p.TeamID, captainID, false)
}

// FormManagedTeam is the moderator's «form the team» for the stuck case (for
// example a minimum size the team can never reach). The route gate authorizes
// the moderator.
func (u *EventUseCase) FormManagedTeam(ctx context.Context, eventID, teamID, moderatorID uuid.UUID) error {
	return u.formTeam(ctx, eventID, teamID, moderatorID, true)
}

func (u *EventUseCase) formTeam(ctx context.Context, eventID, teamID, by uuid.UUID, force bool) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationTeam {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	now := time.Now()
	if !force {
		if err = rosterOpen(txCtx, txRepo, eventID, now); err != nil {
			return err
		}
		if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, by, eventFormModel.CapabilityTeamManage); err != nil {
			return err
		}
	}
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	event, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	minTeamSize, err := teams.MinTeamSize(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get minimum team size").Err()
	}
	if err = team.Form(by, force, minTeamSize, event.Lifecycle.FormsTeamsAtStart(now), now); err != nil {
		return err
	}
	if formed, formErr := teams.Form(txCtx, eventID, teamID, &by, now); formErr != nil {
		return model.ErrPlatform.WithError(formErr).WithMessage("Failed to form event team").Err()
	} else if !formed {
		return eventTeamModel.ErrEventTeamFormed.Err()
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, teamID, now); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to form event team").Err()
	}
	// The team's prepared assignments open now instead of at the next engine pass.
	if err = u.publishAvailableChallenges(ctx, eventID); err != nil {
		return err
	}
	return u.requestLabAccessSyncAfterReady(ctx, teamID)
}

// autoFormedAt is the moment every team of the event formed by itself (the start
// of an event without late join), nil while that has not happened or with late
// join, where only a confirmation forms a team.
func autoFormedAt(ctx context.Context, repo IRepository, eventID uuid.UUID) (*time.Time, error) {
	return autoFormedFrom(eventRepo.New(repo).GetByID(ctx, eventID))
}

func (u *EventUseCase) autoFormedAt(ctx context.Context, eventID uuid.UUID) (*time.Time, error) {
	return autoFormedFrom(u.events.GetByID(ctx, eventID))
}

func autoFormedFrom(event eventModel.Event, err error) (*time.Time, error) {
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event for team formation").Err()
	}
	if !event.Lifecycle.FormsTeamsAtStart(time.Now()) {
		return nil, nil
	}
	start := event.Lifecycle.StartAt
	return &start, nil
}
