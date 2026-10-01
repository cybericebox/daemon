package eventFormModel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// Trigger determines when an assignment becomes eligible to create deliveries.
type Trigger string

const (
	TriggerRegistrationOpenCompleted     Trigger = "registration_open_completed"
	TriggerRegistrationApprovalSubmitted Trigger = "registration_approval_submitted"
	TriggerEventFinished                 Trigger = "event_finished"
	TriggerAtTime                        Trigger = "at_time"
	TriggerManual                        Trigger = "manual"
)

// AudienceKind is a declarative recipient selector resolved by the event use case.
type AudienceKind string

const (
	AudienceSignalSubject      AudienceKind = "signal_subject"
	AudienceAllParticipants    AudienceKind = "all_participants"
	AudienceAllCaptains        AudienceKind = "all_captains"
	AudienceSelectedUsers      AudienceKind = "selected_users"
	AudienceSelectedTeams      AudienceKind = "selected_teams"
	AudienceParticipantsNoTeam AudienceKind = "participants_without_team"
	AudienceTeamsBelowSize     AudienceKind = "teams_below_size"
)

// Audience keeps selector-specific values serializable without tying the domain
// model to a transport DTO. IDs and threshold validation happen at resolution.
type Audience struct {
	Kind      AudienceKind `json:"kind"`
	UserIDs   []uuid.UUID  `json:"user_ids,omitempty"`
	TeamIDs   []uuid.UUID  `json:"team_ids,omitempty"`
	BelowSize *int32       `json:"below_size,omitempty"`
}

// Presentation tells the client how to surface a pending delivery.
type Presentation string

const (
	PresentationModal  Presentation = "modal"
	PresentationBanner Presentation = "banner"
	PresentationTask   Presentation = "task"
)

// Capability is an action that a required, incomplete delivery may block.
type Capability string

const (
	CapabilityTeamJoinOrCreate Capability = "team.join_or_create"
	CapabilityTeamManage       Capability = "team.manage"
	CapabilityChallengeSubmit  Capability = "challenge.submit"
	CapabilityLabAccess        Capability = "lab.access"
)

// Assignment describes a reusable, manager-configured delivery rule. IDs and
// event ownership are held by the repository layer; this type validates the
// configuration independent of storage or transport.
type Assignment struct {
	Trigger      Trigger
	Audience     Audience
	Presentation Presentation
	Dismissible  bool
	At           *time.Time
	Gates        []Capability
}

func (a Assignment) Validate() error {
	if !validTrigger(a.Trigger) {
		return fmt.Errorf("unsupported form trigger %q", a.Trigger)
	}
	if a.Trigger == TriggerAtTime && a.At == nil {
		return errors.New("timed form assignment requires a time")
	}
	if a.Trigger != TriggerAtTime && a.At != nil {
		return errors.New("only timed form assignment may have a time")
	}
	if !validAudience(a.Audience.Kind) {
		return fmt.Errorf("unsupported form audience %q", a.Audience.Kind)
	}
	if err := a.Audience.Validate(); err != nil {
		return err
	}
	if !validPresentation(a.Presentation) {
		return fmt.Errorf("unsupported form presentation %q", a.Presentation)
	}
	for _, capability := range a.Gates {
		if !validCapability(capability) {
			return fmt.Errorf("unsupported form capability %q", capability)
		}
	}
	return nil
}

func (a Audience) Validate() error {
	switch a.Kind {
	case AudienceSelectedUsers:
		if len(a.UserIDs) == 0 {
			return errors.New("selected-users audience requires users")
		}
	case AudienceSelectedTeams:
		if len(a.TeamIDs) == 0 {
			return errors.New("selected-teams audience requires teams")
		}
	case AudienceTeamsBelowSize:
		if a.BelowSize == nil || *a.BelowSize < 1 {
			return errors.New("teams-below-size audience requires a positive threshold")
		}
	}
	return nil
}

func validTrigger(v Trigger) bool {
	switch v {
	case TriggerRegistrationOpenCompleted, TriggerRegistrationApprovalSubmitted, TriggerEventFinished, TriggerAtTime, TriggerManual:
		return true
	default:
		return false
	}
}

func validAudience(v AudienceKind) bool {
	switch v {
	case AudienceSignalSubject, AudienceAllParticipants, AudienceAllCaptains, AudienceSelectedUsers, AudienceSelectedTeams, AudienceParticipantsNoTeam, AudienceTeamsBelowSize:
		return true
	default:
		return false
	}
}

func validPresentation(v Presentation) bool {
	return v == PresentationModal || v == PresentationBanner || v == PresentationTask
}

