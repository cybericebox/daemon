package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// resultsPolicy is everything one reader's results view depends on: the
// visibility, the freeze and the reader's own team.
type resultsPolicy struct {
	cfg         eventConfigModel.EventConfig
	event       eventModel.Event
	manager     bool
	participant *participantModel.Participant
}

// resultsViewer is the reader's own part of a results policy.
type resultsViewer struct {
	manager     bool
	participant *participantModel.Participant
}

func (u *EventUseCase) loadResultsViewer(ctx context.Context, eventID uuid.UUID, access ResultsAccess) resultsViewer {
	if access.Screen {
		// The link was checked for this event by the screen routes.
		return resultsViewer{manager: true}
	}
	viewer := resultsViewer{manager: access.Role.HasPermission(rbac.PermEventsRead)}
	if !viewer.manager && access.UserID != nil {
		if _, managerErr := u.managers.Get(ctx, eventID, *access.UserID); managerErr == nil {
			viewer.manager = true
		}
	}
	if !viewer.manager && access.UserID != nil {
		// A missing participant is simply a guest with a session.
		if p, participantErr := u.participants.Get(ctx, eventID, *access.UserID); participantErr == nil {
			viewer.participant = &p
		}
	}
	return viewer
}

func (u *EventUseCase) loadResultsPolicy(ctx context.Context, eventID uuid.UUID, access ResultsAccess) (resultsPolicy, error) {
	cfg, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return resultsPolicy{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	viewer := u.loadResultsViewer(ctx, eventID, access)
	policy := resultsPolicy{cfg: cfg, manager: viewer.manager, participant: viewer.participant}
	policy.event, err = u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return resultsPolicy{}, eventModel.ErrEventNotFound.Err()
		}
		return resultsPolicy{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return policy, nil
}

func (p resultsPolicy) approved() bool {
	return p.participant != nil && p.participant.Status == participantModel.StatusApproved
}

// ownTeam is the approved reader's team, nil for guests and moderators.
func (p resultsPolicy) ownTeam() *uuid.UUID {
	if !p.approved() {
		return nil
	}
	return p.participant.TeamID
}

func (p resultsPolicy) availability(now time.Time) ResultsAvailability {
	return decideResultsAvailability(p.cfg.ScoreboardVisibility, p.event.Lifecycle.HasStarted(now), p.manager, p.approved())
}

// readErr is the read gate of the results and their stream. The live screen
// is opened by a moderator for the projector, never by the public (L1).
func (p resultsPolicy) readErr(now time.Time, liveScreen bool) error {
	if liveScreen && !p.manager {
		return eventConfigModel.ErrResultsLiveScreenManagersOnly.Err()
	}
	// The route is public: an event that is not published yet does not exist for anyone but its
	// staff (its teams and scores are not for the public to read, whatever the page settings say).
	if !p.manager && p.event.Lifecycle.Status(now) == eventModel.LifecycleNotPublished {
		return eventModel.ErrEventNotFound.Err()
	}
	// Before the start the table is readable too: its audience sees the
	// admitted teams with no points yet instead of a closed page.
	if availability := p.availability(now); availability != ResultsNotStarted {
		return availability.err()
	}
	return nil
}

// freeze applies the freeze to non-moderators. The live screen is a
// moderator's view shown to the audience, so it follows LiveFreeze instead.
func (p resultsPolicy) freeze(now time.Time, liveScreen bool) ResultsFreezeView {
	view := freezeView(p.cfg.Results, p.event.Lifecycle.EffectiveFinishAt(), now)
	if liveScreen {
		view.Applied = view.Active && p.cfg.Results.LiveFreeze
	} else {
		view.Applied = view.Active && !p.manager
	}
	return view
}

func freezeView(settings eventConfigModel.ResultsSettings, finish *time.Time, now time.Time) ResultsFreezeView {
	return ResultsFreezeView{Enabled: settings.FreezeEnabled, FrozenAt: settings.FrozenAt(finish), FinishAt: finish, OpenedAt: settings.OpenedAt,
		Active: settings.FreezeActive(finish, now)}
}

// cutoff is the moment the returned data is frozen at, nil for live data.
func (v ResultsFreezeView) cutoff() *time.Time {
	if !v.Applied {
		return nil
	}
	return v.FrozenAt
}

// freezeKey identifies the viewer's freeze state for a live stream: a change
// means the client must reload its snapshot.
func (v ResultsFreezeView) freezeKey() string {
	if at := v.cutoff(); at != nil {
		return at.UTC().Format(time.RFC3339Nano)
	}
	return ""
}

// ownResultsCutoff is the freeze applied to a participant's own results: the
// visibility setting does not matter there, the freeze does.
func (u *EventUseCase) ownResultsCutoff(ctx context.Context, eventID, userID uuid.UUID) (*time.Time, error) {
	policy, err := u.loadResultsPolicy(ctx, eventID, ResultsAccess{UserID: &userID})
	if err != nil {
		return nil, err
	}
	return policy.freeze(time.Now(), false).cutoff(), nil
}

// boardResultsCutoff decides what the challenge board may reveal about other
// teams: nothing when results are not available to the participant, else the
// solves before the applied freeze (nil = all).
func (u *EventUseCase) boardResultsCutoff(ctx context.Context, eventID, userID uuid.UUID) (bool, *time.Time, error) {
	now := time.Now()
	policy, err := u.loadResultsPolicy(ctx, eventID, ResultsAccess{UserID: &userID})
	if err != nil {
		return false, nil, err
	}
	if policy.availability(now) != ResultsAvailable {
		return false, nil, nil
	}
	return true, policy.freeze(now, false).cutoff(), nil
}
