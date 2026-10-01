package emailUseCase_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	"github.com/cybericebox/daemon/internal/model/notification/emaildefaults"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

type noPresets struct{}

func (noPresets) ListPresets(context.Context) ([]emailModel.BlockPreset, error) { return nil, nil }

type platformFooters struct{}

func (platformFooters) Footer(context.Context, *uuid.UUID) (mailModel.Footer, error) {
	return mailUseCase.RenderPlatformFooter("support@cybericebox.com", "https://cybericebox.com"), nil
}

// sampleValues are realistic variable values for the previews.
var sampleValues = map[string]string{
	"Name": "Олена", "RegistrationURL": "https://id.cybericebox.com/setup?token=abc123",
	"ResetURL":   "https://id.cybericebox.com/reset-password?token=abc123",
	"ConfirmURL": "https://id.cybericebox.com/confirm-email?token=abc123",
	"InviteURL":  "https://id.cybericebox.com/setup?token=abc123", "SignInURL": "https://id.cybericebox.com/sign-in",
	"DeletionDate": "29.10.2026", "Challenge": "Веб: SQL-ін’єкція", "Points": "150",
	"event_name": "Весняний CTF 2026", "event_url": "https://spring-ctf.cybericebox.com/",
	"invite_url": "https://spring-ctf.cybericebox.com/invite", "team_url": "https://spring-ctf.cybericebox.com/participation?tab=team", "team_name": "Синя команда",
	"role_name": "модератором", "reason": "Лабораторія повідомила стан Failed",
	"start_at": "12.05.2026 10:00", "finish_at": "12.05.2026 18:00",
}

func eventLogo() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for x := 0; x < 96; x++ {
		for y := 0; y < 96; y++ {
			img.Set(x, y, color.RGBA{0x0E, 0x7C, 0x66, 0xFF})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// TestWritePreviews renders every default template (uk) to static HTML for
// review: EMAIL_PREVIEW_DIR=/some/dir go test -run TestWritePreviews.
func TestWritePreviews(t *testing.T) {
	dir := os.Getenv("EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("EMAIL_PREVIEW_DIR is not set")
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	eventID := uuid.Must(uuid.NewV7())
	eventBrand := fixedEventBrand{brand: emailUseCase.Brand{
		Colors: branding.Context{Brand: "#0E7C66", Accent: "#0E7C66", OnAccent: "#FFFFFF"},
		Logo:   eventLogo(), LogoContentType: "image/png", LogoAlt: "Весняний CTF 2026",
	}}
	var index strings.Builder
	index.WriteString(`<!doctype html><meta charset="utf-8"><title>Email previews</title><body style="font-family:sans-serif;max-width:900px;margin:32px auto"><h1>Шаблони листів</h1><ul>`)
	for _, tpl := range emaildefaults.All(emaildefaults.UK) {
		in := emailUseCase.PreviewInput{NotificationType: tpl.Type, Subject: tpl.Subject, Preheader: tpl.Preheader, Body: tpl.Body, Values: sampleValues}
		var scope *emailUseCase.EventPreviewScope
		if tpl.Participant {
			scope = &emailUseCase.EventPreviewScope{EventID: eventID, Brands: eventBrand}
		}
		out, err := emailUseCase.Preview(context.Background(), noPresets{}, nil, platformFooters{}, scope, in)
		require.NoError(t, err, tpl.Type)
		name := strings.ReplaceAll(tpl.Type, ".", "-") + ".html"
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(out.HTML), 0o644))
		brand := "платформа"
		if tpl.Participant {
			brand = "бренд заходу"
		}
		fmt.Fprintf(&index, `<li><a href="%s">%s</a> · %s · тема: %s</li>`, name, tpl.Type, brand, out.Subject)
	}
	index.WriteString(`</ul>`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(index.String()), 0o644))
}
