package eventContentModel

import (
	"encoding/json"
	"fmt"
	"github.com/cybericebox/daemon/internal/model/eventContent/testutil"
	"strings"
	"testing"
)

func TestDocumentValidatesLexicalRichText(t *testing.T) {
	text := `{"root":{"type":"root","version":1,"children":[{"type":"paragraph","version":1,"children":[{"type":"text","version":1,"text":"Hello","format":0}]}]}}`
	variable := `{"root":{"type":"root","version":1,"children":[{"type":"paragraph","version":1,"children":[{"type":"variable","version":1,"varName":"event.name"}]}]}}`
	link := `{"root":{"type":"root","version":1,"children":[{"type":"paragraph","version":1,"children":[{"type":"link","version":1,"url":"javascript:alert(1)","children":[{"type":"text","version":1,"text":"bad"}]}]}]}}`
	deep := `{"type":"text","version":1,"text":"x"}`
	for range 35 {
		deep = `{"type":"paragraph","version":1,"children":[` + deep + `]}`
	}
	cases := []struct {
		name     string
		rich     string
		bindings string
		displays string
		valid    bool
	}{
		{name: "text", rich: text, valid: true},
		{name: "variable only", rich: variable, bindings: `,"variables":[{"name":"event.name","format":"text"}]`, valid: true},
		{name: "heading six", rich: `{"root":{"type":"root","version":1,"children":[{"type":"heading","version":1,"tag":"h6","children":[{"type":"text","version":1,"text":"Small heading"}]}]}}`, valid: true},
		{name: "heading seven", rich: `{"root":{"type":"root","version":1,"children":[{"type":"heading","version":1,"tag":"h7","children":[{"type":"text","version":1,"text":"Unsupported"}]}]}}`},
		{name: "empty", rich: `{"root":{"type":"root","version":1,"children":[]}}`},
		{name: "malformed", rich: `{"root":`},
		{name: "too large", rich: `{"root":{"type":"root","version":1,"children":[{"type":"paragraph","version":1,"children":[{"type":"text","version":1,"text":"` + strings.Repeat("a", 270000) + `"}]}]}}`},
		{name: "too deep", rich: `{"root":{"type":"root","version":1,"children":[` + deep + `]}}`},
		{name: "unknown node", rich: `{"root":{"type":"root","version":1,"children":[{"type":"iframe","version":1,"url":"https://example.com"}]}}`},
		{name: "unsafe link", rich: link},
		{name: "undeclared variable", rich: variable},
		{name: "stale date display", rich: text, displays: `,"dateDisplays":{"richText":{"event.startAt":{"format":"date"}}}`, bindings: `,"variables":[{"name":"event.startAt","format":"date-time"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var document Document
			payload := fmt.Sprintf(`{"blocks":[{"id":"body","type":"text","richText":%s%s%s}]}`, tc.rich, tc.bindings, tc.displays)
			if err := json.Unmarshal([]byte(payload), &document); err != nil {
				if tc.name == "malformed" {
					return
				}
				t.Fatalf("test document is malformed: %v", err)
			}
			err := document.Validate()
			if tc.valid && err != nil {
				t.Fatalf("valid Lexical document rejected: %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("invalid Lexical document accepted")
			}
		})
	}
}

func TestDocumentRejectsRawHTMLAndForwardCondition(t *testing.T) {
	document := Document{Blocks: []Block{
		{ID: "first", Type: BlockField, Key: "age", Input: "number", Label: "Age"},
		{ID: "bad", Type: BlockText, RichText: json.RawMessage(`{"root":{"type":"root","version":1,"children":[{"type":"html","version":1,"value":"<script>alert(1)</script>"}]}}`)},
	}}
	if err := document.Validate(); err == nil {
		t.Fatal("want raw HTML rejected")
	}

	document = Document{Blocks: []Block{
		{ID: "conditional", Type: BlockField, Key: "later", Input: "text", Label: "Later", Condition: &Condition{FieldKey: "after", Operator: "equals", Value: true}},
		{ID: "after", Type: BlockField, Key: "after", Input: "checkbox", Label: "After"},
	}}
	if err := document.Validate(); err == nil {
		t.Fatal("want forward condition rejected")
	}
}

func TestDocumentRequiresOptionsForChoiceField(t *testing.T) {
	document := Document{Blocks: []Block{{ID: "university", Type: BlockField, Key: "university", Input: "select", Label: "University"}}}
	if err := document.Validate(); err == nil {
		t.Fatal("want select without options rejected")
	}
}

func TestContentBlocksValidateTypedVariablesAndActions(t *testing.T) {
	document := Document{Blocks: []Block{
		{ID: "facts", Type: BlockFacts, Items: []BlockItem{{Label: "Команди", Value: "{{event.approvedTeamCount}}"}}, Variables: []VariableBinding{{Name: "event.approvedTeamCount", Format: VariableFormatNumber}}},
		{ID: "timer", Type: BlockCountdown, Title: "До початку", TargetVariable: "event.startAt", Variables: []VariableBinding{{Name: "event.startAt", Format: VariableFormatDateTime}}},
		{ID: "action", Type: BlockCTA, Title: "Готові?", Action: &BlockAction{Label: "Приєднатися", Href: "/register"}},
	}}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid content blocks rejected: %v", err)
	}

	bad := []Block{
		{ID: "bad", Type: BlockFacts, Items: []BlockItem{{Label: "Команди", Value: "{{event.teamCount}}"}}},
		{ID: "bad", Type: BlockCountdown, TargetVariable: "event.publishAt"},
		{ID: "bad", Type: BlockCTA, Title: "X", Action: &BlockAction{Label: "X", Href: "javascript:alert(1)"}},
		{ID: "bad", Type: BlockFAQ, Items: []BlockItem{{Label: "Питання"}}},
	}
	for _, block := range bad {
		if err := (Document{Blocks: []Block{block}}).Validate(); err == nil {
			t.Fatalf("invalid block %#v accepted", block)
		}
	}
}

func TestJoinActionAndCountdownWindowValidate(t *testing.T) {
	start := "2026-10-01T12:00:00Z"
	end := "2026-10-02T12:00:00Z"
	good := Document{Blocks: []Block{
		{ID: "timer", Type: BlockCountdown, DateSource: "custom", TargetDate: end, ShowFromSource: "custom", ShowFromDate: start, HideAfterFinish: true},
		{ID: "join", Type: BlockCTA, Action: &BlockAction{Label: "Приєднатися", Kind: "join_event"}},
	}}
	if err := good.ValidateContent(); err != nil {
		t.Fatalf("valid join action and countdown rejected: %v", err)
	}
	bad := []Block{
		{ID: "join", Type: BlockCTA, Action: &BlockAction{Label: "Join", Kind: "join_event", Href: "/join"}},
		{ID: "link", Type: BlockCTA, Action: &BlockAction{Label: "Link", Kind: "link"}},
		{ID: "timer", Type: BlockCountdown, DateSource: "custom", TargetDate: end, ShowFromSource: "custom", ShowFromDate: "invalid"},
		{ID: "timer", Type: BlockCountdown, DateSource: "custom", TargetDate: start, ShowFromSource: "custom", ShowFromDate: end},
	}
	for _, block := range bad {
		if err := (Document{Blocks: []Block{block}}).ValidateContent(); err == nil {
			t.Fatalf("invalid block %#v accepted", block)
		}
	}
}

func TestDocumentRejectsUnsupportedBlocksDuplicateKeysAndAllRawHTML(t *testing.T) {
	for _, document := range []Document{
		{Blocks: []Block{{ID: "x", Type: "embed"}}},
		{Blocks: []Block{{ID: "a", Type: BlockField, Key: "name", Input: "text", Label: "Name"}, {ID: "b", Type: BlockField, Key: "name", Input: "text", Label: "Again"}}},
		{Blocks: []Block{{ID: "x", Type: BlockText, RichText: json.RawMessage(`{"root":{"type":"root","version":1,"children":[{"type":"html","version":1,"value":"<b>unsafe</b>"}]}}`)}}},
	} {
		if err := document.Validate(); err == nil {
			t.Fatalf("document %#v must be rejected", document)
		}
	}
}

func TestDocumentAllowsOnlyDeclaredTypedVariables(t *testing.T) {
	document := Document{Blocks: []Block{{
		ID:       "schedule",
		Type:     BlockText,
		RichText: testutil.RichText("Starts {{event.startAt}}; {{event.teamCount}} teams registered."),
		Variables: []VariableBinding{
			{Name: "event.startAt", Format: VariableFormatDateTime},
			{Name: "event.teamCount", Format: VariableFormatNumber},
		},
	}}}
	if err := document.Validate(); err != nil {
		t.Fatalf("declared variables must validate: %v", err)
	}

	for _, invalid := range []Document{
		{Blocks: []Block{{ID: "undeclared", Type: BlockText, RichText: testutil.RichText("{{event.startAt}}")}}},
		{Blocks: []Block{{ID: "unknown", Type: BlockText, RichText: testutil.RichText("{{user.password}}"), Variables: []VariableBinding{{Name: "user.password", Format: VariableFormatText}}}}},
		{Blocks: []Block{{ID: "format", Type: BlockText, RichText: testutil.RichText("{{event.startAt}}"), Variables: []VariableBinding{{Name: "event.startAt", Format: VariableFormatNumber}}}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("document %#v must reject invalid variable bindings", invalid)
		}
	}
}

func TestDocumentRequiresDeclaredVariablesInActionLinks(t *testing.T) {
	block := Block{ID: "action", Type: BlockCTA, Title: "Правила", Action: &BlockAction{Label: "Відкрити", Href: "/p/{{event.tag}}"}}
	if err := (Document{Blocks: []Block{block}}).Validate(); err == nil {
		t.Fatal("action link with undeclared variable accepted")
	}
	block.Variables = []VariableBinding{{Name: "event.tag", Format: VariableFormatText}}
	if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
		t.Fatalf("action link with declared variable rejected: %v", err)
	}
}

func TestDocumentAllowsTypedContextVisibilityOnlyForContentBlocks(t *testing.T) {
	document := Document{Blocks: []Block{{
		ID:       "running-announcement",
		Type:     BlockText,
		RichText: testutil.RichText("The event is running."),
		Variables: []VariableBinding{
			{Name: "event.phase", Format: VariableFormatText},
			{Name: "event.teamCount", Format: VariableFormatNumber},
		},
		Visibility: []VisibilityCondition{
			{Variable: "event.phase", Operator: "equals", Value: "started"},
			{Variable: "event.teamCount", Operator: "greater_than", Value: 0},
		},
	}}}
	if err := document.Validate(); err != nil {
		t.Fatalf("declared typed visibility conditions must validate: %v", err)
	}

	for _, invalid := range []Document{
		{Blocks: []Block{{ID: "undeclared", Type: BlockText, RichText: testutil.RichText("Visible"), Visibility: []VisibilityCondition{{Variable: "event.phase", Operator: "equals", Value: "started"}}}}},
		{Blocks: []Block{{ID: "operator", Type: BlockText, RichText: testutil.RichText("Visible"), Variables: []VariableBinding{{Name: "event.phase", Format: VariableFormatText}}, Visibility: []VisibilityCondition{{Variable: "event.phase", Operator: "greater_than", Value: "started"}}}}},
		{Blocks: []Block{{ID: "type", Type: BlockText, RichText: testutil.RichText("Visible"), Variables: []VariableBinding{{Name: "event.teamCount", Format: VariableFormatNumber}}, Visibility: []VisibilityCondition{{Variable: "event.teamCount", Operator: "greater_than", Value: "many"}}}}},
		{Blocks: []Block{{ID: "field", Type: BlockField, Key: "name", Input: "text", Label: "Name", Visibility: []VisibilityCondition{{Variable: "event.phase", Operator: "equals", Value: "started"}}}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("document %#v must reject invalid context visibility", invalid)
		}
	}
}

func TestDocumentRequiresDeclarationForVariablesInEveryRenderableTextField(t *testing.T) {
	valid := Document{Blocks: []Block{{
		ID:        "heading",
		Type:      BlockSection,
		Label:     "Welcome to {{event.name}}",
		Help:      "Registration closes at {{event.startAt}}.",
		Variables: []VariableBinding{{Name: "event.name", Format: VariableFormatText}, {Name: "event.startAt", Format: VariableFormatDateTime}},
	}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("variables in section text must validate when declared: %v", err)
	}

	invalid := Document{Blocks: []Block{{
		ID:    "heading",
		Type:  BlockSection,
		Label: "Welcome to {{event.name}}",
	}}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("want undeclared variable in label rejected")
	}
}

func TestHeroBlockValidatesContentAndDateBinding(t *testing.T) {
	valid := Block{ID: "hero", Type: BlockHero, Variant: "mass", By: "CyberICEBox", Title: "{{event.name}}", Kicker: "Фінал", Note: "Починаємо скоро",
		Items: []BlockItem{{Label: "Команди", Value: "{{event.teamCount}}"}}, TargetVariable: "event.startAt",
		Action:          &BlockAction{Label: "Правила", Href: "/p/rules"},
		SecondaryAction: &BlockAction{Label: "Дізнатися більше", Href: "https://example.org"},
		Variables:       []VariableBinding{{Name: "event.name", Format: VariableFormatText}, {Name: "event.teamCount", Format: VariableFormatNumber}, {Name: "event.startAt", Format: VariableFormatDateTime}}}
	if err := (Document{Blocks: []Block{valid}}).Validate(); err != nil {
		t.Fatalf("valid hero: %v", err)
	}
	for _, mutate := range []func(*Block){
		func(b *Block) { b.Title = "" },
		func(b *Block) { b.Variant = "unknown" },
		func(b *Block) { b.TargetVariable = "event.teamCount" },
		func(b *Block) { b.Variables = b.Variables[:2] },
		func(b *Block) { b.SecondaryAction.Href = "javascript:alert(1)" },
		func(b *Block) { b.Items[0].Value = "" },
	} {
		copy := valid
		copy.Items = append([]BlockItem(nil), valid.Items...)
		copy.Variables = append([]VariableBinding(nil), valid.Variables...)
		secondary := *valid.SecondaryAction
		copy.SecondaryAction = &secondary
		mutate(&copy)
		if err := (Document{Blocks: []Block{copy}}).Validate(); err == nil {
			t.Fatalf("invalid hero accepted: %#v", copy)
		}
	}
}

func TestPageBlocksCanUseHeroAndBannerInAnyOrder(t *testing.T) {
	document := Document{Blocks: []Block{
		{ID: "intro", Type: BlockSection, Label: "Вступ", Variant: "center"},
		{ID: "image", Type: BlockBanner, Variant: "frame", WidthPercent: 65},
		{ID: "hero", Type: BlockHero, Title: "Подія", Variant: "mass"},
		{ID: "clock", Type: BlockCountdown, TargetVariable: "event.startAt", Variables: []VariableBinding{{Name: "event.startAt", Format: VariableFormatDateTime}}, Variant: "center", Action: &BlockAction{Label: "Приєднатися", Href: "/register"}},
	}}
	if err := document.Validate(); err != nil {
		t.Fatalf("reordered full-width blocks should validate: %v", err)
	}
	document.Blocks[1].Variant = "unsupported"
	if err := document.Validate(); err == nil {
		t.Fatal("unsupported banner layout should be rejected")
	}
	document.Blocks[1].Variant = "frame"
	for _, width := range []int{49, 66, 101} {
		document.Blocks[1].WidthPercent = width
		if err := document.Validate(); err == nil {
			t.Fatalf("invalid banner width %d should be rejected", width)
		}
	}
}

func TestPageBlockTextAndCaptionAlignment(t *testing.T) {
	blocks := []Block{
		{ID: "heading", Type: BlockSection, Label: "Правила", Variant: "justify"},
		{ID: "body", Type: BlockText, RichText: testutil.RichText("**Текст**")},
		{ID: "banner", Type: BlockBanner, Variant: "frame", Layout: "center"},
	}
	if err := (Document{Blocks: blocks}).ValidateContent(); err != nil {
		t.Fatalf("supported alignments rejected: %v", err)
	}
	for _, invalid := range []struct {
		index int
		value string
	}{{0, "diagonal"}, {1, "justify"}, {2, "justify"}} {
		copy := append([]Block(nil), blocks...)
		if invalid.index == 0 {
			copy[0].Variant = invalid.value
		} else {
			copy[invalid.index].Layout = invalid.value
		}
		if err := (Document{Blocks: copy}).ValidateContent(); err == nil {
			t.Fatalf("unsupported alignment accepted for %s", copy[invalid.index].Type)
		}
	}
}

func TestBlockAnchorsAreReadableAndUniqueOnThePage(t *testing.T) {
	blocks := []Block{
		{ID: "block-1", Type: BlockSection, Label: "Правила", Anchor: "rules"},
		{ID: "block-2", Type: BlockText, RichText: testutil.RichText("Текст"), Anchor: "scoring-2026"},
		{ID: "block-3", Type: BlockDivider},
	}
	if err := (Document{Blocks: blocks}).ValidateContent(); err != nil {
		t.Fatalf("valid anchors rejected: %v", err)
	}
	for name, anchor := range map[string]string{
		"duplicate":  "rules",
		"uppercase":  "Rules",
		"cyrillic":   "правила",
		"spaces":     "my rules",
		"edge dash":  "-rules",
		"block id":   "block-1",
		"too long":   strings.Repeat("a", 65),
		"underscore": "my_rules",
	} {
		copy := append([]Block(nil), blocks...)
		copy[2].Anchor = anchor
		if err := (Document{Blocks: copy}).ValidateContent(); err == nil {
			t.Errorf("%s anchor %q accepted", name, anchor)
		}
	}
}

func TestPartnersBlockRequiresNamedLogosInGroups(t *testing.T) {
	logo := PartnerLogo{Name: "ХНУРЕ", ImageURL: "/api/events/x/content-images/y", Href: "https://nure.ua"}
	valid := Block{ID: "partners", Type: BlockPartners, Title: "Партнери", Groups: []PartnerGroup{
		{Title: "Організатори", Items: []PartnerLogo{logo}},
		{Items: []PartnerLogo{{Name: "Без посилання", ImageURL: "/api/events/x/content-images/z"}}},
	}}
	if err := (Document{Blocks: []Block{valid}}).ValidateContent(); err != nil {
		t.Fatalf("valid partners block rejected: %v", err)
	}
	if urls := (Document{Blocks: []Block{valid}}).ContentImageURLs(); len(urls) != 2 {
		t.Fatalf("partner logos must be checked as content images, got %v", urls)
	}
	cases := map[string]func(*Block){
		"no groups":      func(b *Block) { b.Groups = nil },
		"empty group":    func(b *Block) { b.Groups[0].Items = nil },
		"unnamed logo":   func(b *Block) { b.Groups[0].Items[0].Name = " " },
		"no image":       func(b *Block) { b.Groups[0].Items[0].ImageURL = "" },
		"unsafe link":    func(b *Block) { b.Groups[0].Items[0].Href = "javascript:alert(1)" },
		"undeclared var": func(b *Block) { b.Groups[0].Title = "{{event.name}}" },
	}
	for name, mutate := range cases {
		block := valid
		block.Groups = []PartnerGroup{{Title: valid.Groups[0].Title, Items: append([]PartnerLogo(nil), valid.Groups[0].Items...)}}
		mutate(&block)
		if err := (Document{Blocks: []Block{block}}).ValidateContent(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCountdownAndHeroAcceptOneFixedDate(t *testing.T) {
	date := "2026-10-01T14:25:00Z"
	for _, block := range []Block{
		{ID: "clock", Type: BlockCountdown, TargetDate: date, DateSource: "custom", Variant: "center", TimerSize: "xl", Surface: "frame"},
		{ID: "hero", Type: BlockHero, Title: "Подія", TargetDate: date, Layout: "center", TimerSize: "xl"},
	} {
		if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
			t.Fatalf("fixed date rejected for %s: %v", block.Type, err)
		}
		block.TargetDate = "not-a-date"
		if err := (Document{Blocks: []Block{block}}).Validate(); err == nil {
			t.Fatalf("invalid fixed date accepted for %s", block.Type)
		}
		block.TargetDate = date
		block.TargetVariable = "event.startAt"
		if err := (Document{Blocks: []Block{block}}).Validate(); err == nil {
			t.Fatalf("two countdown sources accepted for %s", block.Type)
		}
	}
	for _, surface := range []string{"plain", "frame"} {
		block := Block{ID: "clock", Type: BlockCountdown, TargetDate: date, Surface: surface}
		if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
			t.Fatalf("countdown surface %s rejected: %v", surface, err)
		}
	}
	if err := (Document{Blocks: []Block{{ID: "clock", Type: BlockCountdown, TargetDate: date, Surface: "unknown"}}}).Validate(); err == nil {
		t.Fatal("unsupported countdown surface accepted")
	}
	for _, position := range []string{"start", "center", "end"} {
		block := Block{ID: "clock", Type: BlockCountdown, TargetDate: date, Title: "До початку", VerticalAlignment: position}
		if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
			t.Fatalf("countdown text position %s rejected: %v", position, err)
		}
	}
	if err := (Document{Blocks: []Block{{ID: "clock", Type: BlockCountdown, TargetDate: date, VerticalAlignment: "outside"}}}).Validate(); err == nil {
		t.Fatal("unsupported countdown text position accepted")
	}
}

func TestCountdownDisplayVariantsRoundTrip(t *testing.T) {
	date := "2026-10-01T14:25:00Z"
	for _, blockType := range []BlockType{BlockCountdown, BlockHero} {
		for _, display := range []string{"segments", "compact", "tiles", "focus", "dial", "ledger", "poster", "tracks", "flip", "ticker", "stairs", "orbits", "matrix", "ribbon", "rings"} {
			block := Block{ID: "timer", Type: blockType, Title: "До початку", TargetDate: date, TimerDisplay: display}
			if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
				t.Fatalf("%s display %s rejected: %v", blockType, display, err)
			}
			raw, err := json.Marshal(block)
			if err != nil {
				t.Fatal(err)
			}
			var restored Block
			if err := json.Unmarshal(raw, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.TimerDisplay != display {
				t.Fatalf("%s display changed after round trip: got %q, want %q", blockType, restored.TimerDisplay, display)
			}
		}
		invalid := Block{ID: "timer", Type: blockType, Title: "До початку", TargetDate: date, TimerDisplay: "unknown"}
		if err := (Document{Blocks: []Block{invalid}}).Validate(); err == nil {
			t.Fatalf("unknown display accepted for %s", blockType)
		}
	}
}

func TestTimelineDatesAndFormats(t *testing.T) {
	eventDate := Block{ID: "schedule", Type: BlockTimeline, Items: []BlockItem{{Value: "Початок завдань", DateSource: "event", DateVariable: "event.startAt", DateFormat: "time"}}, Variables: []VariableBinding{{Name: "event.startAt", Format: VariableFormatDateTime}}}
	customDate := Block{ID: "schedule", Type: BlockTimeline, Items: []BlockItem{{Value: "Нагородження", DateSource: "custom", DateValue: "2026-10-01T14:25:00Z", DateFormat: "custom", DatePattern: "dd.MM.yyyy [о] HH:mm"}}}
	for _, block := range []Block{eventDate, customDate} {
		if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
			t.Fatalf("timeline date rejected: %v", err)
		}
		raw, err := json.Marshal(block)
		if err != nil {
			t.Fatal(err)
		}
		var restored Block
		if err := json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.Items[0].DateFormat != block.Items[0].DateFormat || restored.Items[0].DateSource != block.Items[0].DateSource {
			t.Fatal("timeline date settings changed after JSON round trip")
		}
	}
	for _, invalid := range []Block{
		{ID: "schedule", Type: BlockTimeline, Items: []BlockItem{{Value: "Початок", DateSource: "event", DateVariable: "event.startAt"}}},
		{ID: "schedule", Type: BlockTimeline, Items: []BlockItem{{Value: "{{event.startAt}}", DateSource: "event", DateVariable: "event.startAt"}}, Variables: []VariableBinding{{Name: "event.startAt", Format: VariableFormatDateTime}}},
		{ID: "schedule", Type: BlockTimeline, Items: []BlockItem{{Value: "Початок", DateSource: "custom", DateValue: "invalid"}}},
		{ID: "schedule", Type: BlockTimeline, Items: []BlockItem{{Value: "Початок", DateSource: "custom", DateValue: "2026-10-01T14:25:00Z", DateFormat: "custom", DatePattern: "xyz"}}},
	} {
		if err := (Document{Blocks: []Block{invalid}}).Validate(); err == nil {
			t.Fatalf("invalid timeline accepted: %+v", invalid.Items[0])
		}
	}
}

func TestDateVariableDisplayPerField(t *testing.T) {
	block := Block{
		ID: "dates", Type: BlockCTA, Title: "Початок {{event.startAt}}", Action: &BlockAction{Label: "Відкрити", Href: "/"},
		Variables:    []VariableBinding{{Name: "event.startAt", Format: VariableFormatDateTime}},
		DateDisplays: map[string]map[string]DateDisplay{"title": {"event.startAt": {Format: "custom", Pattern: "dd.MM.yyyy [о] HH:mm"}}},
	}
	if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
		t.Fatalf("valid date display rejected: %v", err)
	}
	raw, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	var restored Block
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.DateDisplays["title"]["event.startAt"].Pattern != "dd.MM.yyyy [о] HH:mm" {
		t.Fatal("date display was not persisted")
	}
	block.DateDisplays["title"]["event.startAt"] = DateDisplay{Format: "custom", Pattern: ""}
	if err := (Document{Blocks: []Block{block}}).Validate(); err == nil {
		t.Fatal("empty date pattern accepted")
	}
}

func TestDocumentBlockRequiresSafeSections(t *testing.T) {
	valid := Block{ID: "rules", Type: BlockDoc, TocTitle: "Зміст", Items: []BlockItem{{Label: "Правила", RichText: testutil.RichText("Текст із виділенням.")}}}
	if err := (Document{Blocks: []Block{valid}}).Validate(); err != nil {
		t.Fatalf("valid document: %v", err)
	}
	for _, value := range []json.RawMessage{testutil.RichText(""), json.RawMessage(`{"root":{"type":"root","version":1,"children":[{"type":"html","version":1,"value":"<img src=x onerror=alert(1)>"}]}}`)} {
		invalid := valid
		invalid.Items = []BlockItem{{Label: "Правила", RichText: value}}
		if err := (Document{Blocks: []Block{invalid}}).Validate(); err == nil {
			t.Fatalf("invalid document section accepted: %q", value)
		}
	}
}

func TestFactsLayoutAndSecondaryAction(t *testing.T) {
	facts := Block{ID: "facts", Type: BlockFacts, Variant: "rows", Items: []BlockItem{{Label: "Формат", Value: "Командний"}}}
	cta := Block{ID: "cta", Type: BlockCTA, Title: "Правила", Action: &BlockAction{Label: "Читати", Href: "/p/rules"}, SecondaryAction: &BlockAction{Label: "Підтримка", Href: "https://example.org"}}
	if err := (Document{Blocks: []Block{facts, cta}}).Validate(); err != nil {
		t.Fatalf("valid content: %v", err)
	}
	facts.Variant = "unknown"
	if err := (Document{Blocks: []Block{facts}}).Validate(); err == nil {
		t.Fatal("unsupported facts layout accepted")
	}
	cta.SecondaryAction.Href = "javascript:alert(1)"
	if err := (Document{Blocks: []Block{cta}}).Validate(); err == nil {
		t.Fatal("unsafe secondary action accepted")
	}
}

func TestFAQInitialOpenItemIsInRange(t *testing.T) {
	open := 0
	block := Block{ID: "faq", Type: BlockFAQ, Items: []BlockItem{{Label: "Питання", RichText: testutil.RichText("Відповідь")}}, OpenItem: &open}
	if err := (Document{Blocks: []Block{block}}).Validate(); err != nil {
		t.Fatalf("first FAQ item should be valid: %v", err)
	}
	open = 1
	if err := (Document{Blocks: []Block{block}}).Validate(); err == nil {
		t.Fatal("out-of-range FAQ item accepted")
	}
}
