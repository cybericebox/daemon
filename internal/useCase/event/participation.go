package event

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// JoinEvent records a self-service join attempt. The registration window is
// derived solely from the event lifecycle; EventConfig.Registration decides
// only whether joining is closed, immediate, or approval-based. Route gate:
// PermSelf.
//
// Re-joining after a Rejected decision is allowed and returns the current
// (still Rejected) status without an error — a rejected participant is not
// "blocked", just not (yet) approved; only a live Pending/Approved row
// counts as "already participating". Moderators can still re-approve a
// rejected row directly if the product wants a manual override path later.
func (u *EventUseCase) JoinEvent(ctx context.Context, eventID, userID uuid.UUID) (JoinInfoView, error) {
	now := time.Now()
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return JoinInfoView{}, eventModel.ErrEventNotFound.Err()
		}
		return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	// GetEventConfig applies the same lazy-create self-heal as every other
	// config read, so JoinEvent never fails just because the config insert
	// hasn't landed yet.
	cfg, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return JoinInfoView{}, err
	}
	staff, err := u.isEventStaff(ctx, eventID, userID)
	if err != nil {
		return JoinInfoView{}, err
	}
	// Status is left at none: an existing row is resolved by the upsert below
	// (rejected re-joins keep returning their status), the rest is the shared
	// participation rule.
	state := ComputeParticipation(ParticipationInput{
		Lifecycle: e.Lifecycle, Registration: cfg.Registration, Participation: cfg.Participation, Now: now,
		Actor: ParticipationActor{Authenticated: true, Staff: staff},
	})
	if err = registerError(state.Register.Reason); err != nil {
		return JoinInfoView{}, err
	}
	p, err := participantModel.NewParticipant(eventID, userID, cfg.Registration, now)
	if err != nil {
		return JoinInfoView{}, err
	}
	if err = u.requireParticipantForm(ctx, eventID, userID); err != nil {
		return JoinInfoView{}, err
	}
	createSolo := cfg.Participation != nil && *cfg.Participation == eventConfigModel.ParticipationIndividual && p.Status == participantModel.StatusApproved
	created, ok, err := u.joinParticipant(ctx, p, e, cfg.Registration, createSolo)
	if err != nil {
		return JoinInfoView{}, err
	}
	if ok {
		if createSolo && u.signalPublishers == nil {
			if err = u.createIndividualTeam(ctx, eventID, userID); err != nil {
				return JoinInfoView{}, err
			}
		}
		return JoinInfoView{Status: created.Status}, nil
	}
	// A row already existed for (eventID, userID) — re-read it to decide
	// what "already exists" means for this participant.
	existing, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get existing participant").Err()
	}
	if existing.Status == participantModel.StatusPending || existing.Status == participantModel.StatusApproved {
		return JoinInfoView{}, participantModel.ErrAlreadyParticipant.Err()
	}
	return JoinInfoView{Status: existing.Status}, nil
}

// registerError turns a blocked Register capability into the API error. The
// reasons that only exist for a viewer with a participant row never reach it.
func registerError(reason ParticipationReason) error {
	switch reason {
	case ReasonStaff:
		return participantModel.ErrStaffCannotParticipate.Err()
	case ReasonClosedAtStart:
		return participantModel.ErrRegistrationAfterStartForbidden.Err()
	case ReasonNotPublished, ReasonFinished, ReasonWithdrawn:
		return participantModel.ErrRegistrationNotOpen.Err()
	}
	// ReasonRegistrationClosed is raised by NewParticipant (registration mode).
	return nil
}

// isEventStaff reports whether the user holds any event-local management
// membership (owner, manager or viewer): staff run the event and never
// compete in it; the hidden moderators team is how they try it out.
func (u *EventUseCase) isEventStaff(ctx context.Context, eventID, userID uuid.UUID) (bool, error) {
	_, err := u.managers.Get(ctx, eventID, userID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, eventManagerModel.ErrEventManagerNotFound.Err()) || repositoryTools.IsObjectNotFoundError(err) || errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, model.ErrPlatform.WithError(err).WithMessage("Failed to check event staff membership").Err()
}

