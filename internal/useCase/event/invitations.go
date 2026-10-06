package event

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

type ParticipantInvitationInput struct {
	Email string
	// FirstName and LastName are optional: they prefill the pending account
	// when the invitation creates one; an existing account keeps its profile.
	FirstName string
	LastName  string
	// Fields prefill the person's participant form answers (a CSV import);
	// required fields may be missing, the person completes them on accepting.
	Fields map[string]any
}

// Invitation outcome codes: the client shows its own text for each.
const (
	InvitationCodeEmailInvalid       = "email_invalid"
	InvitationCodeAlreadyParticipant = "already_participant"
	InvitationCodeAccountUnavailable = "account_unavailable"
	InvitationCodeStaff              = "staff_cannot_participate"
	InvitationCodeFieldsInvalid      = "fields_invalid"
	InvitationCodeFailed             = "failed"
)

type ParticipantInvitationResult struct {
	Email  string
	UserID uuid.UUID
	Error  string
	// Code is empty on success, else one of the InvitationCode* values.
	Code string `json:"Code,omitempty"`
	// SentAt is set by a resend: the recorded delivery time.
	SentAt *time.Time `json:"InvitationSentAt,omitempty"`
}

// InviteParticipants handles every address independently, so a bad CSV row
// cannot roll back invitations that were accepted by the server.
func (u *EventUseCase) InviteParticipants(ctx context.Context, eventID, by uuid.UUID, entries []ParticipantInvitationInput) ([]ParticipantInvitationResult, error) {
	return u.inviteParticipants(ctx, eventID, uuid.NullUUID{}, "", by, entries)
}

