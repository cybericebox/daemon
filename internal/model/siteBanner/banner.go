// Package siteBannerModel is the domain of site banners: one short authored
// line shown under the navbar of every app of its scope.
package siteBannerModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

type Audience string

const (
	AudienceEveryone     Audience = "everyone"
	AudienceSignedIn     Audience = "signed_in"
	AudienceParticipants Audience = "participants"
)

const (
	MaxTextLen  = 280
	MaxLabelLen = 60
	MaxURLLen   = 500
)

// Banner is a site banner. ScopeEventID nil is a platform banner (every app);
// otherwise it shows on that Event's site.
type Banner struct {
	ID           uuid.UUID
	ScopeEventID *uuid.UUID
	Text         string
	LinkURL      string
	LinkLabel    string
	Level        Level
	ActiveFrom   *time.Time
	ActiveTo     *time.Time
	Dismissible  bool
	Audience     Audience
	IsActive     bool
	CreatedBy    *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Input is the editable part of a banner.
type Input struct {
	Text        string
	LinkURL     string
	LinkLabel   string
	Level       Level
	ActiveFrom  *time.Time
	ActiveTo    *time.Time
	Dismissible bool
	Audience    Audience
	IsActive    bool
}

// Validate checks the input against the banner's scope. It also normalizes
// the text fields (trimmed).
func (in *Input) Validate(eventScoped bool) error {
	invalid := func(msg string) error { return notificationModel.ErrBannerInvalid.WithMessage(msg).Err() }
	in.Text = strings.TrimSpace(in.Text)
	in.LinkURL = strings.TrimSpace(in.LinkURL)
	in.LinkLabel = strings.TrimSpace(in.LinkLabel)
	if in.Text == "" || len([]rune(in.Text)) > MaxTextLen {
		return invalid("Banner text is required")
	}
	if len([]rune(in.LinkLabel)) > MaxLabelLen {
		return invalid("Banner link label is too long")
	}
	if in.LinkURL != "" && !SafeLink(in.LinkURL) {
		return invalid("Banner link must be an https address or a site path")
	}
	if in.LinkURL == "" {
		in.LinkLabel = ""
	}
	switch in.Level {
	case LevelInfo, LevelWarning, LevelCritical:
	default:
		return invalid("Unknown banner level")
	}
	switch in.Audience {
	case AudienceEveryone, AudienceSignedIn:
	case AudienceParticipants:
		if !eventScoped {
			return invalid("Participants audience needs an Event")
		}
	default:
		return invalid("Unknown banner audience")
	}
	if in.ActiveFrom != nil && in.ActiveTo != nil && !in.ActiveTo.After(*in.ActiveFrom) {
		return invalid("Banner end must be after its start")
	}
	return nil
}

// SafeLink allows a site path or an http(s) address, never a script scheme.
func SafeLink(raw string) bool {
	if len(raw) > MaxURLLen {
		return false
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return true
	}
	lower := strings.ToLower(raw)
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")
}

// New builds a banner from validated input.
func New(scope *uuid.UUID, createdBy *uuid.UUID, in Input, now time.Time) Banner {
	return Banner{
		ID: uuid.Must(uuid.NewV7()), ScopeEventID: scope, Text: in.Text, LinkURL: in.LinkURL, LinkLabel: in.LinkLabel,
		Level: in.Level, ActiveFrom: in.ActiveFrom, ActiveTo: in.ActiveTo, Dismissible: in.Dismissible,
		Audience: in.Audience, IsActive: in.IsActive, CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now,
	}
}