func (u *EventUseCase) requireParticipantForm(ctx context.Context, eventID, userID uuid.UUID) error {
	form, err := u.forms.Latest(ctx, eventID)
	if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	if !form.Form.BlocksParticipation(false) {
		return nil
	}
	answer, err := u.forms.Answer(ctx, eventID, userID, form.ID)
	if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
		return participantModel.ErrParticipantFormRequired.Err()
	}
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form answer").Err()
	}
	// An organizer's prefill (CSV import) may leave required fields empty:
	// the person still has to complete them.
	if !participantFormComplete(form.Form, answer.Values) {
		return participantModel.ErrParticipantFormRequired.Err()
	}
	return nil
}

// joinParticipant makes participant creation and its durable signals one
// transaction whenever the signal runtime is wired. The direct path remains
// for intentionally narrow, signal-free use cases.
func (u *EventUseCase) joinParticipant(
	ctx context.Context,
	participant participantModel.Participant,
	e eventModel.Event,
	registration eventConfigModel.Registration,
	createSolo bool,
) (participantModel.Participant, bool, error) {
	if u.signalPublishers == nil {
		created, ok, err := u.participants.Upsert(ctx, participant)
		if err != nil {
			return participantModel.Participant{}, false, model.ErrPlatform.WithError(err).WithMessage("Failed to join event").Err()
		}
		return created, ok, nil
	}
	if u.uow == nil {
		return participantModel.Participant{}, false, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return participantModel.Participant{}, false, err
	}
	defer unit.Restore()
	created, ok, err := participantRepo.New(txRepo).Upsert(txCtx, participant)
	if err != nil {
		return participantModel.Participant{}, false, model.ErrPlatform.WithError(err).WithMessage("Failed to join event").Err()
	}
	if ok {
		if createSolo {
			if err := u.createIndividualTeamInTransaction(txCtx, txRepo, e.ID, created.UserID); err != nil {
				return participantModel.Participant{}, false, err
			}
		}
		if err := u.publishParticipantJoinSignals(txCtx, txRepo, e, created, registration); err != nil {
			return participantModel.Participant{}, false, err
		}
	}
	if err := unit.Save(); err != nil {
		return participantModel.Participant{}, false, model.ErrPlatform.WithError(err).WithMessage("Failed to join event").Err()
	}
	return created, ok, nil
}

func (u *EventUseCase) publishParticipantJoinSignals(
	ctx context.Context,
	repo IRepository,
	e eventModel.Event,
	participant participantModel.Participant,
	registration eventConfigModel.Registration,
) error {
	publisher := u.signalPublishers(repo)
	if publisher == nil {
		return model.ErrPlatform.WithMessage("Event signal publisher is not configured").Err()
	}
	kind := signalModel.RegistrationOpen
	types := []signalModel.Type{signalModel.TypeParticipantOpenRegistrationCompleted, signalModel.TypeParticipantEnrolled}
	if registration == eventConfigModel.RegistrationApproval {
		kind = signalModel.RegistrationApproval
		types = []signalModel.Type{signalModel.TypeParticipantApprovalRegistrationSubmitted}
	}
	payload := signalModel.ParticipantPayload{
		ScopeEventID: e.ID, SubjectUserID: participant.UserID,
		EventTag: e.Tag, EventName: e.Name, Registration: kind,
	}
	for _, typ := range types {
		payloadCopy := payload
		if err := publisher.Publish(ctx, typ, &payloadCopy); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to publish participant signal").Err()
		}
	}
	return nil
}