func (u *EventUseCase) InviteTeamMembers(ctx context.Context, eventID, teamID, by uuid.UUID, entries []ParticipantInvitationInput) ([]ParticipantInvitationResult, error) {
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationTeam {
		return nil, eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	team, err := u.teams.GetByID(ctx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	// The seats are checked for each invitation in its own transaction, under the event's roster lock
	// (requireTeamSeat): a count made here would be stale by then.
	return u.inviteParticipants(ctx, eventID, uuid.NullUUID{UUID: teamID, Valid: true}, team.Name, by, entries)
}

func uniqueInvitationEmails(entries []ParticipantInvitationInput) map[string]struct{} {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		seen[userModel.NormalizeEmail(entry.Email)] = struct{}{}
	}
	return seen
}

func (u *EventUseCase) inviteParticipants(ctx context.Context, eventID uuid.UUID, targetTeamID uuid.NullUUID, targetTeamName string, by uuid.UUID, entries []ParticipantInvitationInput) ([]ParticipantInvitationResult, error) {
	if len(entries) == 0 || len(entries) > 200 {
		return nil, participantModel.ErrInvitationEmailInvalid.WithMessage("Provide 1 to 200 email addresses").Err()
	}
	var form *eventFormRepo.Version
	for _, entry := range entries {
		if len(entry.Fields) > 0 {
			var err error
			if form, err = prefillForm(ctx, u.forms, eventID); err != nil {
				return nil, err
			}
			break
		}
	}
	results := make([]ParticipantInvitationResult, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		email := userModel.NormalizeEmail(entry.Email)
		if _, exists := seen[email]; exists {
			continue
		}
		seen[email] = struct{}{}
		result := ParticipantInvitationResult{Email: email}
		var err error
		if !validatePrefill(form, entry.Fields) {
			result.Error, result.Code = "Некоректні значення полів анкети", InvitationCodeFieldsInvalid
			results = append(results, result)
			continue
		}
		result.UserID, err = u.inviteParticipant(ctx, eventID, targetTeamID, targetTeamName, by, email, strings.TrimSpace(entry.FirstName), strings.TrimSpace(entry.LastName), form, entry.Fields)
		if err != nil {
			switch {
			case errors.Is(err, participantModel.ErrInvitationEmailInvalid.Err()):
				result.Error, result.Code = "Некоректна адреса електронної пошти", InvitationCodeEmailInvalid
			case errors.Is(err, participantModel.ErrAlreadyParticipant.Err()):
				result.Error, result.Code = "Уже бере участь або подав заявку", InvitationCodeAlreadyParticipant
			case errors.Is(err, participantModel.ErrStaffCannotParticipate.Err()):
				result.Error, result.Code = "Власники й модератори заходу не беруть участі як учасники", InvitationCodeStaff
			default:
				// An account that cannot be invited (blocked, deleted) is answered
				// like any other failure: the organizer must not learn the state
				// of somebody else's account from an invitation.
				result.Error, result.Code = "Не вдалося надіслати запрошення", InvitationCodeFailed
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (u *EventUseCase) inviteParticipant(ctx context.Context, eventID uuid.UUID, targetTeamID uuid.NullUUID, targetTeamName string, by uuid.UUID, email, firstName, lastName string, form *eventFormRepo.Version, fields map[string]any) (uuid.UUID, error) {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return uuid.Nil, participantModel.ErrInvitationEmailInvalid.Err()
	}
	if err = userModel.ValidName(firstName, lastName); err != nil {
		return uuid.Nil, err
	}
	if u.uow == nil || u.invitationNotifier == nil || u.setupTokens == nil || u.eventDomain == "" || u.idHost == "" {
		return uuid.Nil, model.ErrPlatform.WithMessage("Event invitation dependencies are not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer unit.Restore()
	if targetTeamID.Valid {
		// The seats are counted under the event's roster lock, in the transaction that takes one: two
		// invitations cannot both pass a check made earlier and over-invite the team.
		if err = lockTeamRoster(txCtx, txRepo, eventID); err != nil {
			return uuid.Nil, err
		}
		if err = u.requireTeamSeat(txCtx, txRepo, eventID, targetTeamID.UUID); err != nil {
			return uuid.Nil, err
		}
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	users := userRepo.New(txRepo)
	user, err := users.GetByEmail(txCtx, email)
	if repositoryTools.IsObjectNotFoundError(err) || errors.Is(err, pgx.ErrNoRows) {
		pending := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, time.Now().UTC())
		pending.FirstName, pending.LastName = firstName, lastName
		user, err = users.Create(txCtx, pending)
	}
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to prepare invited account").Err()
	}
	if user.Status != userModel.UserStatusActive && user.Status != userModel.UserStatusIncomplete {
		return uuid.Nil, participantModel.ErrInvitationRequired.WithMessage("This account cannot be invited").Err()
	}
	if staff, staffErr := u.isEventStaff(txCtx, eventID, user.ID); staffErr != nil {
		return uuid.Nil, staffErr
	} else if staff {
		return uuid.Nil, registerError(ReasonStaff)
	}
	participants := participantRepo.New(txRepo)
	_, ok, err := participants.Invite(txCtx, eventID, user.ID, by, targetTeamID, time.Now().UTC())
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to create participant invitation").Err()
	}
	if !ok {
		existing, lookupErr := participants.Get(txCtx, eventID, user.ID)
		if lookupErr != nil || existing.Status != participantModel.StatusPending || !existing.Invited ||
			existing.InvitedToTeam != targetTeamID.Valid || (targetTeamID.Valid && (existing.InvitedTeamID == nil || *existing.InvitedTeamID != targetTeamID.UUID)) {
			return uuid.Nil, participantModel.ErrAlreadyParticipant.Err()
		}
	}
	if len(fields) > 0 && form != nil {
		if err = savePrefilledAnswers(txCtx, eventFormRepo.New(txRepo), eventID, user.ID, *form, fields, time.Now().UTC()); err != nil {
			return uuid.Nil, err
		}
	}
	if err = unit.Save(); err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to save invitation").Err()
	}
	if _, err = u.sendParticipantInvitation(ctx, e, user, targetTeamID.Valid, targetTeamName, by); err != nil {
		return user.ID, err
	}
	return user.ID, nil
}

// sendParticipantInvitation queues the invitation email (a setup link first
// for accounts that were created by the invitation) and records the delivery
// time, so a failed send stays visible and can be resent (M7).
func (u *EventUseCase) sendParticipantInvitation(ctx context.Context, e eventModel.Event, user userModel.User, toTeam bool, teamName string, by uuid.UUID) (time.Time, error) {
	inviteURL := fmt.Sprintf("https://%s.%s/invite", e.Tag, u.eventDomain)
	if user.Status == userModel.UserStatusIncomplete {
		token, tokenErr := u.setupTokens.GenerateSetupToken(ctx, user.ID, 0)
		if tokenErr != nil {
			return time.Time{}, model.ErrPlatform.WithError(tokenErr).WithMessage("Failed to issue invitation setup link").Err()
		}
		inviteURL = fmt.Sprintf("https://%s/setup/?token=%s&return_to=%s", u.idHost, url.QueryEscape(token), url.QueryEscape(inviteURL))
	}
	invitationType := signalModel.TypeParticipantInvitationSent
	if toTeam {
		invitationType = signalModel.TypeParticipantTeamInvitationSent
	}
	payload := notificationPayloads.DefaultPayload{
		Type:     notificationTypes.NotificationType(invitationType),
		Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail},
		Variables: map[string]interface{}{
			"scope_event_id": e.ID.String(), "actor_user_id": by.String(), "subject_user_id": user.ID.String(),
			"event_name": e.Name, "event_tag": e.Tag, "registration": string(signalModel.RegistrationInvitation),
			"user_id": user.ID.String(), "user_email": user.Email, "user_first_name": user.FirstName,
			"user_last_name": user.LastName, "user_name": user.FullName(), "user_picture": user.Picture,
			"invite_url": inviteURL,
			"team_name":  teamName,
			"team_url":   fmt.Sprintf("https://%s.%s/participation?tab=team", e.Tag, u.eventDomain),
			"event_url":  fmt.Sprintf("https://%s.%s/", e.Tag, u.eventDomain),
		},
	}
	if err := u.invitationNotifier.Notify(ctx, user.ID, payload, dispatchModel.WithEventScope(e.ID), dispatchModel.WithOverrideChannels(notificationTypes.NotificationChannelEmail)); err != nil {
		return time.Time{}, model.ErrPlatform.WithError(err).WithMessage("Failed to queue invitation email").Err()
	}
	sentAt := time.Now().UTC()
	if _, err := u.participants.MarkInvitationSent(ctx, e.ID, user.ID, sentAt); err != nil {
		return time.Time{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record invitation delivery").Err()
	}
	// A (re)sent link restarts the 30-day clock of an unconfirmed account.
	if _, err := u.users.MarkInvitationSent(ctx, user.ID, sentAt); err != nil {
		return time.Time{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record invitation delivery").Err()
	}
	return sentAt, nil
}

// invitationExpired applies the invitation validity rule: the registration
// window must be open (the registration type is irrelevant — invitations work
// while registration is closed); a team invitation also needs an open roster.
func invitationExpired(e eventModel.Event, p participantModel.Participant, now time.Time) bool {
	return !e.Lifecycle.RegistrationOpen(now) || (p.InvitedToTeam && !e.Lifecycle.RosterOpen(now))
}

// ResendParticipantInvitation re-sends a pending, unexpired invitation.
func (u *EventUseCase) ResendParticipantInvitation(ctx context.Context, eventID, userID, by uuid.UUID) (ParticipantInvitationResult, error) {
	if u.invitationNotifier == nil || u.setupTokens == nil || u.eventDomain == "" || u.idHost == "" {
		return ParticipantInvitationResult{}, model.ErrPlatform.WithMessage("Event invitation dependencies are not configured").Err()
	}
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil || !p.Invited || p.Status != participantModel.StatusPending {
		if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
			return ParticipantInvitationResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
		}
		return ParticipantInvitationResult{}, participantModel.ErrInvitationRequired.Err()
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return ParticipantInvitationResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if invitationExpired(e, p, time.Now()) {
		return ParticipantInvitationResult{}, participantModel.ErrInvitationExpired.Err()
	}
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return ParticipantInvitationResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get invited account").Err()
	}
	teamName := ""
	if p.InvitedToTeam {
		if p.InvitedTeamID == nil {
			return ParticipantInvitationResult{}, participantModel.ErrTeamInvitationUnavailable.Err()
		}
		team, teamErr := u.teams.GetByID(ctx, eventID, *p.InvitedTeamID)
		if teamErr != nil {
			if repositoryTools.IsObjectNotFoundError(teamErr) {
				return ParticipantInvitationResult{}, participantModel.ErrTeamInvitationUnavailable.Err()
			}
			return ParticipantInvitationResult{}, model.ErrPlatform.WithError(teamErr).WithMessage("Failed to get invited team").Err()
		}
		teamName = team.Name
	}
	sentAt, err := u.sendParticipantInvitation(ctx, e, user, p.InvitedToTeam, teamName, by)
	if err != nil {
		return ParticipantInvitationResult{}, err
	}
	return ParticipantInvitationResult{Email: user.Email, UserID: user.ID, SentAt: &sentAt}, nil
}

// RevokeParticipantInvitation deletes a pending invitation (moderator).
func (u *EventUseCase) RevokeParticipantInvitation(ctx context.Context, eventID, userID, by uuid.UUID) error {
	return u.removeInvitation(ctx, eventID, userID, by, signalModel.TypeParticipantInvitationRevoked)
}

// DeclineParticipantInvitation deletes the caller's pending invitation. The
// user may still register on their own while registration is open.
func (u *EventUseCase) DeclineParticipantInvitation(ctx context.Context, eventID, userID uuid.UUID) error {
	return u.removeInvitation(ctx, eventID, userID, uuid.Nil, signalModel.TypeParticipantInvitationDeclined)
}

func (u *EventUseCase) removeInvitation(ctx context.Context, eventID, userID, by uuid.UUID, signal signalModel.Type) error {
	if u.uow == nil || u.signalPublishers == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	participants := participantRepo.New(txRepo)
	invitation, err := participants.Get(txCtx, eventID, userID)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	affected, err := participants.DeleteInvitation(txCtx, eventID, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove invitation").Err()
	}
	if affected == 0 {
		return participantModel.ErrInvitationRequired.Err()
	}
	if invitation.InvitedToTeam && invitation.InvitedTeamID != nil {
		if err = handOverPendingCaptaincy(txCtx, txRepo, eventID, *invitation.InvitedTeamID, userID); err != nil {
			return err
		}
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	payload := signalModel.ParticipantPayload{ScopeEventID: eventID, ActorUserID: by, SubjectUserID: userID, EventTag: e.Tag, EventName: e.Name, Registration: signalModel.RegistrationInvitation}
	if err = u.signalPublishers(txRepo).Publish(txCtx, signal, &payload); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to publish invitation signal").Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove invitation").Err()
	}
	return nil
}

func (u *EventUseCase) AcceptParticipantInvitation(ctx context.Context, eventID, userID uuid.UUID) (JoinInfoView, error) {
	if err := u.requireParticipantForm(ctx, eventID, userID); err != nil {
		return JoinInfoView{}, err
	}
	if u.uow == nil || u.signalPublishers == nil {
		return JoinInfoView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return JoinInfoView{}, err
	}
	defer unit.Restore()
	if err = lockTeamRoster(txCtx, txRepo, eventID); err != nil {
		return JoinInfoView{}, err
	}
	participants := participantRepo.New(txRepo)
	p, err := participants.Get(txCtx, eventID, userID)
	if err != nil {
		return JoinInfoView{}, participantModel.ErrInvitationRequired.Err()
	}
	if !p.Invited || p.Status != participantModel.StatusPending {
		return JoinInfoView{}, participantModel.ErrInvitationRequired.Err()
	}
	invitedEvent, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if staff, staffErr := u.isEventStaff(txCtx, eventID, userID); staffErr != nil {
		return JoinInfoView{}, staffErr
	} else if staff {
		return JoinInfoView{}, registerError(ReasonStaff)
	}
	if invitationExpired(invitedEvent, p, time.Now()) {
		return JoinInfoView{}, participantModel.ErrInvitationExpired.Err()
	}
	if err = p.Approve(time.Now().UTC(), uuid.Nil); err != nil {
		return JoinInfoView{}, err
	}
	if affected, updateErr := participants.Update(txCtx, p); updateErr != nil {
		return JoinInfoView{}, model.ErrPlatform.WithError(updateErr).WithMessage("Failed to accept participant invitation").Err()
	} else if affected != 1 {
		return JoinInfoView{}, participantModel.ErrInvitationRequired.Err()
	}
	cfg, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if p.InvitedToTeam && (cfg.Participation == nil || *cfg.Participation != eventConfigModel.ParticipationTeam || p.InvitedTeamID == nil) {
		return JoinInfoView{}, participantModel.ErrTeamInvitationUnavailable.Err()
	}
	if cfg.Participation != nil && *cfg.Participation == eventConfigModel.ParticipationIndividual {
		if err = u.createIndividualTeamInTransaction(txCtx, txRepo, eventID, userID); err != nil {
			return JoinInfoView{}, err
		}
	} else if p.InvitedToTeam {
		teamID := *p.InvitedTeamID
		teams := eventTeamRepo.New(txRepo)
		invitedTeam, lookupErr := teams.GetByID(txCtx, eventID, teamID)
		if lookupErr != nil {
			if repositoryTools.IsObjectNotFoundError(lookupErr) {
				return JoinInfoView{}, participantModel.ErrTeamInvitationUnavailable.Err()
			}
			return JoinInfoView{}, model.ErrPlatform.WithError(lookupErr).WithMessage("Failed to get invited team").Err()
		}
		if affected, addErr := teams.TryAddMember(txCtx, eventID, teamID, cfg.MaxTeamSize, time.Now()); addErr != nil {
			return JoinInfoView{}, model.ErrPlatform.WithError(addErr).WithMessage("Failed to reserve invited team membership").Err()
		} else if affected == 0 {
			return JoinInfoView{}, eventTeamModel.ErrEventTeamFull.Err()
		}
		// A moderator may have named a pending invitee captain: accepting
		// takes that seat.
		role := participantModel.TeamRoleMember
		if invitedTeam.IsCaptain(userID) {
			role = participantModel.TeamRoleCaptain
		}
		if err = p.AssignTeam(teamID, role); err != nil {
			return JoinInfoView{}, err
		}
		if affected, assignErr := participants.AssignTeam(txCtx, p); assignErr != nil {
			return JoinInfoView{}, model.ErrPlatform.WithError(assignErr).WithMessage("Failed to assign invited team member").Err()
		} else if affected == 0 {
			return JoinInfoView{}, participantModel.ErrParticipantTeamInvalid.Err()
		}
		if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, teamID, time.Now()); err != nil {
			return JoinInfoView{}, err
		}
	}
	payload := signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID, EventTag: invitedEvent.Tag, EventName: invitedEvent.Name, Registration: signalModel.RegistrationInvitation}
	publisher := u.signalPublishers(txRepo)
	for _, typ := range []signalModel.Type{signalModel.TypeParticipantInvitationAccepted, signalModel.TypeParticipantEnrolled} {
		if err = publisher.Publish(txCtx, typ, &payload); err != nil {
			return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to publish invitation acceptance").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to accept participant invitation").Err()
	}
	return JoinInfoView{Status: participantModel.StatusApproved, Invited: true}, nil
}

