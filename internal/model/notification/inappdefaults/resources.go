package inappdefaults

// The resource calendar flow: a change request and its decision, and the readiness alarm. Created at daemon
// start (see startupdefaults.Seed), like the elevation flow.
func init() {
	for _, typ := range []string{"resources.change.requested", "resources.change.approved", "resources.change.rejected", "resources.alarm.raised"} {
		StartupSeeded[typ] = true
	}
	uk = append(uk,
		Template{"resources.change.requested", "Запит на зміну ресурсів", "{{.requester_name}} просить змінити резерв ресурсів заходу «{{.event_name}}»: {{.summary}}. Причина: {{.reason}}.", "{{.admin_url}}resources/"},
		Template{"resources.change.approved", "Зміну ресурсів схвалено", "Резерв ресурсів заходу «{{.event_name}}» змінено: {{.summary}}.", ""},
		Template{"resources.change.rejected", "Запит на ресурси відхилено", "Запит на зміну резерву ресурсів заходу «{{.event_name}}» відхилено.", ""},
		Template{"resources.alarm.raised", "Резерв ресурсів не забезпечено", "Захід «{{.event_name}}»: резерв ресурсів не забезпечено ({{.alarm_kind}}, команд: {{.units}}). Перегляньте розклад ресурсів.", "{{.admin_url}}resources/"},
	)
	en = append(en,
		Template{"resources.change.requested", "Resource change requested", "{{.requester_name}} asks to change the resource reservation of «{{.event_name}}»: {{.summary}}. Reason: {{.reason}}.", "{{.admin_url}}resources/"},
		Template{"resources.change.approved", "Resource change approved", "The resource reservation of «{{.event_name}}» was changed: {{.summary}}.", ""},
		Template{"resources.change.rejected", "Resource request declined", "The request to change the resource reservation of «{{.event_name}}» was declined.", ""},
		Template{"resources.alarm.raised", "Resource reservation not covered", "Event «{{.event_name}}»: the resource reservation cannot be served ({{.alarm_kind}}, teams: {{.units}}). Check the resource calendar.", "{{.admin_url}}resources/"},
	)
}

// alarmReasons is the wording of every readiness alarm kind in the alarm notification, which prints it where its
// text says why the reservation is not covered. Keys are the values of resourceCalendar.AlarmKind.
var alarmReasons = map[Lang]map[string]string{
	UK: {
		"not_placed":    "не вміщається на жоден агент",
		"agent_lost":    "агент недоступний",
		"agent_shrunk":  "агент зменшив місткість",
		"not_connected": "агент не на звʼязку",
	},
	EN: {
		"not_placed":    "fits no agent",
		"agent_lost":    "agent is gone",
		"agent_shrunk":  "agent capacity dropped",
		"not_connected": "agent is not connected",
	},
}

// AlarmReason is the human text of a readiness alarm kind in the language; an unknown kind is returned as it is.
func AlarmReason(lang Lang, kind string) string {
	if text, ok := alarmReasons[lang][kind]; ok {
		return text
	}
	return kind
}
