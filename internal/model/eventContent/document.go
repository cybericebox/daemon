package eventContentModel

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func validContentHref(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\\\r\n\t") || strings.HasPrefix(raw, "//") {
		return false
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "#") {
		return true
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

type BlockType string

const (
	BlockSection   BlockType = "section"
	BlockText      BlockType = "text"
	BlockField     BlockType = "field"
	BlockFacts     BlockType = "facts"
	BlockTimeline  BlockType = "timeline"
	BlockFAQ       BlockType = "faq"
	BlockCTA       BlockType = "cta"
	BlockCountdown BlockType = "countdown"
	BlockDivider   BlockType = "divider"
	BlockHero      BlockType = "hero"
	BlockBanner    BlockType = "banner"
	BlockDoc       BlockType = "doc"
	BlockPartners  BlockType = "partners"
)

// PartnerLogo is one logo of a partners block. Name is the logo's alt text;
// ImageURL is an uploaded event content image; Href is optional.
type PartnerLogo struct {
	Name     string `json:"name"`
	ImageURL string `json:"imageURL"`
	Href     string `json:"href,omitempty"`
}

type PartnerGroup struct {
	Title string        `json:"title,omitempty"`
	Items []PartnerLogo `json:"items"`
}

const (
	maxPartnerGroups     = 10
	maxLogosPerGroup     = 24
	maxAnchorLength      = 64
	maxPartnerNameLength = 120
)

var anchorPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type BlockItem struct {
	Label        string          `json:"label,omitempty"`
	Value        string          `json:"value,omitempty"`
	RichText     json.RawMessage `json:"richText,omitempty"`
	DateSource   string          `json:"dateSource,omitempty"`
	DateVariable string          `json:"dateVariable,omitempty"`
	DateValue    string          `json:"dateValue,omitempty"`
	DateFormat   string          `json:"dateFormat,omitempty"`
	DatePattern  string          `json:"datePattern,omitempty"`
}

type BlockAction struct {
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
	Href  string `json:"href,omitempty"`
}

func validBlockAction(action *BlockAction) bool {
	if action == nil || strings.TrimSpace(action.Label) == "" {
		return false
	}
	switch action.Kind {
	case "", "link":
		return validContentHref(action.Href)
	case "join_event":
		return strings.TrimSpace(action.Href) == ""
	default:
		return false
	}
}

type Condition struct {
	FieldKey string `json:"fieldKey"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

// VariableFormat specifies the wire type that the renderer receives for an
// event-content variable. Formatting is deliberately left to the client so a
// page can follow its viewer's locale and timezone.
type VariableFormat string

const (
	VariableFormatText     VariableFormat = "text"
	VariableFormatNumber   VariableFormat = "number"
	VariableFormatDateTime VariableFormat = "date-time"
	VariableFormatBoolean  VariableFormat = "boolean"
)

// VariableBinding declares an event-owned value used by one block. The name
// is validated against EventContentVariables; it is not an expression path.
type VariableBinding struct {
	Name   string         `json:"name"`
	Format VariableFormat `json:"format"`
}

type DateDisplay struct {
	Format  string `json:"format"`
	Pattern string `json:"pattern,omitempty"`
}

// VisibilityCondition controls a content block using a declared event
// variable. It is deliberately distinct from Condition, which only controls a
// form field through preceding participant answers.
type VisibilityCondition struct {
	Variable string `json:"variable"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

// EventContentVariables is the complete set of values that pages may request.
// Keep this list intentionally small: content must never expose credentials,
// submissions, flags, individual teams, or manager-only data.
var EventContentVariables = map[string]VariableFormat{
	"event.name":                     VariableFormatText,
	"event.tag":                      VariableFormatText,
	"event.previewDescription":       VariableFormatText,
	"event.phase":                    VariableFormatText,
	"event.publishAt":                VariableFormatDateTime,
	"event.startAt":                  VariableFormatDateTime,
	"event.finishAt":                 VariableFormatDateTime,
	"event.withdrawAt":               VariableFormatDateTime,
	"event.manualFinishAt":           VariableFormatDateTime,
	"event.effectiveFinishAt":        VariableFormatDateTime,
	"event.isPublished":              VariableFormatBoolean,
	"event.isStarted":                VariableFormatBoolean,
	"event.isFinished":               VariableFormatBoolean,
	"event.isWithdrawn":              VariableFormatBoolean,
	"event.runtimeOpen":              VariableFormatBoolean,
	"event.registrationOpen":         VariableFormatBoolean,
	"event.rosterOpen":               VariableFormatBoolean,
	"event.participation":            VariableFormatText,
	"event.registration":             VariableFormatText,
	"event.joinPolicy":               VariableFormatText,
	"event.maxTeamSize":              VariableFormatNumber,
	"event.minTeamSize":              VariableFormatNumber,
	"event.maxTeams":                 VariableFormatNumber,
	"event.scoreboardVisibility":     VariableFormatText,
	"event.participantsVisibility":   VariableFormatText,
	"event.scoringProfile":           VariableFormatText,
	"event.forceEventScoring":        VariableFormatBoolean,
	"event.teamCount":                VariableFormatNumber,
	"event.approvedTeamCount":        VariableFormatNumber,
	"event.participantCount":         VariableFormatNumber,
	"event.approvedParticipantCount": VariableFormatNumber,
	"event.registrationUnitCount":    VariableFormatNumber,
	"event.challengeCount":           VariableFormatNumber,
	"event.availableChallengeCount":  VariableFormatNumber,
	"event.solvedChallengeCount":     VariableFormatNumber,
	"event.solveCount":               VariableFormatNumber,
}

var variableToken = regexp.MustCompile(`\{\{([a-z][a-zA-Z0-9.]*)\}\}`)
var datePatternToken = regexp.MustCompile(`yyyy|yy|MMMM|MMM|MM|M|dd|d|HH|H|mm|m|ss|s`)

type Block struct {
	ID   string    `json:"id"`
	Type BlockType `json:"type"`
	// Anchor is the readable element id for links (#anchor, /slug#anchor).
	Anchor   string `json:"anchor,omitempty"`
	Key      string `json:"key,omitempty"`
	Input    string `json:"input,omitempty"`
	Label    string `json:"label,omitempty"`
	Help     string `json:"help,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Editable lets a team captain change a team-field answer after creation.
	Editable bool `json:"editable,omitempty"`
	// StaffOnly hides a participant/team field from participants: organizers
	// fill it in the manage tables, and it never counts as required.
	StaffOnly         bool                              `json:"staffOnly,omitempty"`
	Options           []string                          `json:"options,omitempty"`
	FileTypes         []string                          `json:"fileTypes,omitempty"` // formats a file question accepts
	MaxSizeMB         int                               `json:"maxSizeMB,omitempty"` // size limit of a file question
	DateMode          string                            `json:"dateMode,omitempty"`  // date question: "date", "time" or "datetime"
	MinDate           string                            `json:"minDate,omitempty"`   // date question: earliest allowed answer
	MaxDate           string                            `json:"maxDate,omitempty"`   // date question: latest allowed answer
	RichText          json.RawMessage                   `json:"richText,omitempty"`
	Title             string                            `json:"title,omitempty"`
	Sub               string                            `json:"sub,omitempty"`
	Text              string                            `json:"text,omitempty"`
	By                string                            `json:"by,omitempty"`
	Kicker            string                            `json:"kicker,omitempty"`
	Note              string                            `json:"note,omitempty"`
	TocTitle          string                            `json:"tocTitle,omitempty"`
	Items             []BlockItem                       `json:"items,omitempty"`
	TargetVariable    string                            `json:"targetVariable,omitempty"`
	TargetDate        string                            `json:"targetDate,omitempty"`
	DateSource        string                            `json:"dateSource,omitempty"`
	ShowFromSource    string                            `json:"showFromSource,omitempty"`
	ShowFromVariable  string                            `json:"showFromVariable,omitempty"`
	ShowFromDate      string                            `json:"showFromDate,omitempty"`
	HideAfterFinish   bool                              `json:"hideAfterFinish,omitempty"`
	Layout            string                            `json:"layout,omitempty"`
	VerticalAlignment string                            `json:"verticalAlignment,omitempty"`
	ActionAlignment   string                            `json:"actionAlignment,omitempty"`
	TimerSize         string                            `json:"timerSize,omitempty"`
	TimerDisplay      string                            `json:"timerDisplay,omitempty"`
	Surface           string                            `json:"surface,omitempty"`
	ImageSource       string                            `json:"imageSource,omitempty"`
	ImageURL          string                            `json:"imageURL,omitempty"`
	Action            *BlockAction                      `json:"action,omitempty"`
	SecondaryAction   *BlockAction                      `json:"secondaryAction,omitempty"`
	Variant           string                            `json:"variant,omitempty"`
	WidthPercent      int                               `json:"widthPercent,omitempty"`
	Size              string                            `json:"size,omitempty"`
	Line              bool                              `json:"line,omitempty"`
	OpenItem          *int                              `json:"openItem,omitempty"`
	Groups            []PartnerGroup                    `json:"groups,omitempty"`
	Variables         []VariableBinding                 `json:"variables,omitempty"`
	DateDisplays      map[string]map[string]DateDisplay `json:"dateDisplays,omitempty"`
	Visibility        []VisibilityCondition             `json:"visibility,omitempty"`
	Condition         *Condition                        `json:"condition,omitempty"`
}

type Document struct {
	Blocks []Block `json:"blocks"`
}

func (d Document) Validate() error {
	fields := make(map[string]struct{})
	ids := make(map[string]struct{})
	for _, block := range d.Blocks {
		ids[strings.TrimSpace(block.ID)] = struct{}{}
	}
	if len(ids) != len(d.Blocks) {
		if _, empty := ids[""]; empty {
			return errors.New("block id is required")
		}
		return errors.New("block ids must be unique")
	}
	if err := validateAnchors(d.Blocks, ids); err != nil {
		return err
	}
	for _, block := range d.Blocks {
		if err := validateVariables(block); err != nil {
			return err
		}
		if err := validateVisibility(block); err != nil {
			return err
		}
		if block.ActionAlignment != "" && block.ActionAlignment != "start" && block.ActionAlignment != "center" && block.ActionAlignment != "end" {
			return errors.New("action alignment is unsupported")
		}
		if block.DateSource != "" && block.DateSource != "none" && block.DateSource != "event" && block.DateSource != "custom" {
			return errors.New("countdown date source is unsupported")
		}
		if block.TimerDisplay != "" {
			if block.Type != BlockHero && block.Type != BlockCountdown {
				return errors.New("timer display is unsupported for this block")
			}
			if block.TimerDisplay != "segments" && block.TimerDisplay != "compact" && block.TimerDisplay != "tiles" && block.TimerDisplay != "focus" && block.TimerDisplay != "dial" && block.TimerDisplay != "ledger" && block.TimerDisplay != "poster" && block.TimerDisplay != "tracks" && block.TimerDisplay != "flip" && block.TimerDisplay != "ticker" && block.TimerDisplay != "stairs" && block.TimerDisplay != "orbits" && block.TimerDisplay != "matrix" && block.TimerDisplay != "ribbon" && block.TimerDisplay != "rings" {
				return errors.New("timer display is unsupported")
			}
		}
		if block.DateSource == "custom" && block.TargetDate == "" {
			return errors.New("custom countdown date is required")
		}
		if block.DateSource == "event" && block.TargetVariable == "" && block.Type == BlockCountdown {
			return errors.New("event countdown date is required")
		}
		if block.DateSource == "none" && (block.TargetVariable != "" || block.TargetDate != "" || block.Type == BlockCountdown) {
			return errors.New("countdown date source is inconsistent")
		}
		if block.DateSource == "event" && block.TargetDate != "" || block.DateSource == "custom" && block.TargetVariable != "" {
			return errors.New("countdown date source is inconsistent")
		}
		if block.Condition != nil {
			if _, ok := fields[block.Condition.FieldKey]; !ok {
				return errors.New("condition must reference a preceding field")
			}
		}
		switch block.Type {
		case BlockBanner:
			if block.Variant != "" && block.Variant != "edge" && block.Variant != "frame" {
				return errors.New("banner layout is unsupported")
			}
			if block.Layout != "" && block.Layout != "left" && block.Layout != "center" && block.Layout != "right" {
				return errors.New("banner caption alignment is unsupported")
			}
			if block.WidthPercent != 0 && (block.WidthPercent < 50 || block.WidthPercent > 100 || block.WidthPercent%5 != 0) {
				return errors.New("banner width must be between 50 and 100 percent in steps of five")
			}
			if block.ImageSource != "" && block.ImageSource != "preview" && block.ImageSource != "custom" {
				return errors.New("banner image source is unsupported")
			}
			if block.ImageSource == "custom" && strings.TrimSpace(block.ImageURL) == "" {
				return errors.New("custom banner image is required")
			}
		case BlockHero:
			if strings.TrimSpace(block.Title) == "" {
				return errors.New("hero title is required")
			}
			if block.Variant != "" && block.Variant != "plain" && block.Variant != "mass" {
				return errors.New("hero variant is unsupported")
			}
			if block.Layout != "" && block.Layout != "split" && block.Layout != "center" {
				return errors.New("hero layout is unsupported")
			}
			if block.TimerSize != "" && block.TimerSize != "large" && block.TimerSize != "xl" {
				return errors.New("hero timer size is unsupported")
			}
			if len(block.Items) > 4 {
				return errors.New("hero allows up to four facts")
			}
			for _, item := range block.Items {
				if strings.TrimSpace(item.Label) == "" || strings.TrimSpace(item.Value) == "" {
					return errors.New("hero fact requires a label and value")
				}
			}
			if block.Action != nil && (strings.TrimSpace(block.Action.Label) == "" || !validContentHref(block.Action.Href)) {
				return errors.New("hero action is invalid")
			}
			if block.SecondaryAction != nil && (strings.TrimSpace(block.SecondaryAction.Label) == "" || !validContentHref(block.SecondaryAction.Href)) {
				return errors.New("hero secondary action is invalid")
			}
			if block.TargetVariable != "" && block.TargetDate != "" {
				return errors.New("hero countdown has two dates")
			}
			if block.TargetDate != "" {
				if _, err := time.Parse(time.RFC3339, block.TargetDate); err != nil {
					return errors.New("hero countdown date is invalid")
				}
			}
			if block.TargetVariable != "" && EventContentVariables[block.TargetVariable] != VariableFormatDateTime {
				return errors.New("hero countdown requires a date variable")
			}
			if block.TargetVariable != "" && !hasDateBinding(block, block.TargetVariable) {
				return errors.New("hero countdown date variable must be declared")
			}
		case BlockSection:
			if strings.TrimSpace(block.Label) == "" {
				return errors.New("section title is required")
			}
			if block.Variant != "" && block.Variant != "left" && block.Variant != "center" && block.Variant != "right" && block.Variant != "justify" {
				return errors.New("section alignment is unsupported")
			}
		case BlockText:
			if block.Variant != "" && block.Variant != "narrow" && block.Variant != "wide" {
				return errors.New("text width is unsupported")
			}
		case BlockDoc:
			if len(block.Items) == 0 {
				return errors.New("document requires at least one section")
			}
			for _, section := range block.Items {
				if strings.TrimSpace(section.Label) == "" {
					return errors.New("document section requires a title and text")
				}
			}
		case BlockFacts, BlockFAQ:
			if block.Type == BlockFacts && block.Variant != "" && block.Variant != "strip" && block.Variant != "rows" {
				return errors.New("facts layout is unsupported")
			}
			if len(block.Items) == 0 {
				return errors.New("list block requires at least one item")
			}
			for _, item := range block.Items {
				if strings.TrimSpace(item.Label) == "" || block.Type == BlockFacts && strings.TrimSpace(item.Value) == "" {
					return errors.New("list item requires a label and value")
				}
			}
			if block.Type == BlockFAQ && block.OpenItem != nil && (*block.OpenItem < 0 || *block.OpenItem >= len(block.Items)) {
				return errors.New("faq open item is out of range")
			}
		case BlockTimeline:
			if block.Variant != "" && block.Variant != "grid" && block.Variant != "list" {
				return errors.New("timeline layout is unsupported")
			}
			if len(block.Items) == 0 {
				return errors.New("timeline requires at least one item")
			}
			for _, item := range block.Items {
				if strings.TrimSpace(item.Value) == "" {
					return errors.New("timeline item requires an event name")
				}
				for _, match := range variableToken.FindAllStringSubmatch(item.Value, -1) {
					if EventContentVariables[match[1]] == VariableFormatDateTime {
						return errors.New("timeline event name cannot contain a date variable")
					}
				}
				switch item.DateSource {
				case "event":
					if item.DateValue != "" || !hasDateBinding(block, item.DateVariable) {
						return errors.New("timeline item requires a declared event date")
					}
				case "custom":
					if item.DateVariable != "" {
						return errors.New("timeline item has two date sources")
					}
					if _, err := time.Parse(time.RFC3339, item.DateValue); err != nil {
						return errors.New("timeline item requires a valid custom date")
					}
				default:
					return errors.New("timeline item date source is unsupported")
				}
				if item.DateFormat != "" && item.DateFormat != "date-time" && item.DateFormat != "date" && item.DateFormat != "time" && item.DateFormat != "short" && item.DateFormat != "custom" {
					return errors.New("timeline item date format is unsupported")
				}
				if item.DateFormat == "custom" && (len(item.DatePattern) == 0 || len(item.DatePattern) > 80 || strings.ContainsAny(item.DatePattern, "\r\n<>") || !datePatternToken.MatchString(item.DatePattern)) {
					return errors.New("timeline item custom date format is invalid")
				}
			}
		case BlockCTA:
			if !validBlockAction(block.Action) {
				return errors.New("action block requires a valid action")
			}
			if block.Variant != "" && block.Variant != "plain" && block.Variant != "mass" {
				return errors.New("action block variant is unsupported")
			}
			if block.SecondaryAction != nil && !validBlockAction(block.SecondaryAction) {
				return errors.New("secondary action is invalid")
			}
		case BlockCountdown:
			if block.ShowFromSource != "" && block.ShowFromSource != "none" && block.ShowFromSource != "event" && block.ShowFromSource != "custom" {
				return errors.New("countdown show-from source is unsupported")
			}
			switch block.ShowFromSource {
			case "", "none":
				if block.ShowFromVariable != "" || block.ShowFromDate != "" {
					return errors.New("countdown show-from source is inconsistent")
				}
			case "event":
				if block.ShowFromDate != "" || EventContentVariables[block.ShowFromVariable] != VariableFormatDateTime || !hasDateBinding(block, block.ShowFromVariable) {
					return errors.New("countdown show-from requires a declared date variable")
				}
			case "custom":
				if block.ShowFromVariable != "" {
					return errors.New("countdown show-from source is inconsistent")
				}
				if _, err := time.Parse(time.RFC3339, block.ShowFromDate); err != nil {
					return errors.New("countdown show-from date is invalid")
				}
			}
			if block.VerticalAlignment != "" && block.VerticalAlignment != "start" && block.VerticalAlignment != "center" && block.VerticalAlignment != "end" {
				return errors.New("countdown text position is unsupported")
			}
			if block.Variant != "" && block.Variant != "split" && block.Variant != "center" {
				return errors.New("countdown layout is unsupported")
			}
			if block.TimerSize != "" && block.TimerSize != "large" && block.TimerSize != "xl" {
				return errors.New("countdown timer size is unsupported")
			}
			if block.Surface != "" && block.Surface != "plain" && block.Surface != "frame" {
				return errors.New("countdown surface is unsupported")
			}
			if block.Action != nil && (strings.TrimSpace(block.Action.Label) == "" || !validContentHref(block.Action.Href)) {
				return errors.New("countdown action is invalid")
			}
			if block.TargetVariable != "" && block.TargetDate != "" {
				return errors.New("countdown has two dates")
			}
			if block.TargetDate != "" {
				if _, err := time.Parse(time.RFC3339, block.TargetDate); err != nil {
					return errors.New("countdown date is invalid")
				}
				if block.ShowFromDate != "" {
					showFrom, _ := time.Parse(time.RFC3339, block.ShowFromDate)
					target, _ := time.Parse(time.RFC3339, block.TargetDate)
					if !showFrom.Before(target) {
						return errors.New("countdown show-from must precede target")
					}
				}
			} else if EventContentVariables[block.TargetVariable] != VariableFormatDateTime || !hasDateBinding(block, block.TargetVariable) {
				return errors.New("countdown requires a declared date variable or a custom date")
			}
		case BlockPartners:
			if err := validatePartners(block); err != nil {
				return err
			}
		case BlockDivider:
			if block.Size != "" && block.Size != "sm" && block.Size != "md" && block.Size != "lg" {
				return errors.New("divider size is unsupported")
			}
		case BlockField:
			if strings.TrimSpace(block.Key) == "" || strings.TrimSpace(block.Input) == "" || strings.TrimSpace(block.Label) == "" {
				return errors.New("field key, input, and label are required")
			}
			if _, exists := fields[block.Key]; exists {
				return errors.New("field keys must be unique")
			}
			switch block.Input {
			case "text", "long_text", "number", "select", "multi_select", "checkbox", "file", "date":
			default:
				return errors.New("field input is unsupported")
			}
			if (block.Input == "select" || block.Input == "multi_select") && len(block.Options) == 0 {
				return errors.New("choice field options are required")
			}
			fields[block.Key] = struct{}{}
		default:
			return errors.New("block type is unsupported")
		}
	}
	return nil
}

// validateAnchors keeps anchors readable and unique on the page. An anchor
// may not repeat another block's id, which is the element id fallback.
func validateAnchors(blocks []Block, ids map[string]struct{}) error {
	anchors := make(map[string]struct{})
	for _, block := range blocks {
		if block.Anchor == "" {
			continue
		}
		if len(block.Anchor) > maxAnchorLength || !anchorPattern.MatchString(block.Anchor) {
			return errors.New("block anchor must use lowercase latin letters, digits and hyphens")
		}
		if _, exists := anchors[block.Anchor]; exists {
			return errors.New("block anchors must be unique on the page")
		}
		if _, clash := ids[block.Anchor]; clash && block.Anchor != block.ID {
			return errors.New("block anchor repeats another block id")
		}
		anchors[block.Anchor] = struct{}{}
	}
	return nil
}

func validatePartners(block Block) error {
	if len(block.Groups) == 0 || len(block.Groups) > maxPartnerGroups {
		return errors.New("partners block requires one to ten groups")
	}
	for _, group := range block.Groups {
		if len(group.Items) == 0 || len(group.Items) > maxLogosPerGroup {
			return errors.New("partner group requires one to twenty-four logos")
		}
		for _, logo := range group.Items {
			name := strings.TrimSpace(logo.Name)
			if name == "" || len([]rune(name)) > maxPartnerNameLength {
				return errors.New("partner logo requires a name")
			}
			if strings.TrimSpace(logo.ImageURL) == "" {
				return errors.New("partner logo requires an image")
			}
			if logo.Href != "" && !validContentHref(logo.Href) {
				return errors.New("partner link is invalid")
			}
		}
	}
	return nil
}

// ContentImageURLs lists every uploaded image a document references: custom
// banner images and partner logos. The caller checks their ownership.
func (d Document) ContentImageURLs() []string {
	var urls []string
	for _, block := range d.Blocks {
		switch block.Type {
		case BlockBanner:
			if block.ImageSource == "custom" {
				urls = append(urls, block.ImageURL)
			}
		case BlockPartners:
			for _, group := range block.Groups {
				for _, logo := range group.Items {
					urls = append(urls, logo.ImageURL)
				}
			}
		}
	}
	return urls
}

func validateVariables(block Block) error {
	declared := make(map[string]struct{}, len(block.Variables))
	for _, binding := range block.Variables {
		name := strings.TrimSpace(binding.Name)
		format, known := EventContentVariables[name]
		if !known {
			return errors.New("content variable is unsupported")
		}
		if binding.Format != format {
			return errors.New("content variable format is invalid")
		}
		if _, exists := declared[name]; exists {
			return errors.New("content variable bindings must be unique")
		}
		declared[name] = struct{}{}
	}
	richFields := map[string]map[string]struct{}{}
	if block.Type == BlockText {
		names, err := validateRichText(block.RichText, declared)
		if err != nil {
			return err
		}
		richFields["richText"] = names
	}
	if block.Type == BlockDoc || block.Type == BlockFAQ {
		for index, item := range block.Items {
			names, err := validateRichText(item.RichText, declared)
			if err != nil {
				return err
			}
			richFields["item:"+strconv.Itoa(index)+":richText"] = names
		}
	}
	texts := []string{block.Label, block.Help, block.Title, block.Sub, block.Text, block.By, block.Kicker, block.Note, block.TocTitle}
	for _, group := range block.Groups {
		texts = append(texts, group.Title)
		for _, logo := range group.Items {
			texts = append(texts, logo.Name, logo.Href)
		}
	}
	if block.Action != nil {
		texts = append(texts, block.Action.Label, block.Action.Href)
	}
	if block.SecondaryAction != nil {
		texts = append(texts, block.SecondaryAction.Label, block.SecondaryAction.Href)
	}
	for _, item := range block.Items {
		texts = append(texts, item.Label)
		if block.Type != BlockDoc && block.Type != BlockFAQ {
			texts = append(texts, item.Value)
		}
	}
	for _, text := range texts {
		for _, match := range variableToken.FindAllStringSubmatch(text, -1) {
			if _, exists := declared[match[1]]; !exists {
				return errors.New("content variable must be declared by its block")
			}
		}
	}
	fieldTexts := map[string]string{
		"label": block.Label, "title": block.Title,
		"sub": block.Sub, "text": block.Text, "by": block.By,
		"kicker": block.Kicker, "note": block.Note, "tocTitle": block.TocTitle,
	}
	if block.Action != nil {
		fieldTexts["action:label"] = block.Action.Label
	}
	if block.SecondaryAction != nil {
		fieldTexts["secondaryAction:label"] = block.SecondaryAction.Label
	}
	for index, item := range block.Items {
		prefix := "item:" + strconv.Itoa(index) + ":"
		fieldTexts[prefix+"label"] = item.Label
		if block.Type != BlockDoc && block.Type != BlockFAQ {
			fieldTexts[prefix+"value"] = item.Value
		}
	}
	for field, formats := range block.DateDisplays {
		text, textExists := fieldTexts[field]
		richNames, richExists := richFields[field]
		if !textExists && !richExists {
			return errors.New("date display field is unsupported")
		}
		for name, display := range formats {
			if EventContentVariables[name] != VariableFormatDateTime {
				return errors.New("date display requires a date variable")
			}
			_, declaredVariable := declared[name]
			_, richVariable := richNames[name]
			if !declaredVariable || !(richExists && richVariable || textExists && strings.Contains(text, "{{"+name+"}}")) {
				return errors.New("date display variable is not used in its field")
			}
			if display.Format != "date-time" && display.Format != "date" && display.Format != "time" && display.Format != "short" && display.Format != "custom" {
				return errors.New("date display format is unsupported")
			}
			if display.Format == "custom" && (len(display.Pattern) == 0 || len(display.Pattern) > 80 || strings.ContainsAny(display.Pattern, "\r\n<>") || !datePatternToken.MatchString(display.Pattern)) {
				return errors.New("date display pattern is invalid")
			}
		}
	}
	return nil
}

func hasDateBinding(block Block, name string) bool {
	for _, binding := range block.Variables {
		if binding.Name == name && binding.Format == VariableFormatDateTime {
			return true
		}
	}
	return false
}

func validateVisibility(block Block) error {
	if len(block.Visibility) == 0 {
		return nil
	}
	if block.Type == BlockField {
		return errors.New("context visibility is supported only by content blocks")
	}
	declared := make(map[string]VariableFormat, len(block.Variables))
	for _, binding := range block.Variables {
		declared[strings.TrimSpace(binding.Name)] = binding.Format
	}
	for _, condition := range block.Visibility {
		format, exists := declared[strings.TrimSpace(condition.Variable)]
		if !exists {
			return errors.New("visibility variable must be declared by its block")
		}
		if !validVisibilityOperator(format, condition.Operator) {
			return errors.New("visibility operator is unsupported for variable type")
		}
		if !validVisibilityValue(format, condition.Value) {
			return errors.New("visibility value does not match variable type")
		}
	}
	return nil
}

func validVisibilityOperator(format VariableFormat, operator string) bool {
	switch format {
	case VariableFormatText, VariableFormatBoolean:
		return operator == "equals" || operator == "not_equals"
	case VariableFormatNumber:
		return operator == "equals" || operator == "not_equals" || operator == "greater_than" || operator == "greater_or_equal" || operator == "less_than" || operator == "less_or_equal"
	case VariableFormatDateTime:
		return operator == "equals" || operator == "not_equals" || operator == "before" || operator == "after"
	default:
		return false
	}
}

func validVisibilityValue(format VariableFormat, value any) bool {
	switch format {
	case VariableFormatText:
		_, ok := value.(string)
		return ok
	case VariableFormatBoolean:
		_, ok := value.(bool)
		return ok
	case VariableFormatNumber:
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		default:
			return false
		}
	case VariableFormatDateTime:
		raw, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339, raw)
		return err == nil
	default:
		return false
	}
}
