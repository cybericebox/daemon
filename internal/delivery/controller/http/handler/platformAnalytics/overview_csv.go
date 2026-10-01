package platformAnalytics

import (
	"encoding/csv"
	"sort"
	"time"

	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// csvPrevious prints the previous period's value; empty for all time.
func csvPrevious(m platformAnalyticsUseCase.OverviewMetricView) string {
	if m.Previous == nil {
		return ""
	}
	return csvInt(*m.Previous)
}

func writeOverviewSummary(w *csv.Writer, v platformAnalyticsUseCase.OverviewView) {
	_ = w.Write([]string{"Показник", "Значення", "Попередній період"})
	metric := func(name string, m platformAnalyticsUseCase.OverviewMetricView) {
		_ = w.Write([]string{name, csvInt(m.Value), csvPrevious(m)})
	}
	current := func(name string, value int64) { _ = w.Write([]string{name, csvInt(value), ""}) }
	current("Акаунтів усього", v.Users.Total)
	metric("Нових акаунтів", v.Users.New)
	metric("Активних акаунтів", v.Users.Active)
	current("Заходів: чернетка", v.Events.Draft)
	current("Заходів: опубліковано", v.Events.Published)
	current("Заходів: триває", v.Events.Running)
	current("Заходів: завершено", v.Events.Finished)
	current("Заходів: архів", v.Events.Archived)
	metric("Нових заходів", v.Events.New)
	metric("Реєстрацій учасників", v.Participants.Registered)
	metric("Схвалених учасників", v.Participants.Approved)
	metric("Спроб", v.Activity.Attempts)
	metric("Розв'язань", v.Activity.Solves)
	metric("Листів надіслано", v.Mail.Sent)
	metric("Листів з помилкою", v.Mail.Failed)
	current("Стендів працює", v.Stands.Ready)
	current("Стендів розгортається", v.Stands.Creating)
	current("Стендів з помилкою зараз", v.Stands.Failed)
	metric("Збоїв стендів", v.Stands.Failures)
}

func writeOverviewSeries(w *csv.Writer, v platformAnalyticsUseCase.OverviewView) {
	_ = w.Write([]string{"Дата", "Нових акаунтів", "Спроб", "Розв'язань", "Листів надіслано", "Листів з помилкою"})
	type row struct{ newUsers, attempts, solves, sent, failed int64 }
	days := map[time.Time]*row{}
	at := func(day time.Time) *row {
		if days[day] == nil {
			days[day] = &row{}
		}
		return days[day]
	}
	for _, d := range v.Series.NewUsers {
		at(d.Day).newUsers = d.New
	}
	for _, d := range v.Series.Activity {
		r := at(d.Day)
		r.attempts, r.solves = d.Attempts, d.Solves
	}
	for _, d := range v.Series.Mail {
		r := at(d.Day)
		r.sent, r.failed = d.Sent, d.Failed
	}
	keys := make([]time.Time, 0, len(days))
	for day := range days {
		keys = append(keys, day)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	for _, day := range keys {
		r := days[day]
		_ = w.Write([]string{csvDay(day), csvInt(r.newUsers), csvInt(r.attempts), csvInt(r.solves), csvInt(r.sent), csvInt(r.failed)})
	}
}
