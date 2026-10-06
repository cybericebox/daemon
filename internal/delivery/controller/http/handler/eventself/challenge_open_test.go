package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/rbac"
)

func (fakeUseCase) OpenOwnChallenge(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

type openChallengeFake struct {
	fakeUseCase
	calls [][3]uuid.UUID
}

func (f *openChallengeFake) OpenOwnChallenge(_ context.Context, eventID, userID, challengeID uuid.UUID) error {
	f.calls = append(f.calls, [3]uuid.UUID{eventID, userID, challengeID})
	return nil
}

func TestOpenChallengeRecordsForTheAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	fake := &openChallengeFake{}
	h := NewEventSelfAPIHandler(fake, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/teams/challenges/"+challengeID.String()+"/open", nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "challengeID", Value: challengeID.String()}}
	h.openChallenge(ctx)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if len(fake.calls) != 1 || fake.calls[0] != [3]uuid.UUID{eventID, userID, challengeID} {
		t.Fatalf("OpenOwnChallenge calls: %v", fake.calls)
	}
}

func TestOpenChallengeRejectsABadChallengeID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &openChallengeFake{}
	h := NewEventSelfAPIHandler(fake, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	eventID := uuid.Must(uuid.NewV7())
	request := httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/teams/challenges/nope/open", nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7())}))
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "challengeID", Value: "nope"}}
	h.openChallenge(ctx)
	if w.Code != http.StatusBadRequest || len(fake.calls) != 0 {
		t.Fatalf("status = %d calls = %v, want 400 and no call", w.Code, fake.calls)
	}
}
