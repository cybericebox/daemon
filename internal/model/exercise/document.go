package exerciseModel

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gofrs/uuid"
)

// NormalizeTaskKeys gives every task of the variant without a key a stable one: "task_<position>" (1-based), or the
// next free number when an author's key already took that name. A key that is set is never changed. Idempotent.
func (v *Variant) NormalizeTaskKeys() {
	taken := make(map[string]struct{}, len(v.Tasks))
	for _, task := range v.Tasks {
		if task.Key != "" {
			taken[task.Key] = struct{}{}
		}
	}
	for i := range v.Tasks {
		if v.Tasks[i].Key != "" {
			continue
		}
		for n := i + 1; ; n++ {
			candidate := "task_" + strconv.Itoa(n)
			if _, used := taken[candidate]; used {
				continue
			}
			v.Tasks[i].Key = candidate
			taken[candidate] = struct{}{}
			break
		}
	}
}

// NormalizeTaskKeys applies Variant.NormalizeTaskKeys to every variant of a set.
func NormalizeTaskKeys(variants []Variant) {
	for i := range variants {
		variants[i].NormalizeTaskKeys()
	}
}

// ValidateDocument checks one variant against the document contract without asking for completeness (no
// description, no hint texts): task keys and requires, the check of each task, the variables and every
// substitution. The runtime keys of a device are checked with the topology (validateStructure).
func (v Variant) ValidateDocument() error {
	if err := validateVariables(v.Topology.Variables); err != nil {
		return err
	}
	keys := make(map[string]int, len(v.Tasks))
	for i, task := range v.Tasks {
		if task.Key == "" {
			continue
		}
		if !ValidKey(task.Key) {
			return ErrTaskKeyInvalid.WithPublicContext("task", task.Name).
				WithPublicContext("key", task.Key).
				Err()
		}
		if _, duplicate := keys[task.Key]; duplicate {
			return ErrTaskKeyInvalid.WithPublicContext("task", task.Name).
				WithPublicContext("key", task.Key).
				Err()
		}
		keys[task.Key] = i
	}
	for _, task := range v.Tasks {
		if err := validateTaskTexts(task); err != nil {
			return err
		}
		if err := v.validateTaskCheck(task, false); err != nil {
			return err
		}
		if err := validateRequires(task, keys); err != nil {
			return err
		}
	}
	if err := requiresCycle(v.Tasks, keys); err != nil {
		return err
	}
	return v.ValidateSubstitutions()
}

// validateTaskTexts refuses a substitution ({{task.<key>}}, {{var.<name>}}, or a malformed one) in the text a participant
// reads: the description and the hints. A substitution would put an answer or an infrastructure value in front of every
// reader; only placeholders ({{key}} of the task's own placeholders) belong there, and \{{ writes a literal {{. The error
// names the task and the field: description, or hints[N].text.
func validateTaskTexts(task Task) error {
	bad := func(field string) error {
		return ErrSubstitutionNotAllowed.WithPublicContext("task", task.Name).WithPublicContext("field", field).Err()
	}
	if hasSubstitutionInJSON(task.Description) {
		return bad("description")
	}
	for i, hint := range task.Hints {
		if hasSubstitution(hint.Text) && textHasSubstitution(hint.Text) {
			return bad(fmt.Sprintf("hints[%d].text", i+1))
		}
	}
	return nil
}

// textHasSubstitution reports a well-formed or malformed substitution in a text (an escaped \{{ is not one).
func textHasSubstitution(s string) bool {
	refs, err := SubstitutionRefs(s)
	return err != nil || len(refs) > 0
}

