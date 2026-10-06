package platformAnalytics

import (
	"encoding/csv"

	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

func writeUsersRegistrations(w *csv.Writer, v platformAnalyticsUseCase.UsersView) {
	_ = w.Write([]string{"Дата", "Нових акаунтів"})
	for _, d := range v.Registrations {
		_ = w.Write([]string{csvDay(d.Day), csvInt(d.New)})
	}
}

func writeUsersActivity(w *csv.Writer, v platformAnalyticsUseCase.UsersView) {
	_ = w.Write([]string{"Дата", "Активних за день (DAU)", "Активних за тиждень (WAU)"})
	for _, d := range v.ActiveByDay {
		_ = w.Write([]string{csvDay(d.Day), csvInt(d.DAU), csvInt(d.WAU)})
	}
}

var usersMethodLabels = map[string]string{
	platformAnalyticsUseCase.UsersMethodPassword: "Пароль",
	platformAnalyticsUseCase.UsersMethodGoogle:   "Google",
	platformAnalyticsUseCase.UsersMethodBoth:     "Пароль і Google",
	platformAnalyticsUseCase.UsersMethodNone:     "Без способу входу",
}

func writeUsersMethods(w *csv.Writer, v platformAnalyticsUseCase.UsersView) {
	_ = w.Write([]string{"Спосіб входу", "Акаунтів", "Нових за період"})
	for _, m := range v.Methods {
		label := usersMethodLabels[m.Method]
		if label == "" {
			label = m.Method
		}
		_ = w.Write([]string{label, csvInt(m.Total), csvInt(m.New)})
	}
}

func writeUsersRetention(w *csv.Writer, v platformAnalyticsUseCase.UsersView) {
	_ = w.Write([]string{"Участь у заходах", "Акаунтів"})
	_ = w.Write([]string{"1 захід", csvInt(v.Retention.One)})
	_ = w.Write([]string{"2 заходи", csvInt(v.Retention.Two)})
	_ = w.Write([]string{"3 і більше заходів", csvInt(v.Retention.ThreePlus)})
	_ = w.Write([]string{"Жодного заходу", csvInt(v.Retention.Never)})
}

func writeUsersPeople(w *csv.Writer, rows []platformAnalyticsUseCase.UsersPersonView) {
	_ = w.Write([]string{"Імʼя", "Email", "Роль", "Заходів", "Розвʼязань"})
	for _, r := range rows {
		_ = w.Write([]string{csvText(r.Name), csvText(r.Email), csvText(r.Role), csvInt(r.EventsJoined), csvInt(r.Solves)})
	}
}
