package eventself

import (
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
)

func nonNilKeys(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}

// participantVisibleAnswers admits only current participant-visible questions:
// removed staff-only answers stay private even if a use case returned them.
func participantVisibleAnswers(document eventContentModel.Document, answers map[string]any) map[string]any {
	return (eventFormModel.Form{Document: document}).AnswersForParticipant(answers)
}