// hasSubstitutionInJSON reports a substitution in any text value of a rich-text document (keys and structure are not text).
func hasSubstitutionInJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var document any
	if json.Unmarshal(raw, &document) != nil {
		return false // the shape of the description is checked elsewhere
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch node := value.(type) {
		case string:
			return hasSubstitution(node) && textHasSubstitution(node)
		case map[string]any:
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(document)
}

func validateRequires(task Task, keys map[string]int) error {
	bad := func(key string) error {
		return ErrTaskRequiresInvalid.WithPublicContext("task", task.Name).
			WithPublicContext("key", key).
			Err()
	}
	if len(task.Requires) > MaxTaskRequires {
		return bad("")
	}
	seen := make(map[string]struct{}, len(task.Requires))
	for _, key := range task.Requires {
		if _, known := keys[key]; !known || key == task.Key {
			return bad(key)
		}
		if _, duplicate := seen[key]; duplicate {
			return bad(key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// requiresCycle reports the first task that waits, through other tasks, for itself.
func requiresCycle(tasks []Task, keys map[string]int) error {
	const (
		fresh = iota
		active
		done
	)
	state := make([]int, len(tasks))
	var visit func(i int) error
	visit = func(i int) error {
		state[i] = active
		for _, key := range tasks[i].Requires {
			next, known := keys[key]
			if !known {
				continue
			}
			switch state[next] {
			case active:
				return ErrTaskRequiresInvalid.WithPublicContext("task", tasks[i].Name).
					WithPublicContext("key", key).
					Err()
			case fresh:
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		state[i] = done
		return nil
	}
	for i := range tasks {
		if state[i] == fresh {
			if err := visit(i); err != nil {
				return err
			}
		}
	}
	return nil
}

// RequireChecks is the publish rule: every task has exactly one of answer and state. A draft may leave a task
// without one.
func (v Variant) RequireChecks() error {
	for _, task := range v.Tasks {
		if task.Check.Kind() == "" {
			return ErrTaskCheckInvalid.WithPublicContext("task", task.Name).Err()
		}
	}
	return nil
}

// validateTaskCheck: at most one of answer and state (exactly one when required), each well-formed; a condition
// looks at a node device.
func (v Variant) validateTaskCheck(task Task, required bool) error {
	if task.Check.Answer != nil && task.Check.State != nil {
		return ErrTaskCheckInvalid.WithPublicContext("task", task.Name).Err()
	}
	switch task.Check.Kind() {
	case "answer":
		if field := validAnswerShape(*task.Check.Answer); field != "" {
			if field == "values" && task.Check.Answer.Type == AnswerTypeFlag {
				return ErrFlagSourceInvalid.WithContext("task", task.Name).Err()
			}
			return ErrAnswerInvalid.WithPublicContext("task", task.Name).
				WithPublicContext("field", "check.answer."+field).
				Err()
		}
	case "state":
		if field := validateStateShape(*task.Check.State); field != "" {
			return ErrStateInvalid.WithPublicContext("task", task.Name).
				WithPublicContext("field", "check.state."+field).
				Err()
		}
		nodes := make(map[uuid.UUID]struct{}, len(v.Topology.Devices))
		for _, device := range v.Topology.Devices {
			if device.IsNode() {
				nodes[device.ID] = struct{}{}
			}
		}
		for i, c := range task.Check.State.Conditions {
			if _, ok := nodes[c.DeviceID]; !ok {
				return ErrStateInvalid.WithPublicContext("task", task.Name).
					WithPublicContext("field", fmt.Sprintf("check.state.conditions[%d].device", i+1)).
					Err()
			}
		}
	default:
		if required {
			return ErrTaskCheckInvalid.WithPublicContext("task", task.Name).Err()
		}
	}
	return nil
}

// validateChecksAcross: all variants of a version have the same kind of check, and the same answer type, at each
// task position.
func validateChecksAcross(variants []Variant) error {
	if len(variants) < 2 {
		return nil
	}
	canonical := variants[0].Tasks
	for _, variant := range variants[1:] {
		for i, task := range variant.Tasks {
			if i >= len(canonical) {
				break
			}
			base := canonical[i].Check
			if task.Check.Kind() != base.Kind() ||
				(task.Check.Answer != nil && base.Answer != nil && task.Check.Answer.Type != base.Answer.Type) {
				return ErrTaskCheckInvalid.WithPublicContext("task", task.Name).Err()
			}
		}
	}
	return nil
}

// StateTasks lists the tasks of the variant that are checked by a lab state.
func (v Variant) StateTasks() []Task {
	var out []Task
	for _, task := range v.Tasks {
		if task.Check.State != nil {
			out = append(out, task)
		}
	}
	return out
}

// TaskKeyByID maps task ids to keys.
func (v Variant) TaskKeyByID() map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(v.Tasks))
	for _, task := range v.Tasks {
		out[task.ID] = task.Key
	}
	return out
}

// HasStateTasks reports whether any variant of the version has a task checked by the state of the lab.
func (v *ExerciseVersion) HasStateTasks() bool {
	for _, variant := range v.Variants {
		if len(variant.StateTasks()) > 0 {
			return true
		}
	}
	return false
}

// RequireNoStateTasks refuses a version that has a task checked by the state of the lab: that kind belongs to the Academy
// and cannot be part of an event (a competition). The public context names the first such task.
func (v *ExerciseVersion) RequireNoStateTasks() error {
	for _, variant := range v.Variants {
		if tasks := variant.StateTasks(); len(tasks) > 0 {
			return ErrStateTaskNotForEvent.WithPublicContext("task", tasks[0].Name).Err()
		}
	}
	return nil
}
