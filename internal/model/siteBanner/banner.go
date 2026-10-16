// Package siteBannerModel is the domain of site banners: one short authored
// line shown above the navbar of every app of its scope.
package siteBannerModel

import (
	"slices"
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

type TargetMode string

const (
	TargetAll      TargetMode = "all"
	TargetSelected TargetMode = "selected"
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

// Banner is a site banner. Global banners use targeting to select their sites.
// ScopeEventID restricts an event-owned banner to that Event regardless of targeting.
type Banner struct {
	TargetMode     TargetMode
	TargetSites    []string
	TargetEventIDs []uuid.UUID
	ID             uuid.UUID
	ScopeEventID   *uuid.UUID
	Text           string
	LinkURL        string
	LinkLabel      string
	Level          Level
	ActiveFrom     *time.Time
	ActiveTo       *time.Time
	Dismissible    bool
	Audience       Audience
	IsActive       bool
	CreatedBy      *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Input is the editable part of a banner.
type Input struct {
	// Nil mode means preserve targeting on update and default to all on create.
	TargetMode     *TargetMode
	TargetSites    []string
	TargetEventIDs []uuid.UUID
	Text           string
	LinkURL        string
	LinkLabel      string
	Level          Level
	ActiveFrom     *time.Time
	ActiveTo       *time.Time
	Dismissible    bool
	Audience       Audience
	IsActive       bool
}

// Validate checks the input against the banner's scope. It also normalizes
// the text fields (trimmed).
func (in *Input) Validate(eventScoped bool) error {
	in.Text = strings.TrimSpace(in.Text)
	in.LinkURL = strings.TrimSpace(in.LinkURL)
	in.LinkLabel = strings.TrimSpace(in.LinkLabel)
	if in.Text == "" || len([]rune(in.Text)) > MaxTextLen {
		return invalidBannerInput("Banner text is required")
	}
	if len([]rune(in.LinkLabel)) > MaxLabelLen {
		return invalidBannerInput("Banner link label is too long")
	}
	if in.LinkURL != "" && !SafeLink(in.LinkURL) {
		return invalidBannerInput("Banner link must be an https address or a site path")
	}
	if in.LinkURL == "" {
		in.LinkLabel = ""
	}
	switch in.Level {
	case LevelInfo, LevelWarning, LevelCritical:
	default:
		return invalidBannerInput("Unknown banner level")
	}
	switch in.Audience {
	case AudienceEveryone, AudienceSignedIn:
	case AudienceParticipants:
		if !eventScoped {
			return invalidBannerInput("Participants audience needs an Event")
		}
	default:
		return invalidBannerInput("Unknown banner audience")
	}
	if in.ActiveFrom != nil && in.ActiveTo != nil && !in.ActiveTo.After(*in.ActiveFrom) {
		return invalidBannerInput("Banner end must be after its start")
	}
	return in.validateTargeting(eventScoped)
}

// validateTargeting preserves nil mode so old updates can leave targeting unchanged.
func (in *Input) validateTargeting(eventScoped bool) error {
	if in.TargetMode == nil {
		if len(in.TargetSites) != 0 || len(in.TargetEventIDs) != 0 {
			return invalidBannerInput("Target mode is required with targets")
		}
		return nil
	}
	if *in.TargetMode != TargetAll && *in.TargetMode != TargetSelected {
		return invalidBannerInput("Unknown banner target mode")
	}
	if eventScoped && (*in.TargetMode != TargetAll || len(in.TargetSites) != 0 || len(in.TargetEventIDs) != 0) {
		return invalidBannerInput("Event banners cannot change their site scope")
	}
	for _, site := range in.TargetSites {
		if !ValidSite(site) {
			return invalidBannerInput("Unknown banner target site")
		}
	}
	if *in.TargetMode == TargetAll {
		in.TargetSites = nil
		in.TargetEventIDs = nil
		return nil
	}
	if len(in.TargetSites) == 0 && len(in.TargetEventIDs) == 0 {
		return invalidBannerInput("Select at least one banner target")
	}
	sites := make([]string, 0, len(in.TargetSites))
	for _, site := range in.TargetSites {
		if !slices.Contains(sites, site) {
			sites = append(sites, site)
		}
	}
	in.TargetSites = sites
	events := make([]uuid.UUID, 0, len(in.TargetEventIDs))
	for _, id := range in.TargetEventIDs {
		if id == uuid.Nil {
			return invalidBannerInput("Invalid target Event id")
		}
		if !slices.Contains(events, id) {
			events = append(events, id)
		}
	}
	in.TargetEventIDs = events
	return nil
}

func invalidBannerInput(message string) error {
	return notificationModel.ErrBannerInvalid.WithMessage(message).Err()
}

// ValidSite reports whether site is a public banner context.
func ValidSite(site string) bool {
	switch site {
	case "main", "id", "admin", "exercises", "event":
		return true
	}
	return false
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
	mode := TargetAll
	if in.TargetMode != nil {
		mode = *in.TargetMode
	}
	return Banner{
		TargetMode: mode, TargetSites: in.TargetSites, TargetEventIDs: in.TargetEventIDs,
		ID: uuid.Must(uuid.NewV7()), ScopeEventID: scope, Text: in.Text, LinkURL: in.LinkURL, LinkLabel: in.LinkLabel,
		Level: in.Level, ActiveFrom: in.ActiveFrom, ActiveTo: in.ActiveTo, Dismissible: in.Dismissible,
		Audience: in.Audience, IsActive: in.IsActive, CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now,
	}
}
