package event

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/mailRepo"
	"github.com/cybericebox/daemon/internal/model"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

// kyiv formats participant-facing times; the platform audience is Ukrainian.
var kyiv = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return time.FixedZone("EET", 2*60*60)
	}
	return loc
}()

func kyivTime(t time.Time) string { return t.In(kyiv).Format("02.01.2006 15:04") }

// RunEventMailNotices is the event_mail job pass: start reminders, finish
// notices and expired invitations. Each notice publishes its signal and
// records its state in one transaction, so a pass is idempotent and a failed
// Event is retried by the next pass.
func (u *EventUseCase) RunEventMailNotices(ctx context.Context) error {
	if u.uow == nil || u.signalPublishers == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	now := time.Now().UTC()
	return errors.Join(
		u.emitStartReminders(ctx, now),
		u.emitFinishedNotices(ctx, now),
		u.emitExpiredInvitations(ctx, now),
	)
}

func (u *EventUseCase) eventURL(tag string) string {
	return fmt.Sprintf("https://%s.%s/", tag, u.eventDomain)
}

func (u *EventUseCase) emitStartReminders(ctx context.Context, now time.Time) error {
	due, err := u.mail.DueStartReminders(ctx, now)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list due start reminders").Err()
	}
	var errs []error
	for _, e := range due {
		hours := int(e.At.Sub(now).Round(time.Hour) / time.Hour)
		payload := signalModel.EventNoticePayload{
			ScopeEventID: e.ID, EventTag: e.Tag, EventName: e.Name, EventURL: u.eventURL(e.Tag),
			StartAt: kyivTime(e.At), HoursLeft: max(hours, 1),
		}
		errs = append(errs, u.publishNotice(ctx, signalModel.TypeParticipantEventStartReminder, &payload, func(txCtx context.Context, repo *mailRepo.Repository) error {
			return repo.MarkStartReminderSent(txCtx, e.ID, e.At, now)
		}))
	}
	return errors.Join(errs...)
}

func (u *EventUseCase) emitFinishedNotices(ctx context.Context, now time.Time) error {
	due, err := u.mail.DueFinishedNotices(ctx, now)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list due finish notices").Err()
	}
	var errs []error
	for _, e := range due {
		payload := signalModel.EventNoticePayload{
			ScopeEventID: e.ID, EventTag: e.Tag, EventName: e.Name, EventURL: u.eventURL(e.Tag),
			FinishAt: kyivTime(e.At),
		}
		errs = append(errs, u.publishNotice(ctx, signalModel.TypeParticipantEventFinished, &payload, func(txCtx context.Context, repo *mailRepo.Repository) error {
			return repo.MarkFinishedNotified(txCtx, e.ID, e.At, now)
		}))
	}
	return errors.Join(errs...)
}

// emitExpiredInvitations announces each pending invitation once, when the
// invitation rule (invitationExpired) says it can no longer be accepted.
func (u *EventUseCase) emitExpiredInvitations(ctx context.Context, now time.Time) error {
	candidates, err := u.mail.InvitationExpiryCandidates(ctx, now)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list invitation expiry candidates").Err()
	}
	var errs []error
	for _, c := range candidates {
		e, err := u.events.GetByID(ctx, c.EventID)
		if err != nil {
			errs = append(errs, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err())
			continue
		}
		if !invitationExpired(e, participantModel.Participant{InvitedToTeam: c.InvitedToTeam}, now) {
			continue
		}
		payload := signalModel.ParticipantPayload{
			ScopeEventID: e.ID, SubjectUserID: c.UserID, EventTag: e.Tag, EventName: e.Name,
			Registration: signalModel.RegistrationInvitation,
		}
		errs = append(errs, u.publishNotice(ctx, signalModel.TypeParticipantInvitationExpired, &payload, func(txCtx context.Context, repo *mailRepo.Repository) error {
			claimed, err := repo.MarkInvitationExpiredNotified(txCtx, c.EventID, c.UserID, now)
			if err == nil && !claimed {
				return errAlreadyNotified
			}
			return err
		}))
	}
	return errors.Join(errs...)
}

// errAlreadyNotified rolls back a notice another pass already recorded.
var errAlreadyNotified = errors.New("notice already recorded")

// publishNotice publishes one signal and records its state in the same
// transaction.
func (u *EventUseCase) publishNotice(ctx context.Context, typ signalModel.Type, payload signalModel.Payload, record func(context.Context, *mailRepo.Repository) error) error {
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if err = record(txCtx, mailRepo.New(txRepo)); err != nil {
		if errors.Is(err, errAlreadyNotified) {
			return nil
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record mail notice").Err()
	}
	if err = u.signalPublishers(txRepo).Publish(txCtx, typ, payload); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to publish mail notice").Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save mail notice").Err()
	}
	log.Info().Str("signal", string(typ)).Str("event_id", payload.Routing().ScopeEventID.String()).Msg("Mail notice published")
	return nil
}
