package event_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type inviteCaptureUC struct {
	eventHandler.IUseCase
	noEmail []bool
}

func (u *inviteCaptureUC) InviteParticipants(_ context.Context, _, _ uuid.UUID, _ []eventUseCase.ParticipantInvitationInput, noEmail bool) ([]eventUseCase.ParticipantInvitationResult, error) {
	u.noEmail = append(u.noEmail, noEmail)
	return nil, nil
}

func (u *inviteCaptureUC) CreateManagedTeams(_ context.Context, _, _ uuid.UUID, _ []eventUseCase.BatchTeamInput, _, noEmail bool) (eventUseCase.BatchTeamsResult, error) {
	u.noEmail = append(u.noEmail, noEmail)
	return eventUseCase.BatchTeamsResult{}, nil
}

// Omitted SendEmails keeps the old behavior (send); only an explicit false is silent.
func TestSendEmailsDefaultsToSending(t *testing.T) {
	actor, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	base := "/api/events/" + id.String() + "/manage/"
	for _, tc := range []struct {
		path, body string
		want       bool
	}{
		{"participants/invitations", `{"Entries":[{"Email":"a@example.test"}]}`, false},
		{"participants/invitations", `{"Entries":[{"Email":"a@example.test"}],"SendEmails":true}`, false},
		{"participants/invitations", `{"Entries":[{"Email":"a@example.test"}],"SendEmails":false}`, true},
		{"teams/batch", `{"Teams":[]}`, false},
		{"teams/batch", `{"Teams":[],"SendEmails":false}`, true},
	} {
		uc := &inviteCaptureUC{}
		router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: uc}, actor)
		req := httptest.NewRequest(http.MethodPost, base+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || len(uc.noEmail) != 1 || uc.noEmail[0] != tc.want {
			t.Fatalf("%s %s: status=%d noEmail=%v want %v body=%s", tc.path, tc.body, rec.Code, uc.noEmail, tc.want, rec.Body.String())
		}
	}
}

func (u *inviteCaptureUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error { return nil }
