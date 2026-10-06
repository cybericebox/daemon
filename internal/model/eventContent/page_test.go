package eventContentModel

import (
	"encoding/json"
	"github.com/cybericebox/daemon/internal/model/eventContent/testutil"
	"testing"

	"github.com/gofrs/uuid"
)

func TestPageAndLiveLayoutAcceptOnlySafeDeclaredConfiguration(t *testing.T) {
	page := Page{
		ID:              uuid.Must(uuid.NewV7()),
		EventID:         uuid.Must(uuid.NewV7()),
		Slug:            "rules-and-conduct",
		Title:           "Rules and conduct",
		Document:        Document{Blocks: []Block{{ID: "rules", Type: BlockText, RichText: testutil.RichText("Be kind.")}}},
		Visibility:      PageVisibilityParticipant,
		Navigation:      PageNavigationNavbar,
		NavigationOrder: 3,
	}
	if err := page.Validate(); err != nil {
		t.Fatalf("valid static page rejected: %v", err)
	}

	layout := DefaultLiveLayout()
	if err := layout.Validate(); err != nil {
		t.Fatalf("valid live layout rejected: %v", err)
	}

	for _, invalid := range []Page{
		{Slug: "Not a slug", Title: "Rules", Document: page.Document, Visibility: PageVisibilityPublic},
		{Slug: "challenges", Title: "Rules", Document: page.Document, Visibility: PageVisibilityPublic},
		{Slug: "manage", Title: "Rules", Document: page.Document, Visibility: PageVisibilityPublic},
		{Slug: "rules", Title: "", Document: page.Document, Visibility: PageVisibilityPublic},
		{Slug: "rules", Title: "Rules", Document: page.Document, Visibility: PageVisibility(99)},
		{Slug: "rules", Title: "Rules", Document: page.Document, Navigation: PageNavigation(99)},
		{Slug: "rules", Title: "Rules", Document: page.Document, Navigation: PageNavigation(2)},
		{Slug: "rules", Title: "Rules", Document: page.Document, Navigation: PageNavigation(3)},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid page %#v accepted", invalid)
		}
	}
	overlap := DefaultLiveLayout()
	overlap.Widgets[1].X = 2
	outside := DefaultLiveLayout()
	outside.Widgets[1].X = 10
	unsupported := DefaultLiveLayout()
	unsupported.Widgets[0].Type = "custom-html"
	badLogo := DefaultLiveLayout()
	badLogo.Widgets[3].Props["logos"] = json.RawMessage(`["javascript:alert(1)"]`)
	tinyGrid := DefaultLiveLayout()
	tinyGrid.Grid = LiveGrid{Cols: 2, Rows: 2}
	tinyGrid.Widgets = []LiveWidget{{ID: "qr", Type: "qr", X: 1, Y: 1, W: 1, H: 1, Props: map[string]json.RawMessage{}}}
	for _, invalid := range []LiveLayout{overlap, outside, unsupported, badLogo, tinyGrid} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid live layout %#v accepted", invalid)
		}
	}
}

func TestPageReservesSystemRoutes(t *testing.T) {
	for _, slug := range []string{"invite", "participation"} {
		if err := (Page{Slug: slug, Title: "System"}).Validate(); err == nil || err.Error() != "page slug is reserved" {
			t.Fatalf("slug %q must be reserved, got %v", slug, err)
		}
	}
}
