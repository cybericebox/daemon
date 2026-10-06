package event

import (
	"sort"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// ContentVariableDefinition is the server-owned editor contract for a value
// that the current event-content renderer can resolve.
type ContentVariableDefinition struct {
	Name     string                           `json:"name"`
	Label    string                           `json:"label"`
	Format   eventContentModel.VariableFormat `json:"format"`
	Audience eventContentModel.PageVisibility `json:"audience"`
}

var contentVariableLabels = map[string]string{
	"event.name":                     "Назва заходу",
	"event.tag":                      "Тег заходу",
	"event.previewDescription":       "Короткий опис",
	"event.phase":                    "Поточний етап",
	"event.publishAt":                "Час публікації",
	"event.startAt":                  "Початок",
	"event.finishAt":                 "Завершення за розкладом",
	"event.withdrawAt":               "Закриття доступу",
	"event.manualFinishAt":           "Ручне завершення",
	"event.effectiveFinishAt":        "Фактичне завершення",
	"event.isPublished":              "Опубліковано",
	"event.isStarted":                "Розпочато",
	"event.isFinished":               "Завершено",
	"event.isWithdrawn":              "Доступ закрито",
	"event.runtimeOpen":              "Проходження відкрите",
	"event.registrationOpen":         "Реєстрація відкрита",
	"event.rosterOpen":               "Зміна команд відкрита",
	"event.participation":            "Формат участі",
	"event.registration":             "Режим реєстрації",
	"event.joinPolicy":               "Період приєднання",
	"event.maxTeamSize":              "Максимум у команді",
	"event.minTeamSize":              "Мінімум у команді",
	"event.maxTeams":                 "Кількість команд",
	"event.scoreboardVisibility":     "Видимість результатів",
	"event.participantsVisibility":   "Видимість учасників",
	"event.scoringProfile":           "Система оцінювання",
	"event.forceEventScoring":        "Єдина система балів",
	"event.teamCount":                "Усі команди",
	"event.approvedTeamCount":        "Схвалені команди",
	"event.participantCount":         "Усі учасники",
	"event.approvedParticipantCount": "Схвалені учасники",
	"event.registrationUnitCount":    "Зареєстровані одиниці",
	"event.challengeCount":           "Усі завдання",
	"event.availableChallengeCount":  "Доступні завдання",
	"event.solvedChallengeCount":     "Розвʼязані завдання",
	"event.solveCount":               "Успішні розвʼязання",
}

func ContentVariableCatalog(scoreboardVisibility eventConfigModel.Visibility) []ContentVariableDefinition {
	items := make([]ContentVariableDefinition, 0, len(eventContentModel.EventContentVariables))
	for name, format := range eventContentModel.EventContentVariables {
		audience, known := contentVariableAudiences[name]
		if !known {
			audience = eventContentModel.PageVisibilityManager
		}
		if name == "event.solveCount" || name == "event.solvedChallengeCount" {
			switch scoreboardVisibility {
			case eventConfigModel.VisibilityPublic:
				audience = eventContentModel.PageVisibilityPublic
			case eventConfigModel.VisibilityPrivate:
				audience = eventContentModel.PageVisibilityParticipant
			default:
				audience = eventContentModel.PageVisibilityManager
			}
		}
		label := contentVariableLabels[name]
		if label == "" {
			label = name
		}
		items = append(items, ContentVariableDefinition{Name: name, Label: label, Format: format, Audience: audience})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