// GetJoinInfo returns the caller's participation status for an event. No row
// is a valid state (never joined), reported as StatusNone rather than an
// error. Route gate: PermSelf.
func (u *EventUseCase) GetJoinInfo(ctx context.Context, eventID, userID uuid.UUID) (JoinInfoView, error) {
	view, err := u.joinInfo(ctx, eventID, userID)
	if err != nil {
		return JoinInfoView{}, err
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return JoinInfoView{}, eventModel.ErrEventNotFound.Err()
		}
		return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	cfg, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return JoinInfoView{}, err
	}
	staff, err := u.isEventStaff(ctx, eventID, userID)
	if err != nil {
		return JoinInfoView{}, err
	}
	var minTeamSize int32 = 1
	if view.TeamID != nil {
		team, teamErr := u.teams.GetByID(ctx, eventID, *view.TeamID)
		if teamErr != nil && !repositoryTools.IsObjectNotFoundError(teamErr) {
			return JoinInfoView{}, model.ErrPlatform.WithError(teamErr).WithMessage("Failed to get team").Err()
		}
		if teamErr == nil {
			view.teamFormed = team.Formed(e.Lifecycle.FormsTeamsAtStart(time.Now()))
			view.teamMembers = team.MemberCount
		}
		if minimum, minErr := u.teams.MinTeamSize(ctx, eventID); minErr == nil {
			minTeamSize = minimum
		}
	}
	state := ComputeParticipation(ParticipationInput{
		Lifecycle: e.Lifecycle, Registration: cfg.Registration, Participation: cfg.Participation, Now: time.Now(),
		MinTeamSize: minTeamSize,
		Actor: ParticipationActor{Authenticated: true, Staff: staff, Status: view.Status, HasTeam: view.TeamID != nil, Captain: view.teamCaptain,
			TeamFormed: view.teamFormed, TeamMembers: view.teamMembers},
	})
	view.Participation = &state
	return view, nil
}

// joinInfo is the caller's stored participation (status, invitation, team).
func (u *EventUseCase) joinInfo(ctx context.Context, eventID, userID uuid.UUID) (JoinInfoView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return JoinInfoView{Status: participantModel.StatusNone}, nil
		}
		return JoinInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	view := JoinInfoView{Status: p.Status, Invited: p.Invited, InvitedTeamID: p.InvitedTeamID, TeamID: p.TeamID}
	view.teamCaptain = p.TeamRole != nil && *p.TeamRole == participantModel.TeamRoleCaptain
	if p.Invited && p.Status == participantModel.StatusPending {
		e, eventErr := u.events.GetByID(ctx, eventID)
		if eventErr != nil {
			return JoinInfoView{}, model.ErrPlatform.WithError(eventErr).WithMessage("Failed to get event").Err()
		}
		view.InvitationExpired = invitationExpired(e, p, time.Now())
	}
	if p.Status == participantModel.StatusPending && p.InvitedToTeam {
		if p.InvitedTeamID == nil {
			view.TeamUnavailable = true
			return view, nil
		}
		team, lookupErr := u.teams.GetByID(ctx, eventID, *p.InvitedTeamID)
		if repositoryTools.IsObjectNotFoundError(lookupErr) {
			view.TeamUnavailable = true
			return view, nil
		}
		if lookupErr != nil {
			return JoinInfoView{}, model.ErrPlatform.WithError(lookupErr).WithMessage("Failed to get invited team").Err()
		}
		view.InvitedTeamName = team.Name
	}
	return view, nil
}

// ListParticipants returns a keyset page of an event's participants,
// optionally narrowed to one status and/or moderation tab (kind). Mirrors
// ListEvents' cursor style: the cursor UUID is a participant's userID,
// resolved back to its (createdAt, userID) keyset position via a Get; an
// unresolved cursor (deleted row, or zero UUID) silently falls back to the
// first page. Profiles, team names and registration answers are loaded in
// batch (no per-row queries). Route gate: events.read.
func (u *EventUseCase) ListParticipants(ctx context.Context, f ListParticipantsFilter) (ParticipantsListResult, error) {
	limit := f.PageSize
	if limit <= 0 || limit > pagination.MaxPageSize {
		limit = pagination.DefaultPageSize
	}
	if !f.Kind.Valid() {
		return ParticipantsListResult{}, participantModel.ErrParticipantKindInvalid.Err()
	}
	statusFilter := int32(-1)
	if f.Status != nil {
		statusFilter = int32(*f.Status)
	}
	curTs, curID := cursorSentinelTime, maxUUID
	if f.Cursor != uuid.Nil {
		if row, err := u.participants.Get(ctx, f.EventID, f.Cursor); err == nil {
			curTs, curID = row.CreatedAt, row.UserID
		}
	}
	rows, err := u.participants.List(ctx, f.EventID, statusFilter, f.Kind, f.Search, answerFiltersJSON(f.Fields), curTs, curID, int32(limit+1))
	if err != nil {
		return ParticipantsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list participants").Err()
	}
	hasMore := false
	if len(rows) > limit {
		hasMore = true
		rows = rows[:limit]
	}
	items, err := u.participantViews(ctx, f.EventID, rows)
	if err != nil {
		return ParticipantsListResult{}, err
	}
	next := uuid.Nil
	if hasMore && len(rows) > 0 {
		next = rows[len(rows)-1].Participant.UserID
	}
	total, err := u.participants.CountMatching(ctx, f.EventID, statusFilter, f.Kind, f.Search, answerFiltersJSON(f.Fields))
	if err != nil {
		return ParticipantsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count participants").Err()
	}
	counts, err := u.participants.CountKinds(ctx, f.EventID)
	if err != nil {
		return ParticipantsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count participant tabs").Err()
	}
	return ParticipantsListResult{
		Participants: items, NextCursor: next, HasMore: hasMore, Total: total,
		Counts: ParticipantCountsView{Participants: counts.Participants, Applications: counts.Applications, Invitations: counts.Invitations},
	}, nil
}

