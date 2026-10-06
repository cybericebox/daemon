package eventFormModel

import (
	"fmt"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// MissingRequired lists the keys of the required questions the answers leave
// empty, in form order. Questions hidden by their condition and staff-only
// questions never count: the check is what a participant can be asked to fill.
func (f Form) MissingRequired(answers map[string]any) []string {
	values := make(map[string]any, len(answers))
	fields := make(map[string]eventContentModel.Block)
	missing := []string{}
	for _, block := range f.Document.Blocks {
		if block.Type != eventContentModel.BlockField {
			continue
		}
		fields[block.Key] = block
		if block.StaffOnly {
			continue
		}
		shown, err := visible(block.Condition, fields, values)
		if err != nil || !shown {
			continue
		}
		value, submitted := answers[block.Key]
		if submitted && !empty(value) {
			values[block.Key] = value
			continue
		}
		if submitted {
			values[block.Key] = value
		} else if block.Input == "checkbox" {
			values[block.Key] = false
		}
		if block.Required {
			missing = append(missing, block.Key)
		}
	}
	return missing
}

// StaffKeys is the set of staff-only question keys.
func (f Form) StaffKeys() map[string]bool {
	out := make(map[string]bool)
	for _, block := range f.Document.Blocks {
		if block.Type == eventContentModel.BlockField && block.StaffOnly {
			out[block.Key] = true
		}
	}
	return out
}

// ForParticipant is the form as a participant may see it: staff-only
// questions are removed. The receiver is not modified.
func (f Form) ForParticipant() Form {
	out := f
	out.Document = f.Document.WithoutStaffOnly()
	return out
}

// WithoutStaffAnswers copies answers without the staff-only keys.
func (f Form) WithoutStaffAnswers(answers map[string]any) map[string]any {
	staff := f.StaffKeys()
	out := make(map[string]any, len(answers))
	for key, value := range answers {
		if !staff[key] {
			out[key] = value
		}
	}
	return out
}

// KeepStaffAnswers returns submitted (a participant's or a captain's answers,
// staff keys dropped) with the stored staff-only values put back, so a
// participant saving their answers never erases what organizers recorded.
func (f Form) KeepStaffAnswers(stored, submitted map[string]any) map[string]any {
	out := f.WithoutStaffAnswers(submitted)
	for key := range f.StaffKeys() {
		if value, ok := stored[key]; ok {
			out[key] = value
		}
	}
	return out
}

// ValidateStaffAnswers checks the staff-only values an organizer saves: only
// staff-only questions are accepted, each of the right type, none is required.
func (f Form) ValidateStaffAnswers(answers map[string]any) error {
	fields := make(map[string]eventContentModel.Block)
	for _, block := range f.Document.Blocks {
		if block.Type == eventContentModel.BlockField && block.StaffOnly {
			fields[block.Key] = block
		}
	}
	for key, value := range answers {
		block, ok := fields[key]
		if !ok {
			return fmt.Errorf("%q is not a staff-only field", key)
		}
		if empty(value) {
			continue
		}
		if err := validateValue(block, value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAnswersKeepingGaps is ValidateAnswers for someone who answered
// before a required question existed: a required question they had left
// empty in stored may stay empty, while clearing one they had filled is still
// refused. Values are always type-checked.
func (f Form) ValidateAnswersKeepingGaps(answers, stored map[string]any) error {
	return f.validateAnswers(answers, func(key string) bool {
		value, had := stored[key]
		return !had || empty(value)
	})
}
