package event_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestOwnChallengeAttachmentRequiresPublishedSnapshotReference(t *testing.T) {
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	challengeID, teamChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	fileID, otherFileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name        string
		approved    bool
		finished    bool
		readiness   teamChallengeModel.Readiness
		requestedID uuid.UUID
		locked      bool
		wantAccess  bool
	}{
		{name: "approved published referenced file", approved: true, readiness: teamChallengeModel.ReadinessPublished, requestedID: fileID, wantAccess: true},
		{name: "finished event remains readable", approved: true, finished: true, readiness: teamChallengeModel.ReadinessPublished, requestedID: fileID, wantAccess: true},
		{name: "unreferenced file", approved: true, readiness: teamChallengeModel.ReadinessPublished, requestedID: otherFileID},
		{name: "unpublished challenge", approved: true, readiness: teamChallengeModel.ReadinessReady, requestedID: fileID},
		{name: "unapproved participant", requestedID: fileID},
		{name: "locked challenge hides its files", approved: true, readiness: teamChallengeModel.ReadinessPublished, requestedID: fileID, locked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newFormGateMock(gomock.NewController(t))
			status := participantModel.StatusPending
			if tc.approved {
				status = participantModel.StatusApproved
			}
			q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
				EventID: eventID, UserID: userID, Status: int16(status), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
			}, nil)
			if tc.approved {
				row := startedEvent(eventID, time.Now())
				if tc.finished {
					row.FinishAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
				}
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(row, nil)
				q.EXPECT().ListTeamBoardChallenges(gomock.Any(), gomock.Any()).Return([]postgres.ListTeamBoardChallengesRow{{
					ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID,
					Readiness: int16(tc.readiness), Snapshot: []byte(`{"attachments":[{"file_id":"` + fileID.String() + `","name":"brief.pdf"}]}`),
				}}, nil)
				var prerequisites []postgres.ListTeamChallengePrerequisitesRow
				if tc.locked {
					prerequisites = []postgres.ListTeamChallengePrerequisitesRow{{ChallengeID: challengeID, PrerequisiteChallengeID: uuid.Must(uuid.NewV7()), Name: "Intro"}}
				}
				q.EXPECT().ListTeamChallengePrerequisites(gomock.Any(), teamID).Return(prerequisites, nil)
			}
			if tc.wantAccess {
				q.EXPECT().CreateEventActivity(gomock.Any(), activityRow(func(p postgres.CreateEventActivityParams) bool {
					return p.Kind == "attachment_downloaded" && p.EventID == eventID && p.UserID.UUID == userID &&
						p.TeamID.UUID == teamID && p.SubjectID.UUID == challengeID && strings.Contains(string(p.Data), fileID.String())
				})).Return(nil)
			}
			media := &brandMediaFake{file: mediaModel.File{ID: fileID, Name: "brief.pdf", SizeBytes: 5}, blob: []byte("brief")}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
			reader, file, err := uc.StreamOwnChallengeAttachment(context.Background(), eventID, userID, challengeID, tc.requestedID)
			if !tc.wantAccess {
				require.Error(t, err)
				require.Nil(t, reader)
				return
			}
			require.NoError(t, err)
			defer reader.Close()
			body, readErr := io.ReadAll(reader)
			require.NoError(t, readErr)
			require.Equal(t, "brief", strings.TrimSpace(string(body)))
			require.Equal(t, fileID, file.ID)
		})
	}
}
