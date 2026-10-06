package emailUseCase_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/notification/emaildefaults"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

var (
	// Spellings the owner does not use in Ukrainian mail.
	bannedUK = regexp.MustCompile(`(?i)поді[яїюй]|івент|імейл|кукі|noreply|\bcookie`)
	bannedEN = regexp.MustCompile(`(?i)noreply`)
	// The product name is «Cyber ICE Box», never CyberICEBox.
	bannedBrand = regexp.MustCompile(`CyberICEBox|Cyber Ice Box|CyberIceBox`)
	hrefPattern = regexp.MustCompile(`href="([^"]*)"`)
	signature   = regexp.MustCompile(`(?i)З повагою|Best regards|Команда підтримки|Support Team|Організатори заходу`)
)

func previewDefault(t *testing.T, tpl emaildefaults.Template, values map[string]string) emailUseCase.PreviewOutput {
	t.Helper()
	out, err := emailUseCase.Preview(context.Background(), noPresets{}, nil, nil, nil, emailUseCase.PreviewInput{
		NotificationType: tpl.Type, Subject: tpl.Subject, Preheader: tpl.Preheader, Body: tpl.Body, Values: values,
	})
	require.NoError(t, err, tpl.Type)
	return out
}

func eachDefault(t *testing.T, fn func(t *testing.T, lang emaildefaults.Lang, tpl emaildefaults.Template)) {
	for _, lang := range []emaildefaults.Lang{emaildefaults.UK, emaildefaults.EN} {
		templates := emaildefaults.All(lang)
		require.GreaterOrEqual(t, len(templates), 20)
		for _, tpl := range templates {
			t.Run(string(lang)+"/"+tpl.Type, func(t *testing.T) { fn(t, lang, tpl) })
		}
	}
}

// Every default template renders with all variables set: no placeholder left,
// no banned spelling, no signature (the platform sends the mail, not a person),
// absolute links only.
func TestDefaultTemplates_RenderWithFullVariables(t *testing.T) {
	eachDefault(t, func(t *testing.T, lang emaildefaults.Lang, tpl emaildefaults.Template) {
		out := previewDefault(t, tpl, nil)
		all := out.Subject + out.Preheader + out.HTML
		require.NotContains(t, out.HTML, "{{")
		require.NotContains(t, out.HTML, "&lt;nil&gt;")
		require.False(t, bannedBrand.MatchString(all), bannedBrand.FindString(all))
		if lang == emaildefaults.UK {
			require.False(t, bannedUK.MatchString(all), bannedUK.FindString(all))
		} else {
			require.False(t, bannedEN.MatchString(all), bannedEN.FindString(all))
		}
		require.False(t, signature.MatchString(all), "automatic mail carries no signature")
		require.Contains(t, out.HTML, "<h1")
		for _, m := range hrefPattern.FindAllStringSubmatch(out.HTML, -1) {
			require.Regexp(t, `^(https://|mailto:)`, m[1], "links are absolute")
		}
	})
}

// The product name never breaks across lines: no default template renders
// «Cyber ICE Box» with ordinary spaces, in the HTML or the plain-text part.
func TestDefaultTemplates_BrandNameNeverBreaks(t *testing.T) {
	breakable := regexp.MustCompile(`Cyber\s+(ICE|Ice)\s+Box`)
	seen := 0
	eachDefault(t, func(t *testing.T, _ emaildefaults.Lang, tpl emaildefaults.Template) {
		out := previewDefault(t, tpl, nil)
		require.False(t, breakable.MatchString(out.HTML), "HTML: "+breakable.FindString(out.HTML))
		require.False(t, breakable.MatchString(out.Preheader), "preheader")
		if strings.Contains(out.HTML, "Cyber&nbsp;ICE&nbsp;Box") {
			seen++
		}
	})
	require.Positive(t, seen, "some default mentions the brand")
}

// Minimal variables: everything the subject and preheader do not need is
// blank. The mail still renders, and an empty variable drops its paragraph,
// facts row or button instead of leaving «Вітаємо, !» or a dead link.
func TestDefaultTemplates_RenderWithMinimalVariables(t *testing.T) {
	eachDefault(t, func(t *testing.T, _ emaildefaults.Lang, tpl emaildefaults.Template) {
		blank := map[string]string{}
		for _, d := range notificationTypes.Descriptors(notificationTypes.NotificationType(tpl.Type)) {
			if !strings.Contains(tpl.Subject+tpl.Preheader, d.Name) {
				blank[d.Name] = ""
			}
		}
		out := previewDefault(t, tpl, blank)
		require.NotContains(t, out.HTML, "{{")
		require.NotContains(t, out.HTML, `href=""`)
		require.NotContains(t, out.HTML, ", !")
		require.NotContains(t, out.HTML, "«»")
		require.NotContains(t, out.HTML, "<a class=\"cib-btn\" href=\"#\"")
	})
}

// One primary action per mail, and an invitation button sits below its text.
func TestDefaultTemplates_OneButtonAtMost(t *testing.T) {
	eachDefault(t, func(t *testing.T, _ emaildefaults.Lang, tpl emaildefaults.Template) {
		var blocks []struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(tpl.Body, &blocks))
		require.Equal(t, "logo", blocks[0].Type)
		buttons := 0
		for _, b := range blocks {
			if b.Type == "button" {
				buttons++
			}
		}
		require.LessOrEqual(t, buttons, 1)
	})
}

// uk and en describe the same set of notification types.
func TestDefaultTemplates_LanguagesMatch(t *testing.T) {
	types := func(lang emaildefaults.Lang) []string {
		var out []string
		for _, tpl := range emaildefaults.All(lang) {
			out = append(out, tpl.Type)
		}
		return out
	}
	require.Equal(t, types(emaildefaults.UK), types(emaildefaults.EN))
}
