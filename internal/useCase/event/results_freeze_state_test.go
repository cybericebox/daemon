package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
	event "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"
)

func TestResultsFreezeReadUsesCommonGatesWithoutScoreQueries(t *testing.T) {
	for _, tc := range []struct {
		name, audience, phase, endMode string
		visibility                     eventConfigModel.Visibility
		wantApplied                    bool
		wantErr                        error
	}{
		{"public guest", "guest", "active", "at_finish", eventConfigModel.VisibilityPublic, true, nil},
		{"planned notice", "guest", "future", "at_finish", eventConfigModel.VisibilityPublic, false, nil},
		{"manual after finish", "guest", "finished", "manual", eventConfigModel.VisibilityPublic, true, nil},
		{"automatic after finish", "guest", "finished", "at_finish", eventConfigModel.VisibilityPublic, false, nil},
		{"hidden results", "guest", "active", "at_finish", eventConfigModel.VisibilityHidden, false, eventConfigModel.ErrResultsHidden.Err()},
		{"private guest", "guest", "active", "at_finish", eventConfigModel.VisibilityPrivate, false, eventConfigModel.ErrResultsParticipantsOnly.Err()},
		{"private participant", "participant", "active", "at_finish", eventConfigModel.VisibilityPrivate, true, nil},
		{"private applicant", "applicant", "active", "at_finish", eventConfigModel.VisibilityPrivate, false, eventConfigModel.ErrResultsParticipantsOnly.Err()},
		{"unpublished guest", "guest", "unpublished", "at_finish", eventConfigModel.VisibilityPublic, false, eventModel.ErrEventNotFound.Err()},
		{"withdrawn guest", "guest", "withdrawn", "manual", eventConfigModel.VisibilityPublic, false, eventModel.ErrEventNotFound.Err()},
		{"withdrawn participant", "participant", "withdrawn", "manual", eventConfigModel.VisibilityPublic, false, eventModel.ErrEventNotFound.Err()},
		{"withdrawn staff", "staff", "withdrawn", "manual", eventConfigModel.VisibilityHidden, false, nil},
		{"staff live view", "staff", "active", "at_finish", eventConfigModel.VisibilityPublic, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := event.NewEventUseCase(event.Dependencies{Repo: q})
			eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now().UTC()
			cfg := frozenConfig(eventID, now, 10)
			cfg.ScoreboardVisibility = int16(tc.visibility)
			cfg.ResultsFreezeEndMode = tc.endMode
			cfg.ResultsFreezeBannerText, cfg.ResultsFreezeBannerLevel = "Організатор відкриє підсумки.", "critical"
			row := startedEvent(eventID, now)
			switch tc.phase {
			case "future":
				row.FinishAt = pgtype.Timestamptz{Time: now.Add(3 * time.Hour), Valid: true}
			case "finished":
				row.FinishAt = pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}
			case "unpublished":
				row.LifecycleConfigured = false
			case "withdrawn":
				row = startedEvent(eventID, now.Add(-3*time.Hour))
			}
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(cfg, nil)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(row, nil)
			q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 7, UpdatedAt: now}, nil)
			access := event.ResultsAccess{Role: rbac.RolePublic}
			if tc.audience == "staff" {
				access.Role = rbac.RoleAdmin
			}
			if tc.audience == "participant" || tc.audience == "applicant" {
				access.UserID, access.Role = &userID, rbac.RoleUser
				q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows).AnyTimes()
				status := participantModel.StatusApproved
				if tc.audience == "applicant" {
					status = participantModel.StatusPending
				}
				q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(status), CreatedAt: now}, nil).AnyTimes()
			}
			// Two calls share event/config state. No score projection query has an expectation.
			for repeat := 0; repeat < 2; repeat++ {
				view, err := uc.ResultsFreezeBanner(context.Background(), eventID, access)
				if tc.wantErr != nil {
					// A viewer who may not read the results has no freeze banner, and no error.
					if err != nil || view.Applied || view.BannerText != "" {
						t.Fatalf("gate %v must give an empty view, got %+v err=%v", tc.wantErr, view, err)
					}
					continue
				}
				if err != nil || view.Applied != tc.wantApplied {
					t.Fatalf("freeze=%+v err=%v", view, err)
				}
				wantText, wantLevel := "", "warning"
				if tc.wantApplied {
					wantText, wantLevel = cfg.ResultsFreezeBannerText, cfg.ResultsFreezeBannerLevel
				}
				if view.BannerText != wantText || string(view.BannerLevel) != wantLevel {
					t.Fatalf("banner privacy: %+v", view)
				}
			}
		})
	}
}