// SetIndividualParticipantHidden presents the solo competitor as a participant
// in the manager UI while changing visibility on the actual competitive unit.
func (u *EventUseCase) SetIndividualParticipantHidden(ctx context.Context, eventID, userID uuid.UUID, hidden bool) error {
	cfg, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return err
	}
	if cfg.Participation == nil || *cfg.Participation != eventConfigModel.ParticipationIndividual {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	person, err := participantRepo.New(txRepo).Get(txCtx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	if person.Status != participantModel.StatusApproved || person.TeamID == nil {
		return participantModel.ErrParticipantNotApproved.Err()
	}
	teams := eventTeamRepo.New(txRepo)
	team, err := teams.GetByID(txCtx, eventID, *person.TeamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get personal team").Err()
	}
	if team.CaptainID != userID || team.MemberCount != 1 {
		return eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	expected := team.UpdatedAt
	hiddenChanged := team.Hidden != hidden
	if err = team.UpdateByModerator(team.Name, hidden, time.Now()); err != nil {
		return err
	}
	affected, err := teams.Update(txCtx, team, expected)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update personal team").Err()
	}
	if affected == 0 {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	if hiddenChanged {
		if err = recordScoreboardRecalculation(txCtx, txRepo, eventID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save personal team visibility").Err()
	}
	return nil
}

// ApproveParticipant transitions a pending request to approved. Route gate:
// events.write (or a dedicated moderation permission).
func (u *EventUseCase) ApproveParticipant(ctx context.Context, eventID, userID, by uuid.UUID) error {
	if err := u.decideParticipantWithSignal(ctx, eventID, userID, by, signalModel.TypeParticipantApprovalRegistrationApproved, true, func(p *participantModel.Participant, now time.Time) error {
		if p.Invited {
			return participantModel.ErrInvitationRequired.Err()
		}
		return p.Approve(now, by)
	}); err != nil {
		return err
	}
	// With signals configured, the solo team is created in the same transaction
	// as the approval. The direct path is retained for signal-free use cases.
	if u.signalPublishers != nil {
		return nil
	}
	cfg, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return err
	}
	if cfg.Participation == nil || *cfg.Participation != eventConfigModel.ParticipationIndividual {
		return nil
	}
	return u.createIndividualTeam(ctx, eventID, userID)
}

// RejectParticipant transitions a pending request to rejected. Route gate:
// events.write (or a dedicated moderation permission).
func (u *EventUseCase) RejectParticipant(ctx context.Context, eventID, userID, by uuid.UUID) error {
	return u.decideParticipantWithSignal(ctx, eventID, userID, by, signalModel.TypeParticipantApprovalRegistrationRejected, false, func(p *participantModel.Participant, now time.Time) error {
		return p.Reject(now, by)
	})
}

func (u *EventUseCase) decideParticipantWithSignal(
	ctx context.Context,
	eventID, userID, by uuid.UUID,
	decisionType signalModel.Type,
	enroll bool,
	decide func(*participantModel.Participant, time.Time) error,
) error {
	if u.signalPublishers == nil {
		return u.decideParticipant(ctx, eventID, userID, decide)
	}
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	participants := participantRepo.New(txRepo)
	p, err := participants.Get(txCtx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	if err = decide(&p, time.Now()); err != nil {
		return err
	}
	affected, err := participants.Update(txCtx, p)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update participant").Err()
	}
	if affected == 0 {
		return participantModel.ErrParticipantNotFound.Err()
	}
	if enroll {
		config, configErr := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
		if configErr != nil {
			return model.ErrPlatform.WithError(configErr).WithMessage("Failed to get event config").Err()
		}
		if config.Participation != nil && *config.Participation == eventConfigModel.ParticipationIndividual {
			if err = u.createIndividualTeamInTransaction(txCtx, txRepo, eventID, userID); err != nil {
				return err
			}
		}
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event for participant signal").Err()
	}
	publisher := u.signalPublishers(txRepo)
	if publisher == nil {
		return model.ErrPlatform.WithMessage("Event signal publisher is not configured").Err()
	}
	payload := signalModel.ParticipantPayload{
		ScopeEventID: e.ID, ActorUserID: by, SubjectUserID: userID,
		EventTag: e.Tag, EventName: e.Name, Registration: signalModel.RegistrationApproval,
	}
	if err = publisher.Publish(txCtx, decisionType, &payload); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to publish participant signal").Err()
	}
	if enroll {
		enrolled := payload
		if err = publisher.Publish(txCtx, signalModel.TypeParticipantEnrolled, &enrolled); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to publish participant signal").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update participant").Err()
	}
	return nil
}

