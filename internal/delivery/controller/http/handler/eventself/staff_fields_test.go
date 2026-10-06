package eventself

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

const (
	staffQuestion = "Internal attendance note"
	staffAnswer   = "VIP: seat 14, call before the start"
)

func staffDocument() eventContentModel.Document {
	return eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City"},
		{ID: "note", Type: eventContentModel.BlockField, Key: "note", Input: "long_text", Label: staffQuestion, StaffOnly: true},
	}}
}

// leakyUseCase hands the handler everything, staff-only data included, as a
// use case with a bug would. The handler layer must still hide it.
type leakyUseCase struct{ fakeUseCase }

func (leakyUseCase) GetParticipantForm(context.Context, uuid.UUID) (eventUseCase.ParticipantFormView, error) {
	return eventUseCase.ParticipantFormView{Version: 3, Enabled: true, Document: staffDocument(), Answered: 40}, nil
}

func (leakyUseCase) GetTeamFields(context.Context, uuid.UUID) (eventUseCase.ParticipantFormView, error) {
	return eventUseCase.ParticipantFormView{Version: 1, Enabled: true, Document: staffDocument()}, nil
}

func (leakyUseCase) GetOwnParticipantFormAnswers(context.Context, uuid.UUID, uuid.UUID) (map[string]any, error) {
	return map[string]any{"city": "Kyiv", "note": staffAnswer}, nil
}

func (leakyUseCase) GetOwnParticipantAnswers(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.OwnParticipantAnswersView, error) {
	return eventUseCase.OwnParticipantAnswersView{
		Form:    eventUseCase.ParticipantFormView{Version: 3, Enabled: true, Document: staffDocument()},
		Answers: map[string]any{"city": "Kyiv", "note": staffAnswer}, Editable: true,
	}, nil
}

func (leakyUseCase) UpdateOwnParticipantAnswers(context.Context, uuid.UUID, uuid.UUID, map[string]any) (eventUseCase.OwnParticipantAnswersView, error) {
	return leakyUseCase{}.GetOwnParticipantAnswers(context.Background(), uuid.Nil, uuid.Nil)
}

func TestParticipantsNeverReceiveStaffOnlyFields(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	router := participantPagesRouter(NewEventSelfAPIHandler(leakyUseCase{}, allowAllProtection{}), userID, eventID)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/events/self/participant-form", ""},
		{http.MethodGet, "/api/events/self/team-fields", ""},
		{http.MethodGet, "/api/events/self/participant-answers", ""},
		{http.MethodPut, "/api/events/self/participant-answers", `{"Answers":{"city":"Kyiv"}}`},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		body := w.Body.String()
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, body)
		}
		if strings.Contains(body, staffQuestion) || strings.Contains(body, staffAnswer) || strings.Contains(body, `"note"`) || strings.Contains(body, "staffOnly") {
			t.Fatalf("%s %s leaked a staff-only field to a participant: %s", tc.method, tc.path, body)
		}
		if !strings.Contains(body, "city") {
			t.Fatalf("%s %s lost the participant's own field: %s", tc.method, tc.path, body)
		}
	}
}

func TestParticipantAnswersCarryWhatIsStillMissing(t *testing.T) {
	body := mustJSON(t, toParticipantAnswersResponse(eventUseCase.OwnParticipantAnswersView{
		Form: eventUseCase.ParticipantFormView{Version: 2, Enabled: true}, Missing: []string{"school"}, Blocking: true,
	}))
	if !strings.Contains(body, `"Missing":["school"]`) || !strings.Contains(body, `"Blocking":true`) {
		t.Fatalf("answers response = %s", body)
	}
	empty := mustJSON(t, toParticipantAnswersResponse(eventUseCase.OwnParticipantAnswersView{}))
	if !strings.Contains(empty, `"Missing":[]`) || !strings.Contains(empty, `"Blocking":false`) {
		t.Fatalf("an empty view must still carry a list: %s", empty)
	}
	team := mustJSON(t, toOwnTeamResponse(eventUseCase.OwnTeamView{MissingFields: []string{"motto"}, BlockingFields: true}))
	if !strings.Contains(team, `"MissingFields":["motto"]`) || !strings.Contains(team, `"BlockingFields":true`) {
		t.Fatalf("team response = %s", team)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
