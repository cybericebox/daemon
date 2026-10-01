package event

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
)

func TestRequiredDeliveryBlocksOnlyItsSelectedCapability(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().HasIncompleteRequiredEventFormDelivery(gomock.Any(), postgres.HasIncompleteRequiredEventFormDeliveryParams{
		EventID: eventID, UserID: userID, Capability: []byte(eventFormModel.CapabilityChallengeSubmit),
	}).Return(true, nil)

	err := requireEventCapability(context.Background(), eventFormRepo.New(q), eventID, userID, eventFormModel.CapabilityChallengeSubmit)
	if err == nil {
		t.Fatal("challenge.submit was not blocked")
	}
	q.EXPECT().HasIncompleteRequiredEventFormDelivery(gomock.Any(), postgres.HasIncompleteRequiredEventFormDeliveryParams{
		EventID: eventID, UserID: userID, Capability: []byte(eventFormModel.CapabilityTeamManage),
	}).Return(false, nil)
	if err = requireEventCapability(context.Background(), eventFormRepo.New(q), eventID, userID, eventFormModel.CapabilityTeamManage); err != nil {
		t.Fatalf("unselected team.manage capability unexpectedly blocked: %v", err)
	}
}

func TestDisabledAndOptionalFormsNeverGate(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().HasIncompleteRequiredEventFormDelivery(gomock.Any(), postgres.HasIncompleteRequiredEventFormDeliveryParams{
		EventID: eventID, UserID: userID, Capability: []byte(eventFormModel.CapabilityLabAccess),
	}).Return(false, nil)

	err := requireEventCapability(context.Background(), eventFormRepo.New(q), eventID, userID, eventFormModel.CapabilityLabAccess)
	if err != nil {
		t.Fatalf("optional or disabled form unexpectedly blocked: %v", err)
	}
}
