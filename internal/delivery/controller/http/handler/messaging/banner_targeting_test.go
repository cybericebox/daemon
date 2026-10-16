package messaging

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/rbac"
)

func TestPublicBannerRejectsInconsistentSiteContext(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	for _, query := range []string{"site=unknown", "site=main&event=" + id, "site=id&event=" + id, "site=event", "event=", "site=", "site=main&site=id", "event=" + id + "&event=" + id, "event=00000000-0000-0000-0000-000000000000"} {
		t.Run(query, func(t *testing.T) {
			uc := &fakeUseCase{}
			w := do(t, uc, http.MethodGet, "/api/banners?"+query, "", "")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(uc.visible) != 0 {
				t.Fatal("invalid context reached use case")
			}
		})
	}
}

func TestBannerDTOCarriesTargetingAndPreservesOmission(t *testing.T) {
	uc := &fakeUseCase{}
	id := uuid.Must(uuid.NewV7())
	event := uuid.Must(uuid.NewV7())
	body := `{"Text":"Hi","Level":"info","Audience":"everyone","IsActive":true,"TargetMode":"selected","TargetSites":["main","admin"],"TargetEventIDs":["` + event.String() + `"]}`
	w := do(t, uc, http.MethodPost, "/api/notifications/site-banners", string(rbac.PermNotificationsBannersWrite), body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	in := uc.createdInputs[0]
	if in.TargetMode == nil || *in.TargetMode != "selected" || len(in.TargetSites) != 2 || len(in.TargetEventIDs) != 1 || in.TargetEventIDs[0] != event {
		t.Fatalf("targeting lost: %+v", in)
	}
	var payload struct{ Data bannerResponse }
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.TargetMode != "selected" || len(payload.Data.TargetSites) != 2 || len(payload.Data.TargetEventIDs) != 1 {
		t.Fatalf("response lost management targeting: %+v", payload)
	}
	w = do(t, uc, http.MethodPut, "/api/notifications/site-banners/"+id.String(), string(rbac.PermNotificationsBannersWrite), `{"Text":"Old client","Level":"info","Audience":"everyone"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if uc.updatedInputs[0].TargetMode != nil {
		t.Fatal("omitted targeting became an explicit mode")
	}
	w = do(t, uc, http.MethodPost, "/api/notifications/site-banners", string(rbac.PermNotificationsBannersWrite), `{"TargetMode":"selected","TargetEventIDs":["bad-uuid"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid target uuid status=%d", w.Code)
	}
}

func TestPublicBannerContextAndPayload(t *testing.T) {
	event := uuid.Must(uuid.NewV7())
	for _, tc := range []struct{ query, site string }{{"", ""}, {"site=main", "main"}, {"site=id", "id"}, {"site=admin", "admin"}, {"site=exercises", "exercises"}, {"site=event&event=" + event.String(), "event"}, {"event=" + event.String(), "event"}} {
		t.Run(tc.query, func(t *testing.T) {
			uc := &fakeUseCase{}
			w := do(t, uc, http.MethodGet, "/api/banners?"+tc.query, "", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(uc.visibleSites) != 1 || uc.visibleSites[0] != tc.site {
				t.Fatalf("site=%v want %q", uc.visibleSites, tc.site)
			}
			for _, field := range []string{"TargetMode", "TargetSites", "TargetEventIDs", "ScopeEventID"} {
				if bytes.Contains(w.Body.Bytes(), []byte(field)) {
					t.Fatalf("public response exposes %s", field)
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("public banners must not be cached across sessions")
			}
		})
	}
}
