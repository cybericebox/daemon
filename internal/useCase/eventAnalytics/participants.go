package eventAnalytics

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

const (
	// maxRegistrationDays bounds the per-day series.
	maxRegistrationDays = 400
	// dropOffLimit cuts the drop-off list.
	dropOffLimit = 500
)

// ParticipantStore is the «Учасники» and «Комунікації» read port (satisfied by
// *eventAnalyticsRepo.Repository); Store embeds it.
type ParticipantStore interface {
	ParticipantFunnel(ctx context.Context, eventID uuid.UUID) (eventAnalyticsRepo.ParticipantFunnel, error)
	RegistrationDays(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]eventAnalyticsRepo.RegistrationDay, error)
	TeamFill(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.TeamFill, error)
	RegistrationFormVersions(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.FormVersion, error)
	RegistrationAnswers(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.FormAnswer, error)
	DropOffs(ctx context.Context, eventID uuid.UUID, limit int32) ([]eventAnalyticsRepo.DropOff, int64, error)
	DispatchStats(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]eventAnalyticsRepo.DispatchCount, error)
	InAppStats(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]eventAnalyticsRepo.InAppCount, error)
	FormCompletion(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]eventAnalyticsRepo.FormCompletion, error)
	MailFunnels(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (dispatchModel.MailFunnels, error)
}

// checkWindow validates optional report bounds: from must precede to.
func checkWindow(from, to *time.Time) error {
	if from != nil && to != nil && !from.Before(*to) {
		return eventAnalyticsModel.ErrEventAnalyticsPeriodInvalid.Err()
	}
	return nil
}

// windowKey is the cache key part of an optional window.
func windowKey(from, to *time.Time) string {
	bound := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return fmt.Sprint(t.Unix())
	}
	return bound(from) + ":" + bound(to)
}

// GetEventAnalyticsParticipants is the «Учасники» report (§6.2). The window
// (nil bounds: open) limits the registrations per day; the funnel, team fill,
// answers and drop-off list describe the current state.
func (u *EventAnalyticsUseCase) GetEventAnalyticsParticipants(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (ParticipantsView, error) {
	if err := checkWindow(from, to); err != nil {
		return ParticipantsView{}, err
	}
	if _, err := u.event(ctx, eventID); err != nil {
		return ParticipantsView{}, err
	}
	key := "participants:" + eventID.String() + ":" + windowKey(from, to)
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (ParticipantsView, error) {
		return u.loadParticipants(ctx, eventID, from, to)
	})
}

func (u *EventAnalyticsUseCase) loadParticipants(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (ParticipantsView, error) {
	fail := func(err error, msg string) (ParticipantsView, error) {
		return ParticipantsView{}, model.ErrPlatform.WithError(err).WithMessage(msg).Err()
	}
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return fail(err, "Failed to get event config")
	}
	funnel, err := u.store.ParticipantFunnel(ctx, eventID)
	if err != nil {
		return fail(err, "Failed to count the participant funnel")
	}
	days, err := u.store.RegistrationDays(ctx, eventID, from, to)
	if err != nil {
		return fail(err, "Failed to read registrations")
	}
	teams, err := u.store.TeamFill(ctx, eventID)
	if err != nil {
		return fail(err, "Failed to read team fill")
	}
	versions, err := u.store.RegistrationFormVersions(ctx, eventID)
	if err != nil {
		return fail(err, "Failed to read the registration form")
	}
	answers, err := u.store.RegistrationAnswers(ctx, eventID)
	if err != nil {
		return fail(err, "Failed to read registration answers")
	}
	dropOffs, dropTotal, err := u.store.DropOffs(ctx, eventID, dropOffLimit)
	if err != nil {
		return fail(err, "Failed to read the drop-off list")
	}

	teamMode := config.Participation != nil && *config.Participation == eventConfigModel.ParticipationTeam
	view := ParticipantsView{
		TeamMode:      teamMode,
		Funnel:        funnelStages(funnel, teamMode),
		Registrations: registrationsView(days, from, to, u.now()),
		Answers:       AnswersView{Respondents: int64(len(answers)), Questions: buildQuestions(versions, answers)},
		DropOff:       DropOffView{Total: dropTotal, Rows: make([]DropOffRowView, 0, len(dropOffs))},
		Period:        OptionalPeriodView{From: from, To: to},
	}
	if teamMode {
		view.Teams = teamFillView(teams, config.EffectiveMinTeamSize(), config.MaxTeamSize, funnel.Approved-funnel.InTeam)
	}
	for _, d := range dropOffs {
		view.DropOff.Rows = append(view.DropOff.Rows, DropOffRowView{
			UserID: d.UserID, Name: d.Name, Email: d.Email, TeamName: d.TeamName,
			RegisteredAt: d.RegisteredAt, ApprovedAt: d.ApprovedAt, OpenedTasks: d.OpenedTasks,
		})
	}
	return view, nil
}

