package inappdefaults_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/notification/emaildefaults"
	"github.com/cybericebox/daemon/internal/model/notification/inappdefaults"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
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

// Every readiness alarm kind has a human text in both languages, and the default alarm template never prints
// the raw enum.
func TestAlarmReason_CoversEveryKind(t *testing.T) {
	kinds := []calModel.AlarmKind{calModel.AlarmNotPlaced, calModel.AlarmAgentLost, calModel.AlarmAgentShrunk, calModel.AlarmNotConnected, calModel.AlarmClusterSyncFailed, calModel.AlarmClusterShrinkHeld}
	for _, lang := range []inappdefaults.Lang{inappdefaults.UK, inappdefaults.EN} {
		seen := map[string]bool{}
		for _, kind := range kinds {
			text := inappdefaults.AlarmReason(lang, string(kind))
			require.NotEqual(t, string(kind), text, "%s %s has no text", lang, kind)
			require.NotContains(t, text, "_")
			require.False(t, seen[text], "texts must differ: %s", text)
			seen[text] = true
		}
	}
	require.Equal(t, "не вміщається на жоден агент", inappdefaults.AlarmReason(inappdefaults.UK, "not_placed"))
	require.Equal(t, "fits no agent", inappdefaults.AlarmReason(inappdefaults.EN, "not_placed"))
}

// Every cluster reservation notice has its text in Ukrainian and in English.
func TestTemplatesBothLanguages(t *testing.T) {
	for _, typ := range []string{"resources.reservation.ending", "resources.reservation.reduced", "testlab.ending", "testlab.ended"} {
		for _, lang := range []inappdefaults.Lang{inappdefaults.UK, inappdefaults.EN} {
			found := false
			for _, tpl := range inappdefaults.All(lang) {
				if tpl.Type == typ {
					found = tpl.Title != "" && tpl.Body != ""
				}
			}
			require.True(t, found, "%s %s", typ, lang)
		}
		require.True(t, inappdefaults.StartupSeeded[typ], typ)
	}
}
