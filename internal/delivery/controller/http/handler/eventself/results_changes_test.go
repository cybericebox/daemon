package eventself

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func pollChanges(t *testing.T, h *Handler, eventID uuid.UUID, query, ifNoneMatch string) (*httptest.ResponseRecorder, resultsChangesResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/results/changes?"+query, nil)
	if ifNoneMatch != "" {
		ctx.Request.Header.Set("If-None-Match", ifNoneMatch)
	}
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	h.resultsChanges(ctx)
	var body struct{ Data resultsChangesResponse }
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body: %v %s", err, w.Body.String())
		}
	}
	return w, body.Data
}

func TestResultsChanges_PollsTheReplayAfterTheCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	var gotAfter int64 = -1
	replay := eventUseCase.LiveResultsReplay{LastRevision: 7, FreezeKey: "", Changes: []eventUseCase.LiveResultChangeView{{Revision: 6, Kind: "team_challenge_solved", Payload: json.RawMessage(`{}`)}}}
	h := NewEventSelfAPIHandler(fakeUseCase{live: func(_ context.Context, _ uuid.UUID, _ eventUseCase.ResultsAccess, after int64) (eventUseCase.LiveResultsReplay, error) {
		gotAfter = after
		return replay, nil
	}}, nil)

	w, got := pollChanges(t, h, eventID, "since=5", "")
	if w.Code != http.StatusOK || gotAfter != 5 || got.Revision != 7 || len(got.Changes) != 1 || got.SnapshotRequired {
		t.Fatalf("changes: %d %+v (after %d)", w.Code, got, gotAfter)
	}
	// Nothing new: the same answer is a cheap 304.
	if again, _ := pollChanges(t, h, eventID, "since=5", w.Header().Get("ETag")); again.Code != http.StatusNotModified {
		t.Fatalf("unchanged poll = %d", again.Code)
	}
	// Frozen solves of other teams advance the cursor without changes.
	replay = eventUseCase.LiveResultsReplay{LastRevision: 9}
	if _, got = pollChanges(t, h, eventID, "since=7", ""); got.Revision != 9 || len(got.Changes) != 0 || got.SnapshotRequired {
		t.Fatalf("skipped changes: %+v", got)
	}
}

func TestResultsChanges_AsksForASnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	replay := eventUseCase.LiveResultsReplay{SnapshotRequired: true}
	h := NewEventSelfAPIHandler(fakeUseCase{live: func(context.Context, uuid.UUID, eventUseCase.ResultsAccess, int64) (eventUseCase.LiveResultsReplay, error) {
		return replay, nil
	}}, nil)
	// The cursor is outside the change window.
	if _, got := pollChanges(t, h, eventID, "since=2", ""); !got.SnapshotRequired || got.Revision != 2 {
		t.Fatalf("outside the window: %+v", got)
	}
	// The viewer's freeze state changed since the previous poll.
	replay = eventUseCase.LiveResultsReplay{LastRevision: 4, FreezeKey: "2026-09-29T15:30:00Z", Changes: []eventUseCase.LiveResultChangeView{{Revision: 4}}}
	if _, got := pollChanges(t, h, eventID, "since=3&freeze=", ""); !got.SnapshotRequired || len(got.Changes) != 0 || got.FreezeKey != replay.FreezeKey {
		t.Fatalf("freeze started: %+v", got)
	}
	if _, got := pollChanges(t, h, eventID, "since=3&freeze=2026-09-29T15:30:00Z", ""); got.SnapshotRequired || got.Revision != 4 {
		t.Fatalf("same freeze: %+v", got)
	}
	if w, _ := pollChanges(t, h, eventID, "since=x", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d", w.Code)
	}
}
