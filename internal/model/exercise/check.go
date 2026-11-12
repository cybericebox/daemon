package exerciseModel

import (
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// Limits of the check of a task. Each is part of the document contract (cib-exercise/1.0) and is repeated in the
// commented example of the archive format.
const (
	// MaxAnswerValues bounds the values of one text answer.
	MaxAnswerValues = 100
	// MaxTextAnswerRunes bounds one fixed text value (and one template pattern).
	MaxTextAnswerRunes = 256
	// MaxInputHintRunes bounds the hint shown inside the answer field.
	MaxInputHintRunes = 100
	// MaxTaskRequires bounds the tasks one task waits for.
	MaxTaskRequires = 32
	// MaxStateConditions bounds the conditions of one state.
	MaxStateConditions = 20
	// MinStateInterval and MaxStateInterval bound the schedule of a state check; 0 or absent is no schedule.
	MinStateInterval = 5 * time.Second
	MaxStateInterval = 24 * time.Hour
	// MinConditionTimeout and MaxConditionTimeout bound the time limit of one condition (absent: the lab's default).
	MinConditionTimeout = time.Second
	MaxConditionTimeout = time.Minute
	// MaxExpectationBytes bounds one expected text (contains, matches, output_contains, output_matches).
	MaxExpectationBytes = 1024
	// MaxCustomRunBytes bounds the command of a custom observation.
	MaxCustomRunBytes = 8192
	// MaxPathBytes bounds a path inside a device.
	MaxPathBytes = 4096
	// MaxInterpreterBytes bounds the path of an interpreter or a shell.
	MaxInterpreterBytes = 256
)

// AnswerType is how the participant's typed value is read.
type AnswerType string

const (
	// AnswerTypeFlag is a value of the form ICE{...}.
	AnswerTypeFlag AnswerType = "flag"
	// AnswerTypeText is free text.
	AnswerTypeText AnswerType = "text"
)

func (t AnswerType) Valid() bool { return t == AnswerTypeFlag || t == AnswerTypeText }

// StateMode says how long a state check runs.
type StateMode string

const (
	// StateModeOnce stops after the first pass (the default).
	StateModeOnce StateMode = "once"
	// StateModeContinuous makes the current state count.
	StateModeContinuous StateMode = "continuous"
)

func (m StateMode) Valid() bool { return m == "" || m == StateModeOnce || m == StateModeContinuous }

type (
	// Check is what makes a task done: exactly one of an answer the participant types or a state the lab checks.
	Check struct {
		Answer *AnswerCheck `json:"answer,omitempty"`
		State  *StateCheck  `json:"state,omitempty"`
	}

	// AnswerCheck is the check of a task answered by a typed value; credit comes only from an exact match with the value pinned to
	// their variant or instance. Values: none (type flag only) = a random flag per instance, one = that value,
	// several = one is pinned per instance, "template:<pattern>" = generated per instance.
	AnswerCheck struct {
		Type   AnswerType `json:"type"`
		Values []string   `json:"values,omitempty"`
		// InputHint is shown inside the answer field (both types).
		InputHint string `json:"input_hint,omitempty"`
		// IgnoreCase and Trim (text only): compare without case; drop spaces at the edges. Trim is on unless it is
		// set to false.
		IgnoreCase bool  `json:"ignore_case,omitempty"`
		Trim       *bool `json:"trim,omitempty"`
	}

	// StateCheck is the check of a task by the lab: nothing is typed, the lab checks devices and the task is
	// marked by itself.
	StateCheck struct {
		Mode StateMode `json:"mode,omitempty"`
		// Interval is a Go duration, MinStateInterval..MaxStateInterval; empty = no schedule.
		Interval string `json:"interval,omitempty"`
		// Manual: the participant may press "check now".
		Manual     bool        `json:"manual,omitempty"`
		Conditions []Condition `json:"conditions"`
	}

	// Condition is one observation on one device (always a node device of the variant); the state is reached when
	// every condition holds. Exactly one of the observations is set (see ObservationKinds).
	Condition struct {
		// DeviceID is the observed device; the document names it.
		DeviceID uuid.UUID `json:"device_id"`
		// Timeout is a Go duration, MinConditionTimeout..MaxConditionTimeout; empty = the lab's default.
		Timeout     string                  `json:"timeout,omitempty"`
		File        *FileObservation        `json:"file,omitempty"`
		Directory   *DirectoryObservation   `json:"directory,omitempty"`
		Process     *ProcessObservation     `json:"process,omitempty"`
		Port        *PortObservation        `json:"port,omitempty"`
		KernelParam *KernelParamObservation `json:"kernel_param,omitempty"`
		Request     *RequestObservation     `json:"request,omitempty"`
		Permission  *PermissionObservation  `json:"permission,omitempty"`
		Custom      *CustomObservation      `json:"custom,omitempty"`
	}

	// FileObservation looks at a file of the device. Exists is true unless set to false; false stands alone.
	FileObservation struct {
		Path     string `json:"path"`
		Exists   *bool  `json:"exists,omitempty"`
		Type     string `json:"type,omitempty"` // file | directory | symlink
		Contains string `json:"contains,omitempty"`
		Matches  string `json:"matches,omitempty"`
		Checksum string `json:"checksum,omitempty"` // sha256 hex of the content
		Mode     string `json:"mode,omitempty"`
		Owner    string `json:"owner,omitempty"`
		Group    string `json:"group,omitempty"`
	}

	// CustomObservation runs a command inside the device: exit code 0 is "passed" unless an output rule is given.
	CustomObservation struct {
		Run            string `json:"run"`
		Interpreter    string `json:"interpreter,omitempty"`
		User           string `json:"user,omitempty"`
		OutputContains string `json:"output_contains,omitempty"`
		OutputMatches  string `json:"output_matches,omitempty"`
	}
)

// values are the answer values of the check (nil for a state check).
func (c Check) values() []string {
	if c.Answer == nil {
		return nil
	}
	return c.Answer.Values
}

// AnswerValues are the answer values of the check, nil for a state check.
func (c Check) AnswerValues() []string { return c.values() }

// IsAnswer reports whether the task is answered by a typed value.
func (c Check) IsAnswer() bool { return c.Answer != nil }

// IsState reports whether the task is checked by the state of the lab.
func (c Check) IsState() bool { return c.State != nil }

// Kind names the check for messages and comparisons: "answer", "state" or "" (none or both).
func (c Check) Kind() string {
	switch {
	case c.Answer != nil && c.State == nil:
		return "answer"
	case c.State != nil && c.Answer == nil:
		return "state"
	}
	return ""
}

// TrimEffective reports whether a text answer drops the spaces at the edges: it does unless trim is false.
func (a AnswerCheck) TrimEffective() bool { return a.Trim == nil || *a.Trim }

// Set parses the values of the answer into the numbered set the event draws from.
func (a AnswerCheck) Set() (flagpattern.Set, error) {
	if a.Type == AnswerTypeText {
		return flagpattern.ParseTextSet(a.Values)
	}
	return flagpattern.ParseSet(a.Values)
}

// Resolve draws the value pinned for one instance: a random flag when a flag answer has no values, otherwise one of
// the values (a template is generated). randomBytes sizes the generated flag.
func (a AnswerCheck) Resolve(randomBytes int, source io.Reader) (string, error) {
	if a.Type == AnswerTypeText {
		return flagpattern.ResolveText(a.Values, source)
	}
	return flagpattern.Resolve(a.Values, randomBytes, source)
}

// Generated reports whether the pinned value is a random flag made per instance (a flag answer without values).
func (a AnswerCheck) Generated() bool { return len(a.Values) == 0 }

// validAnswerShape checks one answer on its own (no cross references). It returns the document key that is wrong,
// or "" when it is well-formed.
func validAnswerShape(a AnswerCheck) string {
	if !a.Type.Valid() {
		return "type"
	}
	if utf8.RuneCountInString(a.InputHint) > MaxInputHintRunes ||
		strings.ContainsFunc(a.InputHint, unicode.IsControl) {
		return "input_hint"
	}
	if a.Type == AnswerTypeFlag {
		if a.IgnoreCase {
			return "ignore_case"
		}
		if a.Trim != nil {
			return "trim"
		}
		if !validFlag(a.Values) {
			return "values"
		}
		return ""
	}
	if len(a.Values) == 0 || len(a.Values) > MaxAnswerValues {
		return "values"
	}
	seen := make(map[string]struct{}, len(a.Values))
	for _, value := range a.Values {
		if utf8.RuneCountInString(value) > MaxTextAnswerRunes || strings.TrimSpace(value) == "" {
			return "values"
		}
		if _, err := flagpattern.ParseText(value); err != nil {
			return "values"
		}
		if _, duplicate := seen[value]; duplicate {
			return "values"
		}
		seen[value] = struct{}{}
	}
	return ""
}

var (
	// accountNameRe is a user or group name inside a device, or a numeric id.
	accountNameRe = regexp.MustCompile(`^([a-z_][a-z0-9_-]{0,31}|[0-9]{1,10})$`)
	fileModeRe    = regexp.MustCompile(`^[0-7]{3,4}$`)
)

// validAbsolutePath: a clean absolute path inside a device.
func validAbsolutePath(p string) bool {
	if p == "" || len(p) > MaxPathBytes || p[0] != '/' || p == "/" ||
		strings.ContainsFunc(p, unicode.IsControl) {
		return false
	}
	for _, part := range strings.Split(p[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validInterpreter(p string) bool {
	return p != "" && len(p) <= MaxInterpreterBytes && p[0] == '/' &&
		!strings.ContainsFunc(p, unicode.IsSpace) &&
		!strings.ContainsFunc(p, unicode.IsControl)
}

func validDurationIn(value string, minimum, maximum time.Duration) bool {
	parsed, err := time.ParseDuration(value)
	return err == nil && parsed >= minimum && parsed <= maximum
}

// validExpectationText: an expected text is bounded and has no control characters other than a line break or a
// tab (a command output can span lines).
func validExpectationText(s string) bool {
	if len(s) > MaxExpectationBytes {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

// validExpectationRegexp: a bounded text that compiles once its substitutions are replaced by a stand-in.
func validExpectationRegexp(s string) bool {
	if !validExpectationText(s) {
		return false
	}
	_, err := regexp.Compile(substitutionStandIn(s))
	return err == nil
}

// validateState checks the shape of one state check on its own; the device of a condition is resolved by the
// caller. It returns the document key that is wrong ("conditions[2].file.matches"), or "".
func validateStateShape(s StateCheck) string {
	if !s.Mode.Valid() {
		return "mode"
	}
	if s.Interval != "" && s.Interval != "0" &&
		!validDurationIn(s.Interval, MinStateInterval, MaxStateInterval) {
		return "interval"
	}
	if len(s.Conditions) == 0 || len(s.Conditions) > MaxStateConditions {
		return "conditions"
	}
	for i, c := range s.Conditions {
		if where := validConditionShape(c); where != "" {
			return "conditions[" + strconv.Itoa(i+1) + "]." + where
		}
	}
	return ""
}

func validConditionShape(c Condition) string {
	if c.Timeout != "" && !validDurationIn(c.Timeout, MinConditionTimeout, MaxConditionTimeout) {
		return "timeout"
	}
	if len(c.Kinds()) != 1 {
		return "observation"
	}
	if c.File == nil && c.Custom == nil {
		return validObservationShape(c)
	}
	if f := c.File; f != nil {
		switch {
		case !validAbsolutePath(substitutionStandIn(f.Path)):
			return "file.path"
		case f.Type != "" && f.Type != "file" && f.Type != "directory" && f.Type != "symlink":
			return "file.type"
		case f.Checksum != "" && !hasSubstitution(f.Checksum) && !checksumRe.MatchString(f.Checksum):
			return "file.checksum"
		case f.Type != "" && f.Type != "file" && (f.Contains != "" || f.Matches != "" || f.Checksum != ""):
			return "file.type"
		case !validExpectationText(f.Contains):
			return "file.contains"
		case !validExpectationRegexp(f.Matches):
			return "file.matches"
		case f.Mode != "" && !fileModeRe.MatchString(f.Mode):
			return "file.mode"
		case f.Owner != "" && !accountNameRe.MatchString(f.Owner):
			return "file.owner"
		case f.Group != "" && !accountNameRe.MatchString(f.Group):
			return "file.group"
		case f.Exists != nil && !*f.Exists && (f.Type != "" || f.Contains != "" || f.Matches != "" || f.Checksum != "" || f.Mode != "" || f.Owner != "" || f.Group != ""):
			return "file.exists"
		}
		return ""
	}
	o := c.Custom
	switch {
	case strings.TrimSpace(o.Run) == "" || len(o.Run) > MaxCustomRunBytes || strings.ContainsRune(o.Run, 0):
		return "custom.run"
	case o.Interpreter != "" && !validInterpreter(o.Interpreter):
		return "custom.interpreter"
	case o.User != "" && !accountNameRe.MatchString(o.User):
		return "custom.user"
	case !validExpectationText(o.OutputContains):
		return "custom.output_contains"
	case !validExpectationRegexp(o.OutputMatches):
		return "custom.output_matches"
	}
	return ""
}

// WithoutSolution is the task without what solves it: the answer values and the conditions of a state. The task
// itself is not changed.
func (t Task) WithoutSolution() Task {
	if t.Check.Answer != nil {
		answer := *t.Check.Answer
		answer.Values = nil
		t.Check.Answer = &answer
	}
	if t.Check.State != nil {
		state := *t.Check.State
		state.Conditions = nil
		t.Check.State = &state
	}
	return t
}

// FlagCheck is the check of a flag answer with the given values (none: a random flag per instance).
func FlagCheck(values ...string) Check {
	return Check{Answer: &AnswerCheck{Type: AnswerTypeFlag, Values: values}}
}

// TextCheck is the check of a text answer with the given values.
func TextCheck(values ...string) Check {
	return Check{Answer: &AnswerCheck{Type: AnswerTypeText, Values: values}}
}
