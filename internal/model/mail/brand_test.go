package mailModel_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	mailModel "github.com/cybericebox/daemon/internal/model/mail"
)

func TestKeepBrandText_JoinsWithNoBreakSpaces(t *testing.T) {
	require.Equal(t, "© Cyber ICE Box", mailModel.KeepBrandText("© Cyber ICE Box"))
	require.Equal(t, "Cyber Ice Box", mailModel.KeepBrandText("Cyber Ice Box"))
	require.Equal(t, "Cyber ICE Box", mailModel.KeepBrandText("Cyber\nICE  Box"))
	require.Equal(t, "Cyber ICE Box", mailModel.KeepBrandText("Cyber ICE Box"))
	require.Equal(t, "CyberICEBox", mailModel.KeepBrandText("CyberICEBox"))
}

func TestKeepBrandHTML_UsesEntities(t *testing.T) {
	require.Equal(t, `<p>Cyber&nbsp;ICE&nbsp;Box</p>`, mailModel.KeepBrandHTML(`<p>Cyber ICE Box</p>`))
	require.Equal(t, `alt="Cyber&nbsp;ICE&nbsp;Box"`, mailModel.KeepBrandHTML(`alt="Cyber ICE Box"`))
}

func TestFooterAppend_KeepsBrandUnbreakableInBothParts(t *testing.T) {
	footer := mailModel.Footer{HTML: "<p>Cyber ICE Box · x</p>", Text: "Cyber ICE Box · x"}
	html, text := footer.Append("<p>Вітаємо в Cyber ICE Box</p>", "Вітаємо в Cyber ICE Box")
	require.NotRegexp(t, `Cyber\s+ICE`, html)
	require.NotRegexp(t, `Cyber\s+ICE`, text)
	require.Contains(t, html, "Cyber&nbsp;ICE&nbsp;Box")
	require.Contains(t, text, "Cyber ICE Box")
	// no footer: the body is still normalised
	html, text = mailModel.Footer{}.Append("Cyber ICE Box", "Cyber ICE Box")
	require.Equal(t, "Cyber&nbsp;ICE&nbsp;Box", html)
	require.Equal(t, "Cyber ICE Box", text)
}

// The From display name is a header, not rendered text: it keeps ordinary spaces.
func TestPlatformSenderNameKeepsOrdinarySpaces(t *testing.T) {
	require.Equal(t, "Cyber ICE Box", mailModel.Identity{}.WithPlatformDefaults("x.y").FromName)
}