// handOverPendingCaptaincy keeps a team led after its captain's invitation
// disappears: the seat goes to the first member, or else to the first
// remaining invitee (who takes it on accepting). A team with nobody left
// keeps the stale captain until a moderator deletes it.
func handOverPendingCaptaincy(ctx context.Context, repo IRepository, eventID, teamID, formerCaptainID uuid.UUID) error {
	teams, participants := eventTeamRepo.New(repo), participantRepo.New(repo)
	team, err := teams.GetByID(ctx, eventID, teamID)
	if repositoryTools.IsObjectNotFoundError(err) {
		return nil
	}
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get invited team").Err()
	}
	if !team.IsCaptain(formerCaptainID) {
		return nil
	}
	members, err := participants.TeamMembers(ctx, eventID, []uuid.UUID{teamID})
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list team members").Err()
	}
	successor, promoteMember := uuid.Nil, false
	if len(members) > 0 {
		successor, promoteMember = members[0].UserID, true
	} else {
		invited, listErr := participants.PendingTeamInvitations(ctx, eventID, []uuid.UUID{teamID})
		if listErr != nil {
			return model.ErrPlatform.WithError(listErr).WithMessage("Failed to list team invitations").Err()
		}
		if len(invited) == 0 {
			return nil
		}
		successor = invited[0].UserID
	}
	expected := team.UpdatedAt
	if err = team.TransferCaptain(successor, formerCaptainID, time.Now()); err != nil {
		return err
	}
	if affected, updateErr := teams.Update(ctx, team, expected); updateErr != nil {
		return model.ErrPlatform.WithError(updateErr).WithMessage("Failed to hand over team captaincy").Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	if promoteMember {
		if _, err = participants.SetTeamRole(ctx, eventID, successor, teamID, participantModel.TeamRoleCaptain); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to promote new team captain").Err()
		}
	}
	return nil
}

// requireTeamSeat checks, inside a transaction, that the team has a seat for one more invitee: its members plus the
// pending team invitations (which reserve seats) stay within the event's maximum.
func (u *EventUseCase) requireTeamSeat(ctx context.Context, repo IRepository, eventID, teamID uuid.UUID) error {
	config, err := eventConfigRepo.New(repo).Get(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	team, err := eventTeamRepo.New(repo).GetByID(ctx, eventID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	pending, err := participantRepo.New(repo).CountPendingTeamInvitations(ctx, eventID, teamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count pending team invitations").Err()
	}
	if config.MaxTeamSize > 0 && int64(team.MemberCount)+pending+1 > int64(config.MaxTeamSize) {
		return eventTeamModel.ErrEventTeamFull.Err()
	}
	return nil
}
