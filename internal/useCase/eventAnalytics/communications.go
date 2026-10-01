package eventAnalytics

import (
	"context"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

// Dispatch statuses and channels as stored by the notification dispatcher.
const (
	dispatchDone    = "done"
	dispatchError   = "error"
	channelEmail    = "email"
	channelInApp    = "in_app"
	registrationKey = "registration"
)

// GetEventAnalyticsCommunications is the «Комунікації» report (§6.7): mail and
// in-app notifications per type with errors and the in-app read rate, and the
// completion of every event form. Nil bounds leave the window open.
func (u *EventAnalyticsUseCase) GetEventAnalyticsCommunications(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (CommunicationsView, error) {
	if err := checkWindow(from, to); err != nil {
		return CommunicationsView{}, err
	}
	if _, err := u.event(ctx, eventID); err != nil {
		return CommunicationsView{}, err
	}
	key := "communications:" + eventID.String() + ":" + windowKey(from, to)
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (CommunicationsView, error) {
		return u.loadCommunications(ctx, eventID, from, to)
	})
}

func (u *EventAnalyticsUseCase) loadCommunications(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (CommunicationsView, error) {
	dispatches, err := u.store.DispatchStats(ctx, eventID, from, to)
	if err != nil {
		return CommunicationsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read notification dispatches").Err()
	}
	inApp, err := u.store.InAppStats(ctx, eventID, from, to)
	if err != nil {
		return CommunicationsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read in-app notifications").Err()
	}
	forms, err := u.store.FormCompletion(ctx, eventID, from, to)
	if err != nil {
		return CommunicationsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read form completion").Err()
	}

	funnels, err := u.store.MailFunnels(ctx, eventID, from, to)
	if err != nil {
		return CommunicationsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read mail funnels").Err()
	}

	byType := map[string]*CommsTypeView{}
	row := func(t string) *CommsTypeView {
		if byType[t] == nil {
			byType[t] = &CommsTypeView{Type: t}
		}
		return byType[t]
	}
	for _, d := range dispatches {
		r := row(d.Type)
		switch {
		case d.Channel == channelEmail && d.Status == dispatchDone:
			r.EmailSent += d.Targets
		case d.Channel == channelEmail && d.Status == dispatchError:
			r.EmailErrors += d.Targets
		case d.Channel == channelInApp && d.Status == dispatchDone:
			r.InAppSent += d.Targets
		case d.Channel == channelInApp && d.Status == dispatchError:
			r.InAppErrors += d.Targets
		}
	}
	for _, n := range inApp {
		r := row(n.Type)
		r.InAppCreated += n.Total
		r.InAppRead += n.Read
	}

	view := CommunicationsView{Types: make([]CommsTypeView, 0, len(byType)), Forms: make([]FormCompletionView, 0, len(forms)), Funnels: funnels.Summary(), Period: OptionalPeriodView{From: from, To: to}}
	for _, r := range byType {
		r.ReadRate = ratio(r.InAppRead, r.InAppCreated)
		view.Types = append(view.Types, *r)
		view.Totals.EmailSent += r.EmailSent
		view.Totals.EmailErrors += r.EmailErrors
		view.Totals.InAppSent += r.InAppSent
		view.Totals.InAppErrors += r.InAppErrors
		view.Totals.InAppCreated += r.InAppCreated
		view.Totals.InAppRead += r.InAppRead
	}
	view.Totals.ReadRate = ratio(view.Totals.InAppRead, view.Totals.InAppCreated)
	sort.Slice(view.Types, func(i, j int) bool {
		ti, tj := view.Types[i].EmailSent+view.Types[i].InAppSent+view.Types[i].EmailErrors+view.Types[i].InAppErrors,
			view.Types[j].EmailSent+view.Types[j].InAppSent+view.Types[j].EmailErrors+view.Types[j].InAppErrors
		if ti != tj {
			return ti > tj
		}
		return view.Types[i].Type < view.Types[j].Type
	})
	for _, f := range forms {
		view.Forms = append(view.Forms, FormCompletionView{
			ID: f.ID, Title: f.Title, Registration: f.Purpose == registrationKey, Enabled: f.Enabled,
			Assigned: f.Assigned, Completed: f.Completed, Answers: f.Answers, CompletionRate: ratio(f.Completed, f.Assigned),
		})
	}
	return view, nil
}

func ratio(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	v := float64(part) / float64(whole)
	return &v
}