// decideParticipant is the shared fetch -> domain decision -> write path for
// Approve/Reject: no optimistic lock is needed (the write is a single
// primary-keyed UPDATE, not a whole-aggregate write competing on updated_at),
// but a zero-rows write still means the row vanished between Get and Update.
func (u *EventUseCase) decideParticipant(ctx context.Context, eventID, userID uuid.UUID, decide func(*participantModel.Participant, time.Time) error) error {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.ErrParticipantNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	if err = decide(&p, time.Now()); err != nil {
		return err
	}
	affected, err := u.participants.Update(ctx, p)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update participant").Err()
	}
	if affected == 0 {
		return participantModel.ErrParticipantNotFound.Err()
	}
	return nil
}

// participantViews completes listed rows with registration answers and the
// invitation expiry (profiles and answers in batch, no per-row queries).
func (u *EventUseCase) participantViews(ctx context.Context, eventID uuid.UUID, rows []participantRepo.Listed) ([]ParticipantView, error) {
	userIDs := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		userIDs = append(userIDs, r.Participant.UserID)
	}
	answers, err := u.forms.LatestRegistrationAnswers(ctx, eventID, userIDs)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list participant answers").Err()
	}
	registrationOpen, rosterOpen := true, true
	if e, eventErr := u.events.GetByID(ctx, eventID); eventErr == nil {
		now := time.Now()
		registrationOpen, rosterOpen = e.Lifecycle.RegistrationOpen(now), e.Lifecycle.RosterOpen(now)
	} else if !repositoryTools.IsObjectNotFoundError(eventErr) {
		return nil, model.ErrPlatform.WithError(eventErr).WithMessage("Failed to get event").Err()
	}
	items := make([]ParticipantView, 0, len(rows))
	for _, r := range rows {
		participant := toParticipantView(r.Participant)
		participant.Name = strings.TrimSpace(r.FirstName + " " + r.LastName)
		participant.Email = r.Email
		participant.DisplayName = r.DisplayName
		participant.TeamName = r.TeamName
		participant.Hidden = r.TeamHidden
		participant.InvitedTeamName = r.InvitedTeamName
		participant.Answers = answers[r.Participant.UserID]
		participant.FieldsMissing = r.FieldsMissing
		participant.LastSeenAt, participant.LastLabAt = r.LastSeenAt, r.LastLabAt
		if r.Participant.Invited && r.Participant.Status == participantModel.StatusPending {
			participant.InvitationExpired = !registrationOpen || (r.Participant.InvitedToTeam && !rosterOpen)
		}
		items = append(items, participant)
	}
	return items, nil
}
