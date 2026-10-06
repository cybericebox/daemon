package eventAnalytics

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// feedLimit is how many moments the overview feed shows.
const feedLimit = 30

// feedOf maps the stored moments and adds the freeze start once it has
// happened, keeping the newest first and the length within feedLimit.
func feedOf(items []eventAnalyticsRepo.FeedItem, freezeAt *time.Time, now time.Time) []FeedItemView {
	out := make([]FeedItemView, 0, len(items)+1)
	for _, item := range items {
		view := FeedItemView{Kind: item.Kind, At: item.At, TeamName: item.TeamName, ChallengeName: item.ChallengeName, Detail: item.Detail}
		if item.TeamID != uuid.Nil {
			id := item.TeamID
			view.TeamID = &id
		}
		out = append(out, view)
	}
	if freezeAt != nil && !freezeAt.After(now) {
		out = append(out, FeedItemView{Kind: "freeze_started", At: *freezeAt})
		sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	}
	if len(out) > feedLimit {
		out = out[:feedLimit]
	}
	return out
}

// GetEventAnalyticsOverview is the «Огляд» report: the §6.1 counters and the
// 5-minute activity series over the period (nil bounds: the event's own
// window). It is the reference section: event read → period → cached load.
func (u *EventAnalyticsUseCase) GetEventAnalyticsOverview(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (OverviewView, error) {
	now := u.now()
	e, err := u.event(ctx, eventID)
	if err != nil {
		return OverviewView{}, err
	}
	finish := e.Lifecycle.EffectiveFinishAt()
	period, err := eventAnalyticsModel.NewPeriod(from, to, e.Lifecycle.StartAt, finish, now)
	if err != nil {
		return OverviewView{}, err
	}
	key := fmt.Sprintf("overview:%s:%d:%d", eventID, period.From.Unix(), period.To.Unix())
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (OverviewView, error) {
		return u.loadOverview(ctx, e, finish, period)
	})
}

func (u *EventAnalyticsUseCase) loadOverview(ctx context.Context, e eventModel.Event, finish *time.Time, period eventAnalyticsModel.Period) (OverviewView, error) {
	now := u.now()
	counts, err := u.store.Overview(ctx, e.ID, now.Add(-eventAnalyticsModel.ActiveWindow))
	if err != nil {
		return OverviewView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count event analytics").Err()
	}
	points, err := u.store.Series(ctx, e.ID, period)
	if err != nil {
		return OverviewView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read event activity series").Err()
	}
	items, err := u.store.Feed(ctx, e.ID, feedLimit)
	if err != nil {
		return OverviewView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read event analytics feed").Err()
	}
	state, err := u.store.RollupState(ctx, e.ID)
	if err != nil {
		return OverviewView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read event analytics state").Err()
	}
	config, err := u.configs.Get(ctx, e.ID)
	if err != nil {
		return OverviewView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	snapshots, err := u.loadSnapshots(ctx, e.ID, period, now)
	if err != nil {
		return OverviewView{}, err
	}
	freezeAt := eventAnalyticsModel.FreezeAt(finish, config.Results.FreezeEnabled, config.Results.FreezeMinutes)
	return OverviewView{
		Participants: ParticipantCountsView{
			Registered: counts.ParticipantsRegistered, Approved: counts.ParticipantsApproved,
			Pending: counts.ParticipantsPending, Invited: counts.ParticipantsInvited, Active: counts.ParticipantsActive,
		},
		Teams:             TeamCountsView{Total: counts.TeamsTotal, Admitted: counts.TeamsAdmitted, Incomplete: counts.TeamsTotal - counts.TeamsAdmitted},
		Attempts:          counts.Attempts,
		Correct:           counts.AttemptsCorrect,
		Solves:            counts.Solves,
		HintsOpened:       counts.HintsOpened,
		HintPoints:        counts.HintPoints,
		Stands:            StandCountsView{Creating: counts.StandsCreating, Ready: counts.StandsReady, Failed: counts.StandsFailed},
		Series:            denseSeries(points, period),
		Feed:              feedOf(items, freezeAt, now),
		OverviewSnapshots: snapshots,
		Markers: MarkersView{
			StartAt:  e.Lifecycle.StartAt,
			FreezeAt: freezeAt,
			FinishAt: finish,
		},
		Period:      PeriodView{From: period.From, To: period.To},
		RefreshedAt: state.RefreshedAt,
		Final:       state.FinalizedAt != nil,
	}, nil
}

// event reads the event of a report; a missing one is the not-found error.
func (u *EventAnalyticsUseCase) event(ctx context.Context, eventID uuid.UUID) (eventModel.Event, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.Event{}, eventModel.ErrEventNotFound.Err()
		}
		return eventModel.Event{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return e, nil
}
