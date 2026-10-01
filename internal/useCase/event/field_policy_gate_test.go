package event

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// The block is the existing form_required rejection, raised only when the
// organizer chose both «required from everyone» and «block submissions».
func TestRequireFieldsFilledBlocksOnlyUnderTheBlockingPolicy(t *testing.T) {
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name                         string
		missing                      int32
		requireExisting, blockPolicy bool
		wantBlocked                  bool
		expectFormRead               bool
	}{
		{name: "gaps under the blocking policy", missing: 1, requireExisting: true, blockPolicy: true, wantBlocked: true, expectFormRead: true},
		{name: "gaps but blocking is off", missing: 2, requireExisting: true, blockPolicy: false, expectFormRead: true},
		{name: "gaps but only new registrations are asked", missing: 1, requireExisting: false, blockPolicy: true, expectFormRead: true},
		{name: "nothing missing never reads the form", missing: 0, requireExisting: true, blockPolicy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			q.EXPECT().GetEventParticipantFieldsMissing(gomock.Any(), postgres.GetEventParticipantFieldsMissingParams{EventID: eventID, UserID: userID}).Return(tc.missing, nil)
			if tc.expectFormRead {
				q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{
					EventID: eventID, Version: 2, Enabled: true, Required: true, RequireExisting: tc.requireExisting, BlockSubmissions: tc.blockPolicy, Document: []byte(`{"blocks":[]}`),
				}, nil)
			}
			if !tc.wantBlocked {
				q.EXPECT().GetEventTeamFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), nil)
			}
			err := requireFieldsFilled(context.Background(), eventFormRepo.New(q), eventTeamRepo.New(q), eventID, userID, teamID)
			if tc.wantBlocked != errors.Is(err, participantModel.ErrEventFormRequired.Err()) {
				t.Fatalf("blocked = %v, err = %v", tc.wantBlocked, err)
			}
			if !tc.wantBlocked && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequireFieldsFilledBlocksOnTeamFieldsToo(t *testing.T) {
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetEventParticipantFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), nil)
	q.EXPECT().GetEventTeamFieldsMissing(gomock.Any(), postgres.GetEventTeamFieldsMissingParams{EventID: eventID, TeamID: teamID}).Return(int32(2), nil)
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{
		EventID: eventID, Enabled: true, Required: true, RequireExisting: true, BlockSubmissions: true, Document: []byte(`{"blocks":[]}`),
	}, nil)
	err := requireFieldsFilled(context.Background(), eventFormRepo.New(q), eventTeamRepo.New(q), eventID, userID, teamID)
	if !errors.Is(err, participantModel.ErrEventFormRequired.Err()) {
		t.Fatalf("a team with unfilled required fields must block submissions: %v", err)
	}
}

func TestRequireFieldsFilledIgnoresEventsWithoutFields(t *testing.T) {
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetEventParticipantFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), pgx.ErrNoRows)
	q.EXPECT().GetEventTeamFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), pgx.ErrNoRows)
	if err := requireFieldsFilled(context.Background(), eventFormRepo.New(q), eventTeamRepo.New(q), eventID, userID, teamID); err != nil {
		t.Fatalf("no rows must not block: %v", err)
	}
}
