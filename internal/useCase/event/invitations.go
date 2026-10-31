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
	"github.com/rs/zerolog/log"

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
//
// noEmail creates the very same invitations but sends no email: the person
// finds the invitation on the event site and accepts it there.
func (u *EventUseCase) InviteParticipants(ctx context.Context, eventID, by uuid.UUID, entries []ParticipantInvitationInput, noEmail bool) ([]ParticipantInvitationResult, error) {
	return u.inviteParticipants(ctx, eventID, uuid.NullUUID{}, "", by, entries, noEmail)
}

func (u *EventUseCase) InviteTeamMembers(ctx context.Context, eventID, teamID, by uuid.UUID, entries []ParticipantInvitationInput, noEmail bool) ([]ParticipantInvitationResult, error) {
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
	return u.inviteParticipants(ctx, eventID, uuid.NullUUID{UUID: teamID, Valid: true}, team.Name, by, entries, noEmail)
}

func uniqueInvitationEmails(entries []ParticipantInvitationInput) map[string]struct{} {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		seen[userModel.NormalizeEmail(entry.Email)] = struct{}{}
	}
	return seen
}

func (u *EventUseCase) inviteParticipants(ctx context.Context, eventID uuid.UUID, targetTeamID uuid.NullUUID, targetTeamName string, by uuid.UUID, entries []ParticipantInvitationInput, noEmail bool) ([]ParticipantInvitationResult, error) {
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
		result.UserID, err = u.inviteParticipant(ctx, eventID, targetTeamID, targetTeamName, by, email, strings.TrimSpace(entry.FirstName), strings.TrimSpace(entry.LastName), form, entry.Fields, noEmail)
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

func (u *EventUseCase) inviteParticipant(ctx context.Context, eventID uuid.UUID, targetTeamID uuid.NullUUID, targetTeamName string, by uuid.UUID, email, firstName, lastName string, form *eventFormRepo.Version, fields map[string]any, noEmail bool) (uuid.UUID, error) {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return uuid.Nil, participantModel.ErrInvitationEmailInvalid.Err()
	}
	if err = userModel.ValidName(firstName, lastName); err != nil {
		return uuid.Nil, err
	}
	if u.uow == nil || (!noEmail && (u.invitationNotifier == nil || u.setupTokens == nil || u.eventDomain == "" || u.idHost == "")) {
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
	if noEmail {
		// No email restarts nothing by itself: the invitation time starts the
		// 30-day clock of an unconfirmed account here instead.
		if _, err = users.MarkInvitationSent(txCtx, user.ID, time.Now().UTC()); err != nil {
			return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to record invitation").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to save invitation").Err()
	}
	if noEmail {
		return user.ID, nil
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
//
// An invitation of an event that is not published yet is not expired: it
// awaits publication (see invitationAwaitingPublication) and cannot be
// accepted before it.
func invitationExpired(e eventModel.Event, p participantModel.Participant, now time.Time) bool {
	if invitationAwaitingPublication(e, now) {
		return false
	}
	return !e.Lifecycle.RegistrationOpen(now) || (p.InvitedToTeam && !e.Lifecycle.RosterOpen(now))
}

// teamClosedToInvitees: a formed team takes no more members from invitations. A team forms when its captain confirms
// it or, in an event that does not accept late joiners, at the start; from then on only an organizer changes the
// roster.
func teamClosedToInvitees(team eventTeamModel.EventTeam, e eventModel.Event, now time.Time) bool {
	return team.Formed(e.Lifecycle.FormsTeamsAtStart(now))
}

// invitationAwaitingPublication: the event is not published yet, and its join
// period (registration and roster alike) will be open at publication, so a
// pending invitation becomes acceptable then. An event without a schedule has
// no publication moment yet; its invitations await it too.
func invitationAwaitingPublication(e eventModel.Event, now time.Time) bool {
	l := e.Lifecycle
	if l.Status(now) != eventModel.LifecycleNotPublished {
		return false
	}
	return !l.Configured || l.JoinPeriodOpen(l.PublishAt)
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

// ChangeParticipantInvitationEmail corrects the address of a pending invitation: the invitation moves to the account
// of the new address (an unclaimed one is created) with its team, captaincy and prefilled answers, and a new email
// with a new setup link goes out (unless noEmail). The wrong address no longer holds an invitation, and a setup link
// it received is revoked when the account has no other event rows. The organizer is never time-limited.
func (u *EventUseCase) ChangeParticipantInvitationEmail(
	ctx context.Context,
	eventID, userID, by uuid.UUID,
	newEmail string,
	noEmail bool,
) (ParticipantInvitationResult, error) {
	email := userModel.NormalizeEmail(newEmail)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return ParticipantInvitationResult{}, participantModel.ErrInvitationEmailInvalid.Err()
	}
	mailReady := u.invitationNotifier != nil && u.setupTokens != nil && u.eventDomain != "" &&
		u.idHost != ""
	if u.uow == nil || (!noEmail && !mailReady) {
		return ParticipantInvitationResult{}, model.ErrPlatform.
			WithMessage("Event invitation dependencies are not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return ParticipantInvitationResult{}, err
	}
	defer restoreUnit(unit)
	if err = lockTeamRoster(txCtx, txRepo, eventID); err != nil {
		return ParticipantInvitationResult{}, err
	}
	move, err := u.planInvitationMove(txCtx, txRepo, eventID, userID, email)
	if err != nil {
		return ParticipantInvitationResult{}, err
	}
	if move.sameAddress {
		// Nothing to correct: the same address just gets the invitation again.
		return u.ResendParticipantInvitation(ctx, eventID, userID, by)
	}
	if err = u.moveInvitation(txCtx, txRepo, &move, by, noEmail); err != nil {
		return ParticipantInvitationResult{}, err
	}
	if err = unit.Save(); err != nil {
		return ParticipantInvitationResult{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to save invitation").
			Err()
	}
	if move.orphan {
		u.revokeSetupLink(ctx, eventID, userID)
	}
	result := ParticipantInvitationResult{Email: move.user.Email, UserID: move.user.ID}
	if noEmail {
		return result, nil
	}
	sentAt, err := u.sendParticipantInvitation(
		ctx,
		move.event,
		move.user,
		move.target.Valid,
		move.teamName,
		by,
	)
	if err != nil {
		return result, err
	}
	result.SentAt = &sentAt
	return result, nil
}

// invitationMove is what an email correction moves: the old invitation, the account it leaves, the account that
// receives it and the team it points to.
type invitationMove struct {
	newEmail    string
	userID      uuid.UUID
	sameAddress bool
	old         participantModel.Participant
	oldUser     userModel.User
	user        userModel.User
	event       eventModel.Event
	team        eventTeamModel.EventTeam
	target      uuid.NullUUID
	teamName    string
	orphan      bool
}

// planInvitationMove reads the pending invitation and finds or creates the account of the new address, refusing
// what cannot be invited.
func (u *EventUseCase) planInvitationMove(
	ctx context.Context,
	repo IRepository,
	eventID, userID uuid.UUID,
	email string,
) (invitationMove, error) {
	move := invitationMove{userID: userID, newEmail: email}
	participants, users := participantRepo.New(repo), userRepo.New(repo)
	old, err := participants.Get(ctx, eventID, userID)
	if err != nil || !old.Invited || old.Status != participantModel.StatusPending {
		if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
			return move, model.ErrPlatform.WithError(err).
				WithMessage("Failed to get participant").
				Err()
		}
		return move, participantModel.ErrInvitationRequired.Err()
	}
	move.old = old
	if move.oldUser, err = users.GetByID(ctx, userID); err != nil {
		return move, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get invited account").
			Err()
	}
	if move.oldUser.Email == email {
		move.sameAddress = true
		return move, nil
	}
	if move.event, err = eventRepo.New(repo).GetByID(ctx, eventID); err != nil {
		return move, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if old.InvitedToTeam {
		if err = u.loadInvitedTeam(ctx, repo, eventID, &move); err != nil {
			return move, err
		}
	}
	user, err := users.GetByEmail(ctx, email)
	if repositoryTools.IsObjectNotFoundError(err) || errors.Is(err, pgx.ErrNoRows) {
		pending := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, time.Now().UTC())
		if move.oldUser.Status == userModel.UserStatusIncomplete {
			pending.FirstName, pending.LastName = move.oldUser.FirstName, move.oldUser.LastName
		}
		user, err = users.Create(ctx, pending)
	}
	if err != nil {
		return move, model.ErrPlatform.WithError(err).
			WithMessage("Failed to prepare invited account").
			Err()
	}
	if user.Status != userModel.UserStatusActive && user.Status != userModel.UserStatusIncomplete {
		return move, participantModel.ErrInvitationRequired.WithMessage("This account cannot be invited").
			Err()
	}
	if staff, staffErr := u.isEventStaff(ctx, eventID, user.ID); staffErr != nil {
		return move, staffErr
	} else if staff {
		return move, registerError(ReasonStaff)
	}
	move.user = user
	return move, nil
}

func (u *EventUseCase) loadInvitedTeam(
	ctx context.Context,
	repo IRepository,
	eventID uuid.UUID,
	move *invitationMove,
) error {
	if move.old.InvitedTeamID == nil {
		return participantModel.ErrTeamInvitationUnavailable.Err()
	}
	team, err := eventTeamRepo.New(repo).GetByID(ctx, eventID, *move.old.InvitedTeamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrTeamInvitationUnavailable.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get invited team").Err()
	}
	move.team, move.target, move.teamName = team, uuid.NullUUID{
		UUID:  team.ID,
		Valid: true,
	}, team.Name
	return nil
}

// moveInvitation rewrites the invitation rows for the new account: delete the old one, invite the new one, carry
// the prefilled answers and a pending captaincy over, and note whether the old account is left without any row.
func (u *EventUseCase) moveInvitation(
	ctx context.Context,
	repo IRepository,
	move *invitationMove,
	by uuid.UUID,
	noEmail bool,
) error {
	eventID, userID := move.event.ID, move.userID
	participants, forms := participantRepo.New(repo), eventFormRepo.New(repo)
	prefill, err := forms.LatestRegistrationAnswers(ctx, eventID, []uuid.UUID{userID})
	if err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to get participant answers").
			Err()
	}
	form, err := prefillForm(ctx, forms, eventID)
	if err != nil {
		return err
	}
	if affected, delErr := participants.DeleteInvitation(ctx, eventID, userID); delErr != nil {
		return model.ErrPlatform.WithError(delErr).WithMessage("Failed to remove invitation").Err()
	} else if affected == 0 {
		return participantModel.ErrInvitationRequired.Err()
	}
	if _, ok, inviteErr := participants.Invite(
		ctx,
		eventID,
		move.user.ID,
		by,
		move.target,
		time.Now().UTC(),
	); inviteErr != nil {
		return model.ErrPlatform.WithError(inviteErr).
			WithMessage("Failed to create participant invitation").
			Err()
	} else if !ok {
		return participantModel.ErrAlreadyParticipant.Err()
	}
	if answers := prefill[userID]; len(answers) > 0 && form != nil {
		if err = savePrefilledAnswers(
			ctx,
			forms,
			eventID,
			move.user.ID,
			*form,
			answers,
			time.Now().UTC(),
		); err != nil {
			return err
		}
	}
	if move.target.Valid && move.team.IsCaptain(userID) {
		if err = moveCaptaincy(ctx, repo, move.team, userID, move.user.ID); err != nil {
			return err
		}
	}
	if noEmail {
		if _, err = userRepo.New(repo).
			MarkInvitationSent(ctx, move.user.ID, time.Now().UTC()); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to record invitation").Err()
		}
	}
	if move.oldUser.Status == userModel.UserStatusIncomplete {
		rows, countErr := participants.CountRowsOfUser(ctx, userID)
		if countErr != nil {
			return model.ErrPlatform.WithError(countErr).
				WithMessage("Failed to count participant rows").
				Err()
		}
		move.orphan = rows == 0
	}
	return nil
}

// moveCaptaincy hands a pending captain's seat to the account the invitation moved to.
func moveCaptaincy(
	ctx context.Context,
	repo IRepository,
	team eventTeamModel.EventTeam,
	from, to uuid.UUID,
) error {
	expected := team.UpdatedAt
	if err := team.TransferCaptain(to, from, time.Now()); err != nil {
		return err
	}
	affected, err := eventTeamRepo.New(repo).Update(ctx, team, expected)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to move team captaincy").Err()
	}
	if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	return nil
}

// revokeSetupLink kills the setup link the wrong address received, when the setup-link store can.
func (u *EventUseCase) revokeSetupLink(ctx context.Context, eventID, userID uuid.UUID) {
	revoker, ok := u.setupTokens.(interface {
		Revoke(ctx context.Context, userID uuid.UUID) error
	})
	if !ok {
		return
	}
	if err := revoker.Revoke(ctx, userID); err != nil {
		log.Warn().
			Err(err).
			Stringer("event_id", eventID).
			Msg("invitation email change: the old setup link was not revoked")
	}
}

// restoreUnit ends a unit of work that was not saved; a failed rollback is logged.
func restoreUnit(unit interface{ Restore() error }) {
	if err := unit.Restore(); err != nil {
		log.Warn().Err(err).Msg("unit of work: rollback failed")
	}
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
	return u.acceptParticipantInvitation(ctx, eventID, userID, uuid.Nil)
}

// AcceptParticipantInvitationFor accepts a pending invitation on behalf of the invited person (organizer). The
// organizer is never time-limited: the registration window, the publication and a formed team do not stop it, and
// the participant form may stay incomplete (the missing answers stay visible in the list). The person still has to
// fit the team size and cannot be event staff.
func (u *EventUseCase) AcceptParticipantInvitationFor(
	ctx context.Context,
	eventID, userID, by uuid.UUID,
) (JoinInfoView, error) {
	if by == uuid.Nil {
		return JoinInfoView{}, participantModel.ErrParticipantAccessForbidden.Err()
	}
	return u.acceptParticipantInvitation(ctx, eventID, userID, by)
}

// acceptParticipantInvitation accepts the invitation; by is the organizer accepting on behalf, uuid.Nil: the
// invited person themselves.
func (u *EventUseCase) acceptParticipantInvitation(
	ctx context.Context,
	eventID, userID, by uuid.UUID,
) (JoinInfoView, error) {
	onBehalf := by != uuid.Nil
	if !onBehalf {
		if err := u.requireParticipantForm(ctx, eventID, userID); err != nil {
			return JoinInfoView{}, err
		}
	}
	if u.uow == nil || u.signalPublishers == nil {
		return JoinInfoView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return JoinInfoView{}, err
	}
	defer restoreUnit(unit)
	if err = lockTeamRoster(txCtx, txRepo, eventID); err != nil {
		return JoinInfoView{}, err
	}
	participants := participantRepo.New(txRepo)
	p, invitedEvent, err := u.pendingInvitationOf(txCtx, txRepo, eventID, userID)
	if err != nil {
		return JoinInfoView{}, err
	}
	if !onBehalf {
		if err = requireAcceptable(invitedEvent, p, time.Now()); err != nil {
			return JoinInfoView{}, err
		}
	}
	if err = p.Approve(time.Now().UTC(), by); err != nil {
		return JoinInfoView{}, err
	}
	if affected, updateErr := participants.Update(txCtx, p); updateErr != nil {
		return JoinInfoView{}, model.ErrPlatform.WithError(updateErr).WithMessage("Failed to accept participant invitation").Err()
	} else if affected != 1 {
		return JoinInfoView{}, participantModel.ErrInvitationRequired.Err()
	}
	if err = u.seatAcceptedInvitee(txCtx, txRepo, invitedEvent, p, by); err != nil {
		return JoinInfoView{}, err
	}
	if err = unit.Save(); err != nil {
		return JoinInfoView{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to accept participant invitation").
			Err()
	}
	return JoinInfoView{Status: participantModel.StatusApproved, Invited: true}, nil
}

// seatAcceptedInvitee places the person who just accepted: the single-member team of an individual event, the
// invited team of a team event, and the signals of the acceptance.
func (u *EventUseCase) seatAcceptedInvitee(
	ctx context.Context,
	repo IRepository,
	e eventModel.Event,
	p participantModel.Participant,
	by uuid.UUID,
) error {
	cfg, err := eventConfigRepo.New(repo).Get(ctx, e.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if p.InvitedToTeam && (cfg.Participation == nil || *cfg.Participation != eventConfigModel.ParticipationTeam || p.InvitedTeamID == nil) {
		return participantModel.ErrTeamInvitationUnavailable.Err()
	}
	if cfg.Participation != nil && *cfg.Participation == eventConfigModel.ParticipationIndividual {
		if err = u.createIndividualTeamInTransaction(ctx, repo, e.ID, p.UserID); err != nil {
			return err
		}
	} else if p.InvitedToTeam {
		if err = u.joinInvitedTeam(ctx, repo, e, p, cfg.MaxTeamSize, by != uuid.Nil); err != nil {
			return err
		}
	}
	payload := signalModel.ParticipantPayload{
		ScopeEventID:  e.ID,
		ActorUserID:   by,
		SubjectUserID: p.UserID,
		EventTag:      e.Tag,
		EventName:     e.Name,
		Registration:  signalModel.RegistrationInvitation,
	}
	publisher := u.signalPublishers(repo)
	for _, typ := range []signalModel.Type{signalModel.TypeParticipantInvitationAccepted, signalModel.TypeParticipantEnrolled} {
		if err = publisher.Publish(ctx, typ, &payload); err != nil {
			return model.ErrPlatform.WithError(err).
				WithMessage("Failed to publish invitation acceptance").
				Err()
		}
	}
	return nil
}

// pendingInvitationOf reads the pending invitation of a person and its event; event staff cannot accept one.
func (u *EventUseCase) pendingInvitationOf(
	ctx context.Context,
	repo IRepository,
	eventID, userID uuid.UUID,
) (participantModel.Participant, eventModel.Event, error) {
	p, err := participantRepo.New(repo).Get(ctx, eventID, userID)
	if err != nil || !p.Invited || p.Status != participantModel.StatusPending {
		return p, eventModel.Event{}, participantModel.ErrInvitationRequired.Err()
	}
	e, err := eventRepo.New(repo).GetByID(ctx, eventID)
	if err != nil {
		return p, e, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if staff, staffErr := u.isEventStaff(ctx, eventID, userID); staffErr != nil {
		return p, e, staffErr
	} else if staff {
		return p, e, registerError(ReasonStaff)
	}
	return p, e, nil
}

// requireAcceptable is the invitee's own time limit: not before the publication, not after the join period.
func requireAcceptable(e eventModel.Event, p participantModel.Participant, now time.Time) error {
	if invitationAwaitingPublication(e, now) {
		// Accepting before publication stays impossible.
		return registerError(ReasonNotPublished)
	}
	if invitationExpired(e, p, now) {
		return participantModel.ErrInvitationExpired.Err()
	}
	return nil
}

// joinInvitedTeam seats the accepting invitee in the team they were invited to. A closed (formed) team refuses the
// invitee themselves, never an organizer.
func (u *EventUseCase) joinInvitedTeam(
	ctx context.Context,
	repo IRepository,
	e eventModel.Event,
	p participantModel.Participant,
	maxTeamSize int32,
	onBehalf bool,
) error {
	teamID := *p.InvitedTeamID
	teams, participants := eventTeamRepo.New(repo), participantRepo.New(repo)
	invitedTeam, err := teams.GetByID(ctx, e.ID, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrTeamInvitationUnavailable.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get invited team").Err()
	}
	if !onBehalf && teamClosedToInvitees(invitedTeam, e, time.Now()) {
		// The captain marked the team formed (or the event formed it at the start): its unaccepted invitations
		// are expired; only an organizer changes the roster now.
		return participantModel.ErrInvitationExpired.Err()
	}
	if affected, addErr := teams.TryAddMember(
		ctx,
		e.ID,
		teamID,
		maxTeamSize,
		time.Now(),
	); addErr != nil {
		return model.ErrPlatform.WithError(addErr).
			WithMessage("Failed to reserve invited team membership").
			Err()
	} else if affected == 0 {
		return eventTeamModel.ErrEventTeamFull.Err()
	}
	// A moderator may have named a pending invitee captain: accepting takes that seat.
	role := participantModel.TeamRoleMember
	if invitedTeam.IsCaptain(p.UserID) {
		role = participantModel.TeamRoleCaptain
	}
	if err = p.AssignTeam(teamID, role); err != nil {
		return err
	}
	if affected, assignErr := participants.AssignTeam(ctx, p); assignErr != nil {
		return model.ErrPlatform.WithError(assignErr).
			WithMessage("Failed to assign invited team member").
			Err()
	} else if affected == 0 {
		return participantModel.ErrParticipantTeamInvalid.Err()
	}
	return u.requestLabAccessSyncInTransaction(ctx, repo, teamID, time.Now())
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
