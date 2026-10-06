package event

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/eventContent/testutil"
)

func TestGetOwnEventFormReturnsDeliveredVersionInsteadOfCurrentFormVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	formID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	deliveredVersionID := uuid.Must(uuid.NewV7())
	currentVersionID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventFormDelivery(gomock.Any(), postgres.GetEventFormDeliveryParams{EventID: eventID, FormID: formID, UserID: userID}).Return(postgres.EventFormDelivery{FormVersionID: deliveredVersionID, UserID: userID, AssignmentID: uuid.Must(uuid.NewV7()), Presentation: "task", Gates: []byte(`[]`), CreatedAt: now}, nil)
	q.EXPECT().GetEventForm(gomock.Any(), postgres.GetEventFormParams{EventID: eventID, FormID: formID}).Return(postgres.GetEventFormRow{
		ID: formID, EventID: eventID, Title: "Check-in", Enabled: true, CurrentVersionID: currentVersionID, CurrentVersion: 2,
		CurrentDocument: testutil.Document("new", "new"), CreatedAt: now, UpdatedAt: now, VersionCreatedAt: now,
	}, nil)
	q.EXPECT().GetEventFormVersionByID(gomock.Any(), postgres.GetEventFormVersionByIDParams{EventID: eventID, ID: deliveredVersionID}).Return(postgres.EventFormVersion{
		ID: deliveredVersionID, FormID: formID, EventID: eventID, Version: 1, Enabled: true,
		Document: testutil.Document("old", "old"), CreatedAt: now,
	}, nil)

	form, err := uc.GetOwnEventForm(context.Background(), eventID, formID, userID)
	if err != nil {
		t.Fatalf("GetOwnEventForm: %v", err)
	}
	if form.CurrentVersionID != deliveredVersionID || form.Version != 1 || len(form.Document.Blocks) != 1 || form.Document.Blocks[0].ID != "old" {
		t.Fatalf("participant received current instead of delivered form: %+v", form)
	}
}

func TestSubmitEventFormResponseRejectsVersionOutsideDeliveredVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	formID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	deliveredVersionID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventFormDelivery(gomock.Any(), postgres.GetEventFormDeliveryParams{EventID: eventID, FormID: formID, UserID: userID}).Return(postgres.EventFormDelivery{FormVersionID: deliveredVersionID, UserID: userID, Gates: []byte(`[]`)}, nil)

	err := uc.SubmitEventFormResponse(context.Background(), eventID, formID, userID, SubmitEventFormResponseInput{FormVersionID: uuid.Must(uuid.NewV7()), Answers: map[string]any{}})
	if err == nil {
		t.Fatal("response with a version other than the delivery version was accepted")
	}
}