func validCapability(v Capability) bool {
	switch v {
	case CapabilityTeamJoinOrCreate, CapabilityTeamManage, CapabilityChallengeSubmit, CapabilityLabAccess:
		return true
	default:
		return false
	}
}

// Form is an immutable versioned participant questionnaire. The use case
// creates a new version instead of editing an existing schema, so submitted
// responses always retain their original interpretation.
type Form struct {
	Version  int32
	Enabled  bool
	Required bool
	Document eventContentModel.Document
	// RequireExisting makes the required fields of this version count for
	// people who answered an older version too (they are asked to fill the new
	// ones, never locked out). BlockSubmissions additionally rejects solution
	// submissions until they did. Both are ignored when RequireExisting is off.
	RequireExisting  bool
	BlockSubmissions bool
}

// Validate checks a questionnaire an organizer saves: the shared document
// rules plus the question rules the editor enforces (filled, unique options;
// conditions on an earlier single-answer question with a value of its type).
// Answers are checked against Document.Validate only, so a version saved
// before these rules keeps accepting submissions.
func (f Form) Validate() error {
	if err := f.Document.Validate(); err != nil {
		return err
	}
	fields := make(map[string]eventContentModel.Block)
	for _, block := range f.Document.Blocks {
		if block.Type != eventContentModel.BlockField {
			continue
		}
		if err := validateOptions(block); err != nil {
			return err
		}
		if block.StaffOnly && block.Input == InputFile {
			return fmt.Errorf("field %q: a staff-only field cannot be a file", block.Key)
		}
		if block.Input == InputFile {
			if err := validateFileQuestion(block); err != nil {
				return err
			}
		}
		if block.Input == InputDate {
			if err := validateDateQuestion(block); err != nil {
				return err
			}
		}
		if block.Condition != nil {
			if err := validateCondition(*block.Condition, fields); err != nil {
				return fmt.Errorf("field %q: %w", block.Key, err)
			}
			if source := fields[block.Condition.FieldKey]; source.StaffOnly && !block.StaffOnly {
				return fmt.Errorf("field %q: a participant field cannot depend on a staff-only field", block.Key)
			}
		}
		fields[block.Key] = block
	}
	return nil
}

