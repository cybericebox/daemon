package event

import (
	"context"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// GetEventConfig returns the event's config. Route gate: events.read.
//
// A config-less event is self-healing: if CreateEvent's sequential,
// non-transactional config insert hasn't landed (or the event predates
// event configs), the safe-default row is lazily created here — so a config
// is never permanently absent for a real event.
func (u *EventUseCase) GetEventConfig(ctx context.Context, eventID uuid.UUID) (EventConfigView, error) {
	now := time.Now()
	c, err := u.configs.Get(ctx, eventID)
	if err == nil {
		return toEventConfigView(c), nil
	}
	if !repositoryTools.IsObjectNotFoundError(err) {
		return EventConfigView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	// event_configs.event_id FKs to events(id); confirm the event exists
	// before lazily creating its config, so a bogus/deleted eventID surfaces
	// the domain 404 instead of a raw FK-violation platform error.
	if _, evErr := u.events.GetByID(ctx, eventID); evErr != nil {
		if repositoryTools.IsObjectNotFoundError(evErr) {
			return EventConfigView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventConfigView{}, model.ErrPlatform.WithError(evErr).WithMessage("Failed to get event").Err()
	}
	created, err := u.createDefaultConfig(ctx, eventID, now)
	if err != nil {
		return EventConfigView{}, err
	}
	return toEventConfigView(created), nil
}

// GetApprovedParticipantInfo is a narrow, event-scoped capability projection.
// A global login or a pending application is insufficient to read it.
func (u *EventUseCase) GetApprovedParticipantInfo(ctx context.Context, eventID, userID uuid.UUID) (ParticipantEventInfoView, error) {
	join, err := u.joinInfo(ctx, eventID, userID)
	if err != nil {
		return ParticipantEventInfoView{}, err
	}
	if join.Status != participantModel.StatusApproved {
		return ParticipantEventInfoView{}, participantModel.ErrParticipantAccessForbidden.Err()
	}
	config, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return ParticipantEventInfoView{}, err
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ParticipantEventInfoView{}, eventModel.ErrEventNotFound.Err()
		}
		return ParticipantEventInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	started := e.Lifecycle.HasStarted(time.Now())
	results := decideResultsAvailability(config.ScoreboardVisibility, started, false, true)
	name, err := u.ownParticipantName(ctx, eventID, userID)
	if err != nil {
		return ParticipantEventInfoView{}, err
	}
	view := ParticipantEventInfoView{
		EventID: eventID, UseVPN: e.InfrastructureAllowed,
		CanViewResults:      results == ResultsAvailable,
		ResultsAvailability: results,
		CanViewParticipants: config.ParticipantsVisibility != eventConfigModel.VisibilityHidden,
		Participation:       config.Participation,
		RealName:            name.RealName,
		Pseudonym:           name.Pseudonym,
		DisplayName:         name.DisplayName,
		AllowPseudonyms:     config.AllowPseudonyms,
		PseudonymEditable:   config.AllowPseudonyms && !started,
		MaxTeamSize:         config.MaxTeamSize,
		ShowDifficulty:      config.ShowDifficulty,
		HintsDisabled:       config.HintsDisabled,
		HintChargeMode:      config.HintChargeMode,
	}
	if view.HasInfrastructureChallenges, err = u.hasInfrastructureChallenges(ctx, e); err != nil {
		return ParticipantEventInfoView{}, err
	}
	if view.MinTeamSize, err = u.teams.MinTeamSize(ctx, eventID); err != nil {
		return ParticipantEventInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get minimum team size").Err()
	}
	if join.TeamID != nil {
		admitted, admittedErr := u.teams.Admitted(ctx, eventID, *join.TeamID)
		if admittedErr != nil {
			return ParticipantEventInfoView{}, model.ErrPlatform.WithError(admittedErr).WithMessage("Failed to get team admission").Err()
		}
		view.TeamID, view.TeamAdmitted = join.TeamID, &admitted
	}
	return view, nil
}

// UpdateEventConfig applies the mutable fields. Participation and team size
// can change only until publication. The infrastructure flag is admin-owned
// (set at event creation) and never changes here. Route gate: events.write.
func (u *EventUseCase) UpdateEventConfig(ctx context.Context, eventID uuid.UUID, in UpdateConfigInput, by uuid.UUID) (EventConfigView, error) {
	if !u.lifecycleControls || in.TaskRevealMode == nil {
		return u.updateEventConfig(ctx, eventID, in, by)
	}
	if u.uow == nil {
		return EventConfigView{}, model.ErrPlatform.WithMessage("Reveal mode transaction is not configured").Err()
	}
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventConfigView{}, err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return EventConfigView{}, err
	}
	inner := *u
	inner.repo = q
	inner.configs = eventConfigRepo.New(q)
	inner.events = eventRepo.New(q)
	view, err := inner.updateEventConfig(txCtx, eventID, in, by)
	if err != nil {
		return EventConfigView{}, err
	}
	sets, err := eventExerciseRepo.New(q).List(txCtx, eventID)
	if err != nil {
		return EventConfigView{}, err
	}
	for _, set := range activeAttachments(sets) {
		if _, err = eventLabRevealRepo.New(q).Freeze(txCtx, eventID, set.ID, time.Now().UTC()); err != nil {
			return EventConfigView{}, err
		}
	}
	if err = unit.Save(); err != nil {
		return EventConfigView{}, err
	}
	return view, nil
}
func (u *EventUseCase) updateEventConfig(ctx context.Context, eventID uuid.UUID, in UpdateConfigInput, by uuid.UUID) (EventConfigView, error) {
	now := time.Now()
	c, err := u.mutateEventConfig(ctx, eventID, func(cfg *eventConfigModel.EventConfig) error {
		participationChanged := in.Participation != nil && (cfg.Participation == nil || *cfg.Participation != *in.Participation)
		teamSizeChanged := (in.MaxTeamSize != 0 && in.MaxTeamSize != cfg.MaxTeamSize) || !sameLimit(cfg.MinTeamSize, in.MinTeamSize)
		if participationChanged || teamSizeChanged {
			event, eventErr := u.events.GetByID(ctx, eventID)
			if eventErr != nil {
				if repositoryTools.IsObjectNotFoundError(eventErr) {
					return eventModel.ErrEventNotFound.Err()
				}
				return model.ErrPlatform.WithError(eventErr).WithMessage("Failed to get event").Err()
			}
			published := event.Lifecycle.Configured && !now.Before(event.Lifecycle.PublishAt)
			if published && teamSizeChanged {
				return eventConfigModel.ErrTeamSizeLocked.Err()
			}
			if participationChanged {
				if err := cfg.SetParticipation(*in.Participation, published, now, by); err != nil {
					return err
				}
			}
		}
		if !u.acceptablePreviewPicture(eventID, in.PreviewPicture, cfg.PreviewPicture) {
			return eventConfigModel.ErrPreviewPictureInvalid.Err()
		}
		if err := cfg.Update(in.toConfigInput(*cfg), now, by); err != nil {
			return err
		}
		if in.LabPolicy != nil {
			if err := cfg.SetLabPolicy(in.LabPolicy.Resolve(cfg.EffectiveLabPolicy()), now, by); err != nil {
				return err
			}
		}
		if in.TaskRevealMode != nil {
			started := false
			if *in.TaskRevealMode != cfg.TaskRevealMode {
				event, eventErr := u.events.GetByID(ctx, eventID)
				if eventErr != nil {
					if repositoryTools.IsObjectNotFoundError(eventErr) {
						return eventModel.ErrEventNotFound.Err()
					}
					return model.ErrPlatform.WithError(eventErr).WithMessage("Failed to get event").Err()
				}
				started = event.Lifecycle.HasStarted(now)
			}
			if err := cfg.SetTaskRevealMode(*in.TaskRevealMode, started, now, by); err != nil {
				return err
			}
			if u.lifecycleControls {
				return u.validateExistingGroupSizing(ctx, eventID, *cfg)
			}
			return nil
		}
		if u.lifecycleControls {
			return u.validateExistingGroupSizing(ctx, eventID, *cfg)
		}
		return nil
	})
	if err != nil {
		return EventConfigView{}, err
	}
	return toEventConfigView(c), nil
}

// acceptablePreviewPicture: the preview picture is the event's own uploaded image (our API URL, set by the
// upload route), unchanged, or cleared. Any other address would make every link preview of the
// event fetch it from a host the organizer controls (a tracking pixel).
func (u *EventUseCase) acceptablePreviewPicture(eventID uuid.UUID, requested, current string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == current {
		return true
	}
	ours, err := u.eventPreviewPictureURL(eventID, uuid.Nil)
	if err != nil {
		return false
	}
	return strings.HasPrefix(requested, strings.TrimSuffix(ours, uuid.Nil.String()))
}

func sameLimit(a, b *int32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// UpdateEventTheme keeps visual settings independent of the general config
// PUT, so a client saving registration or team limits cannot erase branding.
func (u *EventUseCase) UpdateEventTheme(ctx context.Context, eventID uuid.UUID, brand, accent string, by uuid.UUID) (EventConfigView, error) {
	now := time.Now()
	c, err := u.mutateEventConfig(ctx, eventID, func(cfg *eventConfigModel.EventConfig) error {
		return cfg.SetTheme(brand, accent, now, by)
	})
	if err != nil {
		return EventConfigView{}, err
	}
	return toEventConfigView(c), nil
}

// GetEventInfo composes the participant-facing view: the tenancy window from
// Event plus the participant-visible settings from EventConfig. Same
// lazy-create self-heal as GetEventConfig. Route gate: events.read.
func (u *EventUseCase) GetEventInfo(ctx context.Context, eventID uuid.UUID) (EventInfoView, error) {
	now := time.Now()
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventInfoView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	c, err := u.configs.Get(ctx, eventID)
	if err != nil {
		if !repositoryTools.IsObjectNotFoundError(err) {
			return EventInfoView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
		}
		// The event is already confirmed to exist (fetched above), so it is
		// safe to lazily create its config without re-checking.
		if c, err = u.createDefaultConfig(ctx, eventID, now); err != nil {
			return EventInfoView{}, err
		}
	}
	plan, err := u.eventInfrastructurePlan(ctx, eventID)
	if err != nil {
		return EventInfoView{}, err
	}
	view := toEventInfoView(e, now, c, plan)
	view.LogoURL, err = u.EventLogoURL(ctx, eventID)
	if err != nil {
		return EventInfoView{}, err
	}
	view.FaviconURL, err = u.EventFaviconURL(ctx, eventID)
	if err != nil {
		return EventInfoView{}, err
	}
	return view, nil
}

// createDefaultConfig persists the safe-default config row for an event
// already confirmed to exist but without one yet.
//
// Two concurrent first-reads of the same config-less event can both reach
// here; event_configs.event_id is the primary key, so the loser's Create
// fails with a unique-violation rather than corrupting anything. Tolerate
// that race by re-reading and returning the winner's row instead of
// surfacing a spurious platform error.
func (u *EventUseCase) createDefaultConfig(ctx context.Context, eventID uuid.UUID, now time.Time) (eventConfigModel.EventConfig, error) {
	created, err := u.configs.Create(ctx, eventConfigModel.NewEventConfig(eventID, now))
	if err != nil {
		if _, ok := repositoryTools.UniqueViolationError(err, model.ErrPlatform); ok {
			return u.configs.Get(ctx, eventID)
		}
		return eventConfigModel.EventConfig{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create default event config").Err()
	}
	return created, nil
}
