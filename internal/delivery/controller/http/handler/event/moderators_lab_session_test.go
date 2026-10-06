package event_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

type moderatorsLabLinkUC struct {
	accessContractUC
	calls [][3]uuid.UUID
}

func (u *moderatorsLabLinkUC) OpenModeratorsLabLink(_ context.Context, eventID, userID, challengeID uuid.UUID, _ string, _ int32) (labaccess.Link, error) {
	u.calls = append(u.calls, [3]uuid.UUID{eventID, userID, challengeID})
	return labaccess.Link{URL: "https://web-abc123.challenges.example.com/_auth?t=jwt", Token: "jwt", ExpiresAt: time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)}, nil
}

func linkPath(eventID, challengeID uuid.UUID) string {
	return "/api/events/" + eventID.String() + "/manage/labs/moderators/challenges/" + challengeID.String() + "/lab/link"
}

func TestModeratorsLabLinkReturnsTheLinkForTheManager(t *testing.T) {
	actor, eventID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &moderatorsLabLinkUC{}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, linkPath(eventID, challengeID), strings.NewReader(`{"Device":"web","Port":80}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ExpiresAt"`) || !strings.Contains(w.Body.String(), `/_auth?t=jwt`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(u.calls) != 1 || u.calls[0] != [3]uuid.UUID{eventID, actor, challengeID} {
		t.Fatalf("calls = %v", u.calls)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("the platform sets no lab cookie: %+v", w.Result().Cookies())
	}
}

func TestModeratorsLabLinkRequiresManageAccess(t *testing.T) {
	actor, eventID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &moderatorsLabLinkUC{accessContractUC: accessContractUC{manageErr: eventManagerModel.ErrEventManagementForbidden.Err()}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, linkPath(eventID, challengeID), strings.NewReader(`{"Device":"web","Port":80}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || len(u.calls) != 0 {
		t.Fatalf("viewer must not get a link: status=%d calls=%v", w.Code, u.calls)
	}
}
