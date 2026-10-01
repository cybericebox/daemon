package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
)

func TestListParticipantFormAnswersExcludesOtherFormsAndKeepsQuestionVersion(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, registrationID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	registrationVersionID := uuid.Must(uuid.NewV7())
	registrationUser := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	registrationDocument := []byte(`{"blocks":[{"id":"question","type":"field","key":"city","input":"text","label":"Місто"}]}`)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{
		ID: registrationVersionID, FormID: registrationID, EventID: eventID, Version: 2, Enabled: true,
		Document: registrationDocument, CreatedAt: now,
	}, nil)
	q.EXPECT().ListEventFormAnswers(gomock.Any(), postgres.ListEventFormAnswersParams{EventID: eventID, FormID: registrationID}).Return([]postgres.ListEventFormAnswersRow{
		{EventID: eventID, UserID: registrationUser, FormVersionID: registrationVersionID, FormID: registrationID, Version: 2, Document: registrationDocument, Name: "Учасник", Answers: []byte(`{"city":"Київ"}`), SubmittedAt: now},
	}, nil)

	answers, err := uc.ListParticipantFormAnswers(context.Background(), eventID)
	if err != nil {
		t.Fatalf("list participant form answers: %v", err)
	}
	if len(answers) != 1 || answers[0].UserID != registrationUser || answers[0].FormVersion != 2 || answers[0].Name != "Учасник" {
		t.Fatalf("registration answers = %+v", answers)
	}
	if len(answers[0].Document.Blocks) != 1 || answers[0].Document.Blocks[0].Label != "Місто" {
		t.Fatalf("answer document must match submitted version: %+v", answers[0].Document)
	}
}
