// Package inappdefaults is the code-owned source of the default in-app
// (inbox) notification copy: title, body and the one action link of every
// participant/manager notification type, in Ukrainian (the seeded rows) and
// English. Titles match the h1 of the email of the same type. The seed
// migration is generated from it. Icon and tone stay as seeded.
//
// Bodies use only variables the emitter always provides ({{.name}} text
// templates, a missing key fails delivery), so there are no optional parts.
// The link is empty unless the user has to act or can go to the event.
package inappdefaults

// Lang is the language of a copy set.
type Lang string

const (
	UK Lang = "uk"
	EN Lang = "en"
)

// Template is the copy of one in-app notification type.
type Template struct {
	Type, Title, Body, Link string
}

// StartupSeeded are the in-app types created at daemon start from these defaults instead of by a migration
// (see startupdefaults.Seed): the resource elevation flow. Database tests that compare the migrated rows with
// the defaults skip them.
var StartupSeeded = map[string]bool{
	"exercise.elevation.requested": true, "exercise.elevation.approved": true, "exercise.elevation.rejected": true,
}

// All returns the copy of lang, one entry per type whose text is rewritten.
func All(lang Lang) []Template {
	if lang == EN {
		return en
	}
	return uk
}

var uk = []Template{
	{"flag_accepted", "Прапор зараховано", "«{{.Challenge}}»: {{.Points}} балів.", ""},
	{"event.manager.assigned", "Вам надано доступ до заходу", "Вас призначено {{.role_name}} заходу «{{.event_name}}».", ""},
	{"event.lab.failed", "Лабораторія не працює", "Команда «{{.team_name}}», захід «{{.event_name}}». Перевірте лабораторію на сторінці «Лабораторії».", ""},
	{"participant.approval_registration.submitted", "Заявку подано", "Заявку на участь у заході «{{.event_name}}» отримано. Очікуйте рішення.", ""},
	{"participant.approval_registration.approved", "Участь схвалено", "Заявку на участь у заході «{{.event_name}}» схвалено.", "{{.event_url}}"},
	{"participant.approval_registration.rejected", "Заявку відхилено", "Заявку на участь у заході «{{.event_name}}» відхилено.", ""},
	{"participant.open_registration.completed", "Ви зареєстровані", "Ви зареєстровані на захід «{{.event_name}}».", "{{.event_url}}"},
	{"participant.invitation.sent", "Вас запрошено до заходу", "Вас запрошено до заходу «{{.event_name}}».", "{{.invite_url}}"},
	{"participant.team_invitation.sent", "Запрошення до команди", "Вас запрошено до команди «{{.team_name}}» на заході «{{.event_name}}».", "{{.invite_url}}"},
	{"participant.invitation.accepted", "Запрошення прийнято", "Ви прийняли запрошення до заходу «{{.event_name}}».", "{{.event_url}}"},
	{"participant.invitation.revoked", "Запрошення скасовано", "Запрошення до заходу «{{.event_name}}» скасовано.", ""},
	{"participant.invitation.expired", "Термін запрошення минув", "Запрошення до заходу «{{.event_name}}» більше не чинне.", ""},
	{"participant.event.start_reminder", "Захід скоро почнеться", "Захід «{{.event_name}}» починається {{.start_at}} (за київським часом).", "{{.event_url}}"},
	{"participant.event.finished", "Захід завершено", "Дякуємо за участь у заході «{{.event_name}}»!", "{{.event_url}}"},
	{"participant.event.results_published", "Підсумки відкрито", "Результати заходу «{{.event_name}}» уже доступні.", "{{.event_url}}"},
	{"exercise.elevation.requested", "Запит на більше ресурсів", "{{.requester_name}} просить дозволити пристроям завдання «{{.exercise_name}}» більше ресурсів, ніж дає платформа: {{.devices}}.", ""},
	{"exercise.elevation.approved", "Більше ресурсів схвалено", "Пристроям завдання «{{.exercise_name}}» дозволено більше ресурсів: {{.devices}}.", ""},
	{"exercise.elevation.rejected", "Запит на ресурси відхилено", "Запит на більше ресурсів для завдання «{{.exercise_name}}» відхилено.", ""},
}

var en = []Template{
	{"flag_accepted", "Flag accepted", "«{{.Challenge}}»: {{.Points}} points.", ""},
	{"event.manager.assigned", "You now have access to an event", "You have been assigned as {{.role_name}} of the event «{{.event_name}}».", ""},
	{"event.lab.failed", "A lab is down", "Team «{{.team_name}}», event «{{.event_name}}». Check the lab on the «Labs» page.", ""},
	{"participant.approval_registration.submitted", "Application received", "Your application to «{{.event_name}}» is received. Wait for the decision.", ""},
	{"participant.approval_registration.approved", "Participation approved", "Your application to «{{.event_name}}» is approved.", "{{.event_url}}"},
	{"participant.approval_registration.rejected", "Application declined", "Your application to «{{.event_name}}» was declined.", ""},
	{"participant.open_registration.completed", "You are registered", "You are registered for «{{.event_name}}».", "{{.event_url}}"},
	{"participant.invitation.sent", "You are invited to an event", "You are invited to «{{.event_name}}».", "{{.invite_url}}"},
	{"participant.team_invitation.sent", "Team invitation", "You are invited to team «{{.team_name}}» at «{{.event_name}}».", "{{.invite_url}}"},
	{"participant.invitation.accepted", "Invitation accepted", "You accepted the invitation to «{{.event_name}}».", "{{.event_url}}"},
	{"participant.invitation.revoked", "Invitation withdrawn", "The invitation to «{{.event_name}}» was withdrawn.", ""},
	{"participant.invitation.expired", "Invitation expired", "The invitation to «{{.event_name}}» is no longer valid.", ""},
	{"participant.event.start_reminder", "The event starts soon", "«{{.event_name}}» starts {{.start_at}} (Kyiv time).", "{{.event_url}}"},
	{"participant.event.finished", "The event has ended", "Thank you for taking part in «{{.event_name}}»!", "{{.event_url}}"},
	{"participant.event.results_published", "Results are open", "The results of «{{.event_name}}» are now available.", "{{.event_url}}"},
	{"exercise.elevation.requested", "More resources requested", "{{.requester_name}} asks to give devices of the task «{{.exercise_name}}» more than the platform frame: {{.devices}}.", ""},
	{"exercise.elevation.approved", "More resources approved", "Devices of the task «{{.exercise_name}}» may use more resources: {{.devices}}.", ""},
	{"exercise.elevation.rejected", "Resource request declined", "The request for more resources for the task «{{.exercise_name}}» was declined.", ""},
}
