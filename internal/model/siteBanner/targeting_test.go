package siteBannerModel_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

func TestTargetingValidation(t *testing.T) {
	id := uuid.Must(uuid.FromString("11111111-1111-4111-8111-111111111111"))
	for _, tc := range []struct {
		name, mode    string
		sites         []string
		events        []uuid.UUID
		scoped, valid bool
	}{
		{name: "legacy omission", valid: true},
		{name: "selected services and event", mode: "selected", sites: []string{"main", "admin"}, events: []uuid.UUID{id}, valid: true},
		{name: "all events", mode: "selected", sites: []string{"event"}, valid: true},
		{name: "empty selection", mode: "selected"},
		{name: "unknown mode", mode: "some", sites: []string{"main"}},
		{name: "unknown service", mode: "selected", sites: []string{"unknown"}},
		{name: "targets without mode", sites: []string{"main"}},
		{name: "event owner cannot target other event", mode: "selected", events: []uuid.UUID{id}, scoped: true},
		{name: "event owner cannot target main", mode: "selected", sites: []string{"main"}, scoped: true},
		{name: "event default", mode: "all", scoped: true, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input()
			if tc.mode != "" {
				mode := siteBannerModel.TargetMode(tc.mode)
				in.TargetMode = &mode
			}
			in.TargetSites = tc.sites
			in.TargetEventIDs = tc.events
			if err := in.Validate(tc.scoped); (err == nil) != tc.valid {
				t.Fatalf("Validate()=%v valid=%v", err, tc.valid)
			}
		})
	}
}

func TestTargetingNormalizesDuplicates(t *testing.T) {
	in := input()
	mode := siteBannerModel.TargetMode("selected")
	in.TargetMode = &mode
	id := uuid.Must(uuid.FromString("11111111-1111-4111-8111-111111111111"))
	in.TargetSites = []string{"admin", "main", "admin"}
	in.TargetEventIDs = []uuid.UUID{id, id}
	if err := in.Validate(false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.TargetSites, []string{"admin", "main"}) || !reflect.DeepEqual(in.TargetEventIDs, []uuid.UUID{id}) {
		t.Fatalf("not normalized: %+v", in)
	}
}

func TestNewBannerDefaultsLegacyTargetingAndClearsAllSelection(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	in := input()
	if err := in.Validate(false); err != nil {
		t.Fatal(err)
	}
	b := siteBannerModel.New(nil, nil, in, now)
	if b.TargetMode != siteBannerModel.TargetAll || len(b.TargetSites) != 0 || len(b.TargetEventIDs) != 0 {
		t.Fatalf("legacy create: %+v", b)
	}
	mode := siteBannerModel.TargetAll
	in.TargetMode = &mode
	in.TargetSites = []string{"main"}
	in.TargetEventIDs = []uuid.UUID{uuid.Must(uuid.NewV7())}
	if err := in.Validate(false); err != nil {
		t.Fatal(err)
	}
	b = siteBannerModel.New(nil, nil, in, now)
	if b.TargetMode != siteBannerModel.TargetAll || len(b.TargetSites) != 0 || len(b.TargetEventIDs) != 0 {
		t.Fatalf("all mode retained stale targets: %+v", b)
	}
}
