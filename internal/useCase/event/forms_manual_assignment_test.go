package event

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
)

func TestAssignManualEventFormImmediatelyCreatesRecipientDeliveries(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	formID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	audience, err := json.Marshal(eventFormModel.Audience{Kind: eventFormModel.AudienceAllParticipants})
	if err != nil {
		t.Fatalf("marshal audience: %v", err)
	}

	q.EXPECT().GetEventForm(gomock.Any(), postgres.GetEventFormParams{EventID: eventID, FormID: formID}).Return(postgres.GetEventFormRow{
		ID: formID, EventID: eventID, Title: "Feedback", Enabled: true, CurrentVersionID: versionID,
		CurrentVersion: 1, CurrentDocument: []byte(`{"blocks":[]}`), CreatedAt: now, UpdatedAt: now, VersionCreatedAt: now,
	}, nil)
	q.EXPECT().CreateEventFormAssignment(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventFormAssignmentParams) (postgres.EventFormAssignment, error) {
		if arg.EventID != eventID || arg.FormID != formID || arg.Trigger != string(eventFormModel.TriggerManual) {
			t.Fatalf("unexpected manual assignment: %+v", arg)
		}
		return postgres.EventFormAssignment{ID: arg.ID, EventID: eventID, FormID: formID, Trigger: arg.Trigger, Audience: audience, Presentation: string(eventFormModel.PresentationTask), Gates: []byte(`[]`), Enabled: true, CreatedAt: now, UpdatedAt: now}, nil
	})
	q.EXPECT().GetLatestEventFormVersionByFormID(gomock.Any(), formID).Return(postgres.EventFormVersion{ID: versionID, FormID: formID, EventID: eventID, Version: 1, Enabled: true, Document: []byte(`{"blocks":[]}`), CreatedAt: now}, nil)
	q.EXPECT().ListEventFormRecipientCandidates(gomock.Any(), eventID).Return([]postgres.ListEventFormRecipientCandidatesRow{{UserID: userID}}, nil)
	q.EXPECT().CreateEventFormDelivery(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventFormDeliveryParams) (int64, error) {
		if arg.FormVersionID != versionID || arg.UserID != userID || arg.Presentation != string(eventFormModel.PresentationTask) {
			t.Fatalf("unexpected manual delivery: %+v", arg)
		}
		return 1, nil
	})
	q.EXPECT().MarkEventFormAssignmentMaterialized(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.MarkEventFormAssignmentMaterializedParams) (int64, error) {
		if arg.ID == uuid.Nil || !arg.MaterializedAt.Valid {
			t.Fatalf("manual assignment must record its materialized audience snapshot: %+v", arg)
		}
		return 1, nil
	})

	err = uc.AssignEventForm(context.Background(), eventID, formID, CreateEventFormAssignmentInput{
		Rule:          eventFormModel.Assignment{Trigger: eventFormModel.TriggerManual, Audience: eventFormModel.Audience{Kind: eventFormModel.AudienceAllParticipants}, Presentation: eventFormModel.PresentationTask},
		Enabled:       true,
		IncludeFuture: true,
	})
	if err != nil {
		t.Fatalf("AssignEventForm: %v", err)
	}
}
