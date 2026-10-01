package siteBannerModel_test

import (
	"strings"
	"testing"
	"time"

	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

func input() siteBannerModel.Input {
	return siteBannerModel.Input{Text: "Planned maintenance", Level: siteBannerModel.LevelInfo, Audience: siteBannerModel.AudienceEveryone, Dismissible: true, IsActive: true}
}

func TestInputValidate(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-time.Hour)
	cases := []struct {
		name   string
		event  bool
		mutate func(*siteBannerModel.Input)
		ok     bool
	}{
		{"plain banner", false, func(*siteBannerModel.Input) {}, true},
		{"empty text", false, func(in *siteBannerModel.Input) { in.Text = "  " }, false},
		{"text too long", false, func(in *siteBannerModel.Input) { in.Text = strings.Repeat("a", siteBannerModel.MaxTextLen+1) }, false},
		{"unknown level", false, func(in *siteBannerModel.Input) { in.Level = "fatal" }, false},
		{"critical level", false, func(in *siteBannerModel.Input) { in.Level = siteBannerModel.LevelCritical }, true},
		{"site path link", false, func(in *siteBannerModel.Input) { in.LinkURL = "/status" }, true},
		{"https link", false, func(in *siteBannerModel.Input) { in.LinkURL = "https://status.example.org" }, true},
		{"script link", false, func(in *siteBannerModel.Input) { in.LinkURL = "javascript:alert(1)" }, false},
		{"protocol-relative link", false, func(in *siteBannerModel.Input) { in.LinkURL = "//evil.example" }, false},
		{"unknown audience", false, func(in *siteBannerModel.Input) { in.Audience = "admins" }, false},
		{"platform participants audience", false, func(in *siteBannerModel.Input) { in.Audience = siteBannerModel.AudienceParticipants }, false},
		{"event participants audience", true, func(in *siteBannerModel.Input) { in.Audience = siteBannerModel.AudienceParticipants }, true},
		{"end before start", false, func(in *siteBannerModel.Input) { in.ActiveFrom, in.ActiveTo = &now, &earlier }, false},
		{"end after start", false, func(in *siteBannerModel.Input) { in.ActiveFrom, in.ActiveTo = &earlier, &now }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := input()
			c.mutate(&in)
			if err := in.Validate(c.event); (err == nil) != c.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestInputValidateDropsLabelWithoutLink(t *testing.T) {
	in := input()
	in.LinkLabel = "More"
	if err := in.Validate(false); err != nil {
		t.Fatal(err)
	}
	if in.LinkLabel != "" {
		t.Fatalf("label %q kept without a link", in.LinkLabel)
	}
}
