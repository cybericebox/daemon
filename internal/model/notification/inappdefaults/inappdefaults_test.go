package inappdefaults_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/notification/emaildefaults"
	"github.com/cybericebox/daemon/internal/model/notification/inappdefaults"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
)

var banned = regexp.MustCompile(`(?i)поді[яїюй]|івент|імейл|кукі|noreply|\bcookie|CyberICEBox`)

// Every default renders with the type's sample values, uses only variables the
// type guarantees on the in-app channel, keeps titles short and has no banned
// spelling.
func TestDefaults_RenderAndValidate(t *testing.T) {
	for _, lang := range []inappdefaults.Lang{inappdefaults.UK, inappdefaults.EN} {
		require.Len(t, inappdefaults.All(lang), len(inappdefaults.All(inappdefaults.UK)))
		for _, tpl := range inappdefaults.All(lang) {
			typ := notificationTypes.NotificationType(tpl.Type)
			require.NoError(t, notificationTypes.ValidateTemplateVariables(typ, notificationTypes.NotificationChannelInApp, tpl.Title, tpl.Body, tpl.Link), tpl.Type)
			vars := map[string]any{}
			for _, d := range notificationTypes.Descriptors(typ) {
				vars[d.Name] = d.Default
			}
			for _, s := range []string{tpl.Title, tpl.Body, tpl.Link} {
				out, err := render.RenderText(s, vars)
				require.NoError(t, err, tpl.Type)
				require.NotContains(t, out, "{{")
				require.False(t, banned.MatchString(out), out)
			}
			require.LessOrEqual(t, len([]rune(tpl.Title)), 40, tpl.Type)
		}
	}
}

// The in-app title of a type is the heading of its email.
func TestDefaults_TitlesMatchEmailHeadings(t *testing.T) {
	// emaildefaults holds the heading as the first heading node of the body.
	for _, tpl := range emaildefaults.All(emaildefaults.UK) {
		for _, in := range inappdefaults.All(inappdefaults.UK) {
			if in.Type == tpl.Type {
				require.Contains(t, string(tpl.Body), `"`+in.Title+`"`, tpl.Type)
			}
		}
	}
}