func validateOptions(block eventContentModel.Block) error {
	if block.Input != "select" && block.Input != "multi_select" {
		return nil
	}
	seen := make(map[string]struct{}, len(block.Options))
	for _, option := range block.Options {
		value := strings.TrimSpace(option)
		if value == "" {
			return fmt.Errorf("field %q has an empty option", block.Key)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("field %q repeats option %q", block.Key, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateCondition(condition eventContentModel.Condition, fields map[string]eventContentModel.Block) error {
	source, ok := fields[condition.FieldKey]
	if !ok {
		return errors.New("condition must reference a preceding field")
	}
	comparison := condition.Operator == "equals" || condition.Operator == "not_equals"
	ordering := condition.Operator == OperatorBefore || condition.Operator == OperatorAfter
	if !comparison && !(ordering && source.Input == InputDate) {
		return errors.New("condition operator is unsupported")
	}
	switch source.Input {
	case InputDate:
		if _, ok := dateValue(source, condition.Value); !ok {
			return errors.New("condition value must be a date of the source field")
		}
	case "multi_select", InputFile:
		return errors.New("condition source must have a single comparable answer")
	case "checkbox":
		if _, ok := condition.Value.(bool); !ok {
			return errors.New("condition value must be boolean")
		}
	case "number":
		if _, ok := condition.Value.(float64); !ok {
			return errors.New("condition value must be a number")
		}
	case "select":
		value, ok := condition.Value.(string)
		if !ok || !contains(source.Options, value) {
			return errors.New("condition value must be an option of the source field")
		}
	default:
		value, ok := condition.Value.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return errors.New("condition value is required")
		}
	}
	return nil
}

func (f Form) ValidateAnswers(answers map[string]any) error {
	return f.validateAnswers(answers, nil)
}

// validateAnswers checks answers; a required question is not demanded when
// tolerate reports its key (nil tolerates nothing).
func (f Form) validateAnswers(answers map[string]any, tolerate func(key string) bool) error {
	if !f.Enabled {
		return errors.New("form is disabled")
	}
	if err := f.Document.Validate(); err != nil {
		return err
	}
	// values holds the answers of the questions the participant sees; a
	// question hidden by its condition hides everything that depends on it.
	values := make(map[string]any, len(answers))
	fields := make(map[string]eventContentModel.Block)
	for _, block := range f.Document.Blocks {
		if block.Type != eventContentModel.BlockField {
			continue
		}
		fields[block.Key] = block
		visible, err := visible(block.Condition, fields, values)
		if err != nil {
			return err
		}
		if !visible {
			continue
		}
		value, submitted := answers[block.Key]
		if block.Required && !block.StaffOnly && (!submitted || empty(value)) && (tolerate == nil || !tolerate(block.Key)) {
			return fmt.Errorf("required field %q is missing", block.Key)
		}
		if submitted {
			if err := validateValue(block, value); err != nil {
				return err
			}
			values[block.Key] = value
		} else if block.Input == "checkbox" {
			// An untouched checkbox is «Ні» for the questions that depend on it.
			values[block.Key] = false
		}
	}
	return nil
}

// BlocksParticipation reports only the event-level onboarding gate. An
// optional form may still be submitted and validated, but its absence never
// blocks teams, tasks, labs, or submissions.
func (f Form) BlocksParticipation(hasResponse bool) bool {
	return f.Enabled && f.Required && !hasResponse
}

func visible(condition *eventContentModel.Condition, fields map[string]eventContentModel.Block, values map[string]any) (bool, error) {
	if condition == nil {
		return true, nil
	}
	value, exists := values[condition.FieldKey]
	if !exists {
		return false, nil
	}
	if source := fields[condition.FieldKey]; source.Input == InputDate {
		return compareDates(source, condition.Operator, value, condition.Value), nil
	}
	switch condition.Operator {
	case "equals":
		return fmt.Sprint(value) == fmt.Sprint(condition.Value), nil
	case "not_equals":
		return fmt.Sprint(value) != fmt.Sprint(condition.Value), nil
	default:
		return false, errors.New("condition operator is unsupported")
	}
}

func empty(value any) bool { return value == nil || fmt.Sprint(value) == "" }

func validateValue(block eventContentModel.Block, value any) error {
	switch block.Input {
	case "text", "long_text":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("field %q must be text", block.Key)
		}
	case "number":
		switch value.(type) {
		case float64, float32, int, int32, int64:
		default:
			return fmt.Errorf("field %q must be a number", block.Key)
		}
	case "checkbox":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field %q must be boolean", block.Key)
		}
	case "select":
		text, ok := value.(string)
		if !ok || !contains(block.Options, text) {
			return fmt.Errorf("field %q has unsupported option", block.Key)
		}
	case InputDate:
		if empty(value) {
			break // a cleared date; required questions fail earlier
		}
		if err := validateDateAnswer(block, value); err != nil {
			return err
		}
	case InputFile:
		if empty(value) {
			break // a cleared file answer; required questions fail earlier
		}
		if _, ok := AnswerFileID(value); !ok {
			return fmt.Errorf("field %q must reference an uploaded file", block.Key)
		}
	case "multi_select":
		values, ok := value.([]any)
		if !ok {
			return fmt.Errorf("field %q must be a list", block.Key)
		}
		for _, item := range values {
			text, ok := item.(string)
			if !ok || !contains(block.Options, text) {
				return fmt.Errorf("field %q has unsupported option", block.Key)
			}
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// FileFields returns the file questions of the form by key.
func (f Form) FileFields() map[string]eventContentModel.Block {
	out := make(map[string]eventContentModel.Block)
	for _, block := range f.Document.Blocks {
		if block.Type == eventContentModel.BlockField && block.Input == InputFile {
			out[block.Key] = block
		}
	}
	return out
}

// EditableFields maps every field key of the form to its editable flag.
func (f Form) EditableFields() map[string]bool {
	out := make(map[string]bool)
	for _, block := range f.Document.Blocks {
		if block.Type == eventContentModel.BlockField {
			out[block.Key] = block.Editable
		}
	}
	return out
}

// ValidatePartialAnswers checks the values an organizer prefills (a CSV
// import) against the field types. Unlike ValidateAnswers it does not require
// anything and ignores conditions: missing required fields are filled by the
// participant later. Unknown keys and file answers are refused.
func (f Form) ValidatePartialAnswers(answers map[string]any) error {
	fields := make(map[string]eventContentModel.Block)
	for _, block := range f.Document.Blocks {
		if block.Type == eventContentModel.BlockField {
			fields[block.Key] = block
		}
	}
	for key, value := range answers {
		block, known := fields[key]
		if !known {
			return fmt.Errorf("unknown field %q", key)
		}
		if block.Input == InputFile {
			return fmt.Errorf("field %q cannot be imported: files are uploaded by people", key)
		}
		if err := validateValue(block, value); err != nil {
			return err
		}
	}
	return nil
}
