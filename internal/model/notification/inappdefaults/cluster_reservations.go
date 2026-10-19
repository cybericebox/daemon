package inappdefaults

// The cluster reservations: the ending notice and the end people must act on. Created at daemon start (see
// startupdefaults.Seed), like the resource calendar flow.
func init() {
	for _, typ := range []string{"resources.reservation.ending", "resources.reservation.reduced", "testlab.ending", "testlab.ended"} {
		StartupSeeded[typ] = true
	}
	uk = append(uk,
		Template{"resources.reservation.ending", "Резерв ресурсів закінчується", "Захід «{{.event_name}}»: резерв ресурсів закінчується о {{.ends_at}}, хоча захід ще триває. Лабораторії буде видалено. Продовжте резерв у розкладі ресурсів.", ""},
		Template{"resources.reservation.reduced", "Резерв ресурсів зменшено", "Захід «{{.event_name}}»: резерв став меншим, ніж уже працює. Лабораторії, що не вміщаються, буде видалено о {{.ends_at}}.", ""},
		Template{"testlab.ending", "Тестова лабораторія скоро завершиться", "Лабораторію «{{.exercise_name}}» буде видалено о {{.ends_at}}. Щоб працювати далі, продовжте її.", ""},
		Template{"testlab.ended", "Тестову лабораторію завершено", "Час лабораторії «{{.exercise_name}}» минув, її видалено.", ""},
	)
	en = append(en,
		Template{"resources.reservation.ending", "Resource reservation is ending", "Event «{{.event_name}}»: the resource reservation ends at {{.ends_at}} while the event still runs. The labs will be deleted. Extend the reservation in the resource calendar.", ""},
		Template{"resources.reservation.reduced", "Resource reservation reduced", "Event «{{.event_name}}»: the reservation became smaller than what already runs. The labs that do not fit will be deleted at {{.ends_at}}.", ""},
		Template{"testlab.ending", "Test laboratory ends soon", "The laboratory «{{.exercise_name}}» will be deleted at {{.ends_at}}. Extend it to keep working.", ""},
		Template{"testlab.ended", "Test laboratory ended", "The time of the laboratory «{{.exercise_name}}» is over, it was deleted.", ""},
	)
}