func funnelStages(f eventAnalyticsRepo.ParticipantFunnel, teamMode bool) []FunnelStageView {
	stages := []FunnelStageView{
		{Stage: StageInvited, Count: f.Invited},
		{Stage: StageRegistered, Count: f.Registered},
		{Stage: StageApproved, Count: f.Approved},
	}
	if teamMode {
		stages = append(stages, FunnelStageView{Stage: StageInTeam, Count: f.InTeam})
	}
	return append(stages,
		FunnelStageView{Stage: StageAttempted, Count: f.Attempted},
		FunnelStageView{Stage: StageSolved, Count: f.Solved},
	)
}

// registrationsView lays the stored days over every UTC day of the window: from
// the requested start (else the first registration day) to the requested end
// (else today or the last registration day).
func registrationsView(rows []eventAnalyticsRepo.RegistrationDay, from, to *time.Time, now time.Time) RegistrationsView {
	view := RegistrationsView{Days: []RegistrationDayView{}}
	if len(rows) == 0 && (from == nil || to == nil) {
		return view
	}
	byDay := map[int64]*RegistrationDayView{}
	first, last := now.UTC().Truncate(24*time.Hour), time.Time{}
	if len(rows) > 0 {
		first = rows[0].Day
	}
	for _, r := range rows {
		d := byDay[r.Day.Unix()]
		if d == nil {
			d = &RegistrationDayView{Day: r.Day}
			byDay[r.Day.Unix()] = d
		}
		switch r.Channel {
		case ChannelOpen:
			d.Open += r.Registrations
		case ChannelInvitation:
			d.Invitation += r.Registrations
		default:
			d.Approval += r.Registrations
		}
		view.Total += r.Registrations
		if r.Day.After(last) {
			last = r.Day
		}
	}
	start := first
	if from != nil {
		start = from.UTC().Truncate(24 * time.Hour)
	}
	end := now.UTC().Truncate(24 * time.Hour)
	if last.After(end) {
		end = last
	}
	if to != nil {
		end = to.UTC().Add(-time.Nanosecond).Truncate(24 * time.Hour)
	}
	if end.Sub(start) > (maxRegistrationDays-1)*24*time.Hour {
		start = end.Add(-(maxRegistrationDays - 1) * 24 * time.Hour)
	}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		if d := byDay[day.Unix()]; d != nil {
			view.Days = append(view.Days, *d)
		} else {
			view.Days = append(view.Days, RegistrationDayView{Day: day})
		}
	}
	return view
}

func teamFillView(teams []eventAnalyticsRepo.TeamFill, minSize, maxSize int32, withoutTeam int64) TeamFillView {
	if maxSize < 1 {
		maxSize = 1
	}
	view := TeamFillView{
		MinSize: minSize, MaxSize: maxSize, Total: int64(len(teams)), WithoutTeam: max(withoutTeam, 0),
		Histogram: make([]FillBucketView, maxSize+1), Incomplete: []IncompleteTeamView{},
	}
	for size := range view.Histogram {
		view.Histogram[size].Members = int32(size)
	}
	for _, t := range teams {
		view.Histogram[min(max(t.Members, 0), maxSize)].Teams++
		view.PendingInvitees += t.PendingInvitees
		if !t.Admitted && !t.Individual {
			view.Incomplete = append(view.Incomplete, IncompleteTeamView{ID: t.ID, Name: t.Name, Members: t.Members, PendingInvitees: t.PendingInvitees})
		}
	}
	return view
}
