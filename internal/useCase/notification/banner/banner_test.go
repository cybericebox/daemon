package bannerUseCase

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

func validInput() siteBannerModel.Input {
	return siteBannerModel.Input{Text: "Maintenance", Level: siteBannerModel.LevelInfo, Audience: siteBannerModel.AudienceEveryone, IsActive: true}
}

func TestUpdateLegacyClientLeavesTargetingUnchanged(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	u := NewSiteBannerUseCase(Dependencies{Repo: q})
	id := uuid.Must(uuid.NewV7())
	target := uuid.Must(uuid.NewV7())
	q.EXPECT().GetSiteBanner(gomock.Any(), id).Return(postgres.SiteBanner{ID: id, TargetMode: "selected", TargetSites: []string{"admin"}, TargetEventIds: []uuid.UUID{target}}, nil)
	q.EXPECT().UpdateSiteBanner(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateSiteBannerParams) (postgres.SiteBanner, error) {
		if p.UpdateTargeting {
			t.Fatal("omitted targeting must not overwrite a newer selection")
		}
		if p.Text != "Maintenance" {
			t.Fatal("content was not updated")
		}
		return postgres.SiteBanner{ID: id, Text: p.Text, TargetMode: "selected", TargetSites: []string{"admin"}, TargetEventIds: []uuid.UUID{target}}, nil
	})
	b, err := u.UpdateSiteBanner(context.Background(), nil, id, validInput())
	if err != nil {
		t.Fatal(err)
	}
	if b.TargetMode != siteBannerModel.TargetSelected || len(b.TargetEventIDs) != 1 || b.TargetEventIDs[0] != target {
		t.Fatalf("targeting lost: %+v", b)
	}
}

func TestCreateAndUpdateNormalizeTargeting(t *testing.T) {
	for _, update := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "update"}[update], func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			u := NewSiteBannerUseCase(Dependencies{Repo: q})
			u.now = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }
			in := validInput()
			mode := siteBannerModel.TargetSelected
			in.TargetMode = &mode
			in.TargetSites = []string{"main", "main", "id"}
			id := uuid.Must(uuid.NewV7())
			event := uuid.Must(uuid.NewV7())
			in.TargetEventIDs = []uuid.UUID{event, event}
			check := func(sites []string, events []uuid.UUID) {
				t.Helper()
				if len(sites) != 2 || sites[0] != "main" || sites[1] != "id" || len(events) != 1 || events[0] != event {
					t.Fatalf("not normalized sites=%v events=%v", sites, events)
				}
			}
			if update {
				q.EXPECT().GetSiteBanner(gomock.Any(), id).Return(postgres.SiteBanner{ID: id}, nil)
				q.EXPECT().UpdateSiteBanner(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateSiteBannerParams) (postgres.SiteBanner, error) {
					check(p.TargetSites, p.TargetEventIds)
					if !p.UpdateTargeting || p.TargetMode != "selected" {
						t.Fatal("selection not written")
					}
					return postgres.SiteBanner{}, nil
				})
				if _, err := u.UpdateSiteBanner(context.Background(), nil, id, in); err != nil {
					t.Fatal(err)
				}
			} else {
				q.EXPECT().CreateSiteBanner(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateSiteBannerParams) (postgres.SiteBanner, error) {
					check(p.TargetSites, p.TargetEventIds)
					if p.TargetMode != "selected" {
						t.Fatal("selection not written")
					}
					return postgres.SiteBanner{}, nil
				})
				if _, err := u.CreateSiteBanner(context.Background(), nil, id, in); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestScopedBannerCannotBeBroadenedOrEditedThroughAnotherScope(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	u := NewSiteBannerUseCase(Dependencies{Repo: q})
	event, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	in := validInput()
	mode := siteBannerModel.TargetSelected
	in.TargetMode = &mode
	in.TargetSites = []string{"main"}
	if _, err := u.CreateSiteBanner(context.Background(), &event, id, in); err == nil {
		t.Fatal("event scope was broadened")
	}
	if _, err := u.UpdateSiteBanner(context.Background(), &event, id, in); err == nil {
		t.Fatal("event scope was broadened")
	}
	q.EXPECT().GetSiteBanner(gomock.Any(), id).Return(postgres.SiteBanner{ID: id, ScopeEventID: uuid.NullUUID{UUID: event, Valid: true}}, nil)
	if _, err := u.UpdateSiteBanner(context.Background(), nil, id, validInput()); err == nil {
		t.Fatal("platform path edited an event banner")
	}
	q.EXPECT().GetSiteBanner(gomock.Any(), id).Return(postgres.SiteBanner{}, pgx.ErrNoRows)
	if _, err := u.UpdateSiteBanner(context.Background(), nil, id, validInput()); err == nil {
		t.Fatal("missing banner accepted")
	}
}
