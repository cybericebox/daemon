package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
)

// AssetKind distinguishes the brand logo from an uploaded file.
type AssetKind string

const (
	AssetLogo AssetKind = "logo"
	AssetFile AssetKind = "file"
)

// Asset is an image an email body references. FileID is zero for the logo.
type Asset struct {
	Kind   AssetKind
	FileID uuid.UUID
}

// RenderContext carries what rendering needs beyond the template itself.
type RenderContext struct {
	Brand branding.Context
	// LogoAlt is the alt text of the logo block: the event name for an event
	// logo; empty means the platform name.
	LogoAlt string
	// AssetSrc maps an asset to the <img src>; dispatch → "cid:…", preview → URL.
	// Nil defaults to the cid: form.
	AssetSrc func(Asset) string
}

// Rendered is a rendered email body plus the assets it references, unique and
// in first-use order (the inline parts a dispatch must attach).
type Rendered struct {
	HTML   string
	Assets []Asset
}

const cidDomain = "@cybericebox"

// CID is the Content-ID of an asset's inline MIME part.
func CID(a Asset) string {
	if a.Kind == AssetLogo {
		return "logo" + cidDomain
	}
	return a.FileID.String() + cidDomain
}

const themePrefix = "theme:"

// resolveToken maps a theme:* token to its brand colour; ok is false for an
// unknown token. Non-token values are returned unchanged.
func resolveToken(v string, b branding.Context) (string, bool) {
	name, isToken := strings.CutPrefix(v, themePrefix)
	if !isToken {
		return v, true
	}
	switch name {
	case "brand":
		return b.Brand, true
	case "accent":
		return b.Accent, true
	case "on_accent":
		return b.OnAccent, true
	default:
		return v, false
	}
}

// resolveTokens replaces every known theme:* value in st with the brand colour.
// Unknown tokens are left as-is (ValidateStyling keeps them out of storage;
// safeCSSValue neutralises any that slip through).
func resolveTokens(st *emailStyling, b branding.Context) {
	for _, f := range st.fields() {
		*f, _ = resolveToken(*f, b)
	}
}

// ValidateStyling rejects styling that is not a JSON object of strings or that
// uses an unknown theme:* token, with ErrInvalidTemplateStyling. Empty / null
// styling is valid (all defaults).
func ValidateStyling(raw json.RawMessage) error {
	if err := validateStyling(raw); err != nil {
		return notificationModel.ErrInvalidTemplateStyling.WithError(err).Err()
	}
	return nil
}

func validateStyling(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("styling must be a JSON object of strings: %w", err)
	}
	for k, v := range m {
		if _, ok := resolveToken(v, branding.Context{}); !ok {
			return fmt.Errorf("styling %q: unknown colour token %q", k, v)
		}
	}
	return nil
}
