package postgres_test

import (
	"context"
	"regexp"
	"sort"
	"testing"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var (
	seedToken   = regexp.MustCompile(`\{\{\s*\.?\s*([A-Za-z_][\w]*)\s*\}\}`)
	seedVarNode = regexp.MustCompile(`"varName":\s*"([^"]+)"`)
)

// TestSeededTemplatesUseOnlyDeclaredVariables: every variable a seeded
// platform template uses (subject, preheader, rich-text variable nodes, plain
// {{tokens}}, button URLs, in-app title/body/link) is declared by its type, so
// the editor can show it as a known variable pill instead of plain text.
func TestSeededTemplatesUseOnlyDeclaredVariables(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()

	unknown := map[string]bool{}
	check := func(kind, typ string, texts ...string) {
		descriptors := notificationTypes.Descriptors(notificationTypes.NotificationType(typ))
		if descriptors == nil {
			return // retired type (participant.enrolled, ...): no editor shows it
		}
		declared := map[string]bool{}
		for _, d := range descriptors {
			declared[d.Name] = true
		}
		for _, text := range texts {
			for _, re := range []*regexp.Regexp{seedToken, seedVarNode} {
				for _, m := range re.FindAllStringSubmatch(text, -1) {
					if !declared[m[1]] {
						unknown[kind+" "+typ+" uses undeclared {{"+m[1]+"}}"] = true
					}
				}
			}
		}
	}

	rows, err := db.Pool.Query(ctx, `SELECT notification_type, subject, preheader, body::text FROM notification_email_templates`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var typ, subject, preheader, body string
		if err = rows.Scan(&typ, &subject, &preheader, &body); err != nil {
			t.Fatal(err)
		}
		check("email", typ, subject, preheader, body)
	}
	rows.Close()

	rows, err = db.Pool.Query(ctx, `SELECT notification_type, title, body, link, actions::text FROM notification_in_app_templates`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var typ, title, body, link, actions string
		if err = rows.Scan(&typ, &title, &body, &link, &actions); err != nil {
			t.Fatal(err)
		}
		check("in_app", typ, title, body, link, actions)
	}
	rows.Close()

	var list []string
	for k := range unknown {
		list = append(list, k)
	}
	sort.Strings(list)
	for _, k := range list {
		t.Error(k)
	}
}
