package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/rbac"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// liveBoardNames reads the view=live board of an individual event with one
// real name and one pseudonym, under the given participants visibility.
func liveBoardNames(t *testing.T, visibility eventConfigModel.Visibility, access event.ResultsAccess, liveScreen bool) []event.ScoreboardEntryView {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic),
		ParticipantsVisibility: int16(visibility), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 3, UpdatedAt: now}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return([]postgres.ListEventScoreboardRow{
		{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Olena Real", NameIsReal: true, Points: 300},
		{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Frost", Points: 200},
	}, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil)
	got, err := uc.GetResultsSnapshot(context.Background(), eventID, access, liveScreen)
	if err != nil {
		t.Fatalf("GetResultsSnapshot: %v", err)
	}
	return got.Scoreboard
}

// The live screen is shown to the hall: real names follow the public rule
// whether a screen link or a moderator's session opened it.
func TestLiveScreenMasksRealNamesUnlessParticipantsArePublic(t *testing.T) {
	screen, staff := event.LiveScreenResultsAccess, event.ResultsAccess{Role: rbac.RoleSuperAdmin}
	for _, tc := range []struct {
		name       string
		visibility eventConfigModel.Visibility
		access     event.ResultsAccess
		masked     bool
	}{
		{"link, hidden", eventConfigModel.VisibilityHidden, screen, true},
		{"link, private", eventConfigModel.VisibilityPrivate, screen, true},
		{"link, public", eventConfigModel.VisibilityPublic, screen, false},
		{"staff session, hidden", eventConfigModel.VisibilityHidden, staff, true},
		{"staff session, public", eventConfigModel.VisibilityPublic, staff, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := liveBoardNames(t, tc.visibility, tc.access, true)
			if rows[0].NameHidden != tc.masked || (rows[0].TeamName == "") != tc.masked {
				t.Fatalf("real name row = %+v, masked want %v", rows[0], tc.masked)
			}
			if rows[1].TeamName != "Frost" || rows[1].NameHidden {
				t.Fatalf("pseudonym row = %+v", rows[1])
			}
		})
	}
}

// The moderator's own manage view (not the live screen) keeps the real names.
func TestManagerPageViewKeepsRealNames(t *testing.T) {
	rows := liveBoardNames(t, eventConfigModel.VisibilityHidden, event.ResultsAccess{Role: rbac.RoleSuperAdmin}, false)
	if rows[0].TeamName != "Olena Real" || rows[0].NameHidden {
		t.Fatalf("manager page row = %+v", rows[0])
	}
}
