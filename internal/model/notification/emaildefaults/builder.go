// Package emaildefaults is the code-owned source of the default email
// templates: subject, preheader and blocks of every notification type, in
// Ukrainian (the seeded rows) and English (for the day mail has a language).
// The seed migration is generated from it and tests render it, so wording and
// design live in one place.
package emaildefaults

import "encoding/json"

// Lang is the language of a template set.
type Lang string

const (
	UK Lang = "uk"
	EN Lang = "en"
)

// Template is one email template: what a notification_email_templates row
// holds besides its scope and status.
type Template struct {
	Type      string
	Subject   string
	Preheader string
	Body      json.RawMessage
	// Audience is participant for mail an event brands, platform otherwise.
	Participant bool
}

type node = map[string]any

func text(s string) node { return node{"type": "text", "text": s, "format": 0} }

func variable(name string) node { return node{"type": "variable", "varName": name} }

func link(url string, children ...node) node {
	return node{"type": "link", "url": url, "children": children}
}

// parts turns a mix of plain strings and nodes into paragraph children.
func parts(items []any) []node {
	out := make([]node, 0, len(items))
	for _, it := range items {
		switch v := it.(type) {
		case string:
			out = append(out, text(v))
		case node:
			out = append(out, v)
		}
	}
	return out
}

func para(items ...any) node { return node{"type": "paragraph", "children": parts(items)} }

func heading(s string) node {
	return node{"type": "heading", "tag": "h1", "children": []node{text(s)}}
}

func rich(nodes ...node) node {
	return node{"type": "rich_text", "content": node{"root": node{"type": "root", "children": nodes}}}
}

func logo() node { return node{"type": "logo", "align": "left", "width_px": 44} }

func button(label, url string) node {
	return node{"type": "button", "label": label, "url": url, "align": "left"}
}

// fact is one row of a facts card.
type fact struct{ Label, Value string }

func facts(rows ...fact) node {
	items := make([]node, 0, len(rows))
	for _, r := range rows {
		items = append(items, node{"label": r.Label, "value": r.Value})
	}
	return node{"type": "facts", "items": items}
}

func blocks(bs ...node) json.RawMessage {
	raw, err := json.Marshal(bs)
	if err != nil {
		panic(err)
	}
	return raw
}
