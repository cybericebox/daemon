package inappdefaults

// The resource calendar flow: a change request and its decision, and the readiness alarm. Created at daemon
// start (see startupdefaults.Seed), like the elevation flow.
func init() {
	for _, typ := range []string{"resources.change.requested", "resources.change.approved", "resources.change.rejected", "resources.alarm.raised", "resources.labs.withdrawn", "resources.cpu.throttled"} {
		StartupSeeded[typ] = true
	}
	uk = append(uk,
		Template{"resources.change.requested", "Запит на зміну ресурсів", "{{.requester_name}} просить змінити резерв ресурсів заходу «{{.event_name}}»: {{.summary}}. Причина: {{.reason}}.", "{{.admin_url}}resources/"},
		Template{"resources.change.approved", "Зміну ресурсів схвалено", "Резерв ресурсів заходу «{{.event_name}}» змінено: {{.summary}}.", ""},
		Template{"resources.change.rejected", "Запит на ресурси відхилено", "Запит на зміну резерву ресурсів заходу «{{.event_name}}» відхилено.", ""},
		Template{"resources.alarm.raised", "Резерв ресурсів не забезпечено", "Захід «{{.event_name}}»: резерв ресурсів не забезпечено ({{.alarm_kind}}, команд: {{.units}}). Перегляньте розклад ресурсів.", "{{.admin_url}}resources/"},
		Template{"resources.labs.withdrawn", "Лабораторіям бракує вузлів", "Агент «{{.agent_name}}»: групу лабораторій {{.group}} відкликано вже {{.attempt}} рази поспіль — не всі її поди отримали вузол вчасно. Перевірте місткість кластера.", "{{.admin_url}}resources/"},
		Template{"resources.cpu.throttled", "Пристрою не вистачає процесора", "Пристрій «{{.device_name}}» лабораторії «{{.lab_name}}» не отримував достатньо процесорного часу в {{.percent}}% інтервалів за останні {{.window_minutes}} хв: його стримує виділений ліміт CPU.", ""},
	)
	en = append(en,
		Template{"resources.change.requested", "Resource change requested", "{{.requester_name}} asks to change the resource reservation of «{{.event_name}}»: {{.summary}}. Reason: {{.reason}}.", "{{.admin_url}}resources/"},
		Template{"resources.change.approved", "Resource change approved", "The resource reservation of «{{.event_name}}» was changed: {{.summary}}.", ""},
		Template{"resources.change.rejected", "Resource request declined", "The request to change the resource reservation of «{{.event_name}}» was declined.", ""},
		Template{"resources.alarm.raised", "Resource reservation not covered", "Event «{{.event_name}}»: the resource reservation cannot be served ({{.alarm_kind}}, teams: {{.units}}). Check the resource calendar.", "{{.admin_url}}resources/"},
		Template{"resources.labs.withdrawn", "Labs are short of nodes", "Agent «{{.agent_name}}»: the lab group {{.group}} was withdrawn {{.attempt}} times in a row — not every pod got a node in time. Check the cluster capacity.", "{{.admin_url}}resources/"},
		Template{"resources.cpu.throttled", "A device is short of CPU", "Device «{{.device_name}}» of lab «{{.lab_name}}» did not get enough CPU time in {{.percent}}% of intervals over the last {{.window_minutes}} min: its allocated CPU limit holds it back.", ""},
	)
}

// alarmReasons is the wording of every readiness alarm kind in the alarm notification, which prints it where its
// text says why the reservation is not covered. Keys are the values of resourceCalendar.AlarmKind.
var alarmReasons = map[Lang]map[string]string{
	UK: {
		"not_placed":          "не вміщається на жоден агент",
		"agent_lost":          "агент недоступний",
		"agent_shrunk":        "агент зменшив місткість",
		"not_connected":       "агент не на звʼязку",
		"cluster_sync_failed": "резерв не передано в кластер",
		"cluster_shrink_held": "зменшення резерву відкладено: у ньому ще працюють лабораторії",
	},
	EN: {
		"not_placed":          "fits no agent",
		"agent_lost":          "agent is gone",
		"agent_shrunk":        "agent capacity dropped",
		"not_connected":       "agent is not connected",
		"cluster_sync_failed": "the reservation did not reach the cluster",
		"cluster_shrink_held": "the shrink is held back: labs still run in the reservation",
	},
}

// AlarmReason is the human text of a readiness alarm kind in the language; an unknown kind is returned as it is.
func AlarmReason(lang Lang, kind string) string {
	if text, ok := alarmReasons[lang][kind]; ok {
		return text
	}
	return kind
}
