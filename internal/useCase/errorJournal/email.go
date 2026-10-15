package errorJournalUseCase

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/cybericebox/daemon/internal/model/notification/branding"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
	"github.com/gofrs/uuid"
)

// Render the persisted plain-text snapshot, including older queued deliveries.
// Only our own journal address becomes a button; diagnostic URLs remain data.
func (j *Journal) emailHTML(text string) (string, error) {
	lines := strings.Split(text, "\n")
	action := ""
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if link := j.errorLink(line); action == "" && link != "" {
			action = link
			continue
		}
		kept = append(kept, line)
	}
	summary, details, _ := strings.Cut(strings.Join(kept, "\n"), "\n\n")
	lines = strings.Split(strings.TrimSpace(summary), "\n")
	blocks := []map[string]any{}
	addText := func(kind, tag, value string) {
		blocks = append(blocks, map[string]any{"type": "rich_text", "content": map[string]any{
			"root": map[string]any{"type": "root", "children": []any{map[string]any{
				"type": kind, "tag": tag, "children": []any{map[string]any{"type": "text", "text": value}},
			}}},
		}})
	}
	if len(lines) > 1 && strings.HasPrefix(lines[0], "[") {
		addText("paragraph", "", lines[0])
		lines = lines[1:]
	}
	addText("heading", "h1", lines[0])
	facts := []map[string]string{}
	vars := map[string]any{}
	for _, line := range lines[1:] {
		prefix, value, ok := strings.Cut(line, ": ")
		labels := map[string]string{"Source": "Джерело", "Total": "Випадків", "Request": "Запит", "User": "Користувач"}
		if label := labels[prefix]; ok && label != "" {
			key := fmt.Sprintf("fact%d", len(facts))
			vars[key] = value // substitute once: diagnostics containing {{...}} remain literal.
			facts = append(facts, map[string]string{"label": label, "value": "{{." + key + "}}"})
		} else if strings.TrimSpace(line) != "" {
			addText("paragraph", "", line)
		}
	}
	if len(facts) > 0 {
		blocks = append(blocks, map[string]any{"type": "facts", "items": facts})
	}
	label := "Відкрити помилку"
	if action == "" && j.cfg.AdminURL != "" {
		action = strings.TrimRight(j.cfg.AdminURL, "/") + "/errors/"
		label = "Відкрити журнал"
	}
	if action != "" {
		blocks = append(blocks, map[string]any{"type": "button", "label": label, "url": action, "align": "left"})
	}
	if strings.TrimSpace(details) != "" {
		blocks = append(blocks, map[string]any{"type": "divider"})
		addText("heading", "h2", "Деталі помилки")
	}
	body, err := json.Marshal(blocks)
	if err != nil {
		return "", err
	}
	rendered, err := render.RenderEmail(body, nil, nil, vars, render.RenderContext{Brand: branding.Platform()})
	if err != nil {
		return "", err
	}
	content := rendered.HTML
	if strings.TrimSpace(details) != "" {
		content += `<pre class="cib-t" style="font-family:Consolas,Monaco,monospace;font-size:13px;line-height:1.6;color:#24263A;margin:0;white-space:pre-wrap;word-break:break-word;overflow-wrap:anywhere">` + html.EscapeString(details) + `</pre>`
	}
	return render.Shell("uk", subject(text), content), nil
}

func (j *Journal) errorLink(line string) string {
	if j.cfg.AdminURL == "" {
		return ""
	}
	base := strings.TrimRight(j.cfg.AdminURL, "/") + "/errors/"
	id, ok := strings.CutPrefix(line, base+"?group=")
	if !ok {
		id, ok = strings.CutPrefix(line, base)
		id = strings.TrimSuffix(id, "/")
	}
	if !ok {
		return ""
	}
	_, err := uuid.FromString(id)
	if err != nil {
		return ""
	}
	return base + "?group=" + id
}
