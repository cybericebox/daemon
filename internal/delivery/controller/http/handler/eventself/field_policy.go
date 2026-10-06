package eventself

import (
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func nonNilKeys(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}

// participantVisibleAnswers drops the answers of staff-only questions of the
// form: whatever the use case returned, a participant never receives them.
func participantVisibleAnswers(document eventContentModel.Document, answers map[string]any) map[string]any {
	staff := make(map[string]bool)
	for _, block := range document.Blocks {
		if block.Type == eventContentModel.BlockField && block.StaffOnly {
			staff[block.Key] = true
		}
	}
	out := make(map[string]any, len(answers))
	for key, value := range answers {
		if !staff[key] {
			out[key] = value
		}
	}
	return out
}
