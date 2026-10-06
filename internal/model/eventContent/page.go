package eventContentModel

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

type PageVisibility int16

const (
	PageVisibilityPublic PageVisibility = iota
	PageVisibilityParticipant
	PageVisibilityManager
)

type PageNavigation int16

// The event site has a single menu, the navbar: a page is either in it or not.
const (
	PageNavigationNone PageNavigation = iota
	PageNavigationNavbar
)

// Page is an optional static event page. Landing remains separate because it
// is mandatory and has no slug or navigation placement.
//
// The regular fields are the published version. A page that was never
// published (PublishedAt nil) mirrors its draft there and is invisible to
// every public route. Draft holds the editor's saved, unpublished version.
type Page struct {
	ID              uuid.UUID
	EventID         uuid.UUID
	Slug            string
	Title           string
	Document        Document
	Visibility      PageVisibility
	Navigation      PageNavigation
	NavigationOrder int32
	Draft           *PageDraft
	PublishedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PageDraft is a saved but unpublished page version. NavigationAfter is the
// navbar placement chosen in the editor (see PlaceInNavigation); it is applied
// only when the draft is published.
type PageDraft struct {
	Slug            string         `json:"Slug"`
	Title           string         `json:"Title"`
	Document        Document       `json:"Document"`
	Visibility      PageVisibility `json:"Visibility"`
	Navigation      PageNavigation `json:"Navigation"`
	NavigationAfter string         `json:"NavigationAfter,omitempty"`
}

// Published reports whether the page is visible on the public site.
func (p Page) Published() bool { return p.PublishedAt != nil }

// EditorSlug is the address the editor knows the page by: the draft slug
// while a draft exists, the published slug otherwise.
func (p Page) EditorSlug() string {
	if p.Draft != nil {
		return p.Draft.Slug
	}
	return p.Slug
}

// WithDraftValues returns the page as it will look once the draft is
// published (order excluded). Without a draft it is the page itself.
func (p Page) WithDraftValues() Page {
	if p.Draft == nil {
		return p
	}
	p.Slug, p.Title, p.Document = p.Draft.Slug, p.Draft.Title, p.Draft.Document
	p.Visibility, p.Navigation = p.Draft.Visibility, p.Draft.Navigation
	return p
}

var pageSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var reservedPageSlugs = map[string]struct{}{
	"api": {}, "challenges": {}, "scoreboard": {}, "team": {}, "teams": {}, "vpn": {}, "join": {}, "invite": {}, "participation": {}, "forms": {}, "live": {}, "manage": {}, "p": {}, "cabinet": {},
}

func (p Page) Validate() error {
	slug := strings.TrimSpace(p.Slug)
	if len(slug) == 0 || len(slug) > 128 || !pageSlugPattern.MatchString(slug) {
		return errors.New("page slug is invalid")
	}
	if _, reserved := reservedPageSlugs[slug]; reserved {
		return errors.New("page slug is reserved")
	}
	if title := strings.TrimSpace(p.Title); title == "" || len(title) > 255 {
		return errors.New("page title is invalid")
	}
	if p.Visibility < PageVisibilityPublic || p.Visibility > PageVisibilityManager {
		return errors.New("page visibility is invalid")
	}
	if p.Navigation != PageNavigationNone && p.Navigation != PageNavigationNavbar {
		return errors.New("page navigation is invalid")
	}
	if p.Visibility == PageVisibilityManager && p.Navigation != PageNavigationNone {
		return errors.New("manager page cannot be placed in participant navigation")
	}
	if p.Draft != nil && !validNavigationAfter(p.Draft.NavigationAfter) {
		return errors.New("page navigation placement is invalid")
	}
	return p.Document.ValidateContent()
}

// ValidateContent checks a block document intended for landing/static pages.
// Field blocks are intentionally reserved for forms. A text block has no
// block-level alignment: rich text aligns each paragraph itself.
func (d Document) ValidateContent() error {
	for _, block := range d.Blocks {
		if block.Type == BlockField {
			return errors.New("content document cannot contain form fields")
		}
		if block.Type == BlockText && block.Layout != "" {
			return errors.New("text alignment is set per paragraph")
		}
	}
	return d.Validate()
}

// Settings are the non-page content fields stored on the event itself.
type Settings struct {
	Landing      Document
	LandingDraft *Document
	Live         LiveLayout
	LiveDraft    *LiveLayout
}

// Statistics contains only event-wide aggregates suitable for configured
// content. It intentionally has no submissions, flags, secrets, or
// per-team values.
type Statistics struct {
	TeamCount, ApprovedTeamCount               int64
	ParticipantCount, ApprovedParticipantCount int64
	ChallengeCount, PublishedChallengeCount    int64
	SolvedChallengeCount, SolveCount           int64
}

// ValidateStored checks settings loaded from storage; live layouts use the
// tolerant read-side rules (see LiveLayout.ValidateStored).
func (s Settings) ValidateStored() error {
	if err := s.Landing.ValidateContent(); err != nil {
		return err
	}
	if err := s.Live.ValidateStored(); err != nil {
		return err
	}
	if s.LiveDraft != nil {
		return s.LiveDraft.ValidateStored()
	}
	return nil
}
