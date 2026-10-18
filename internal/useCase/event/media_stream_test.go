package event_test

import (
	"context"
	"io"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// streamMedia is the two halves of serving an event image in one call: the database check, then the bytes.
func streamMedia(u *event.EventUseCase, kind event.EventMediaKind) func(context.Context, uuid.UUID, uuid.UUID) (io.ReadCloser, string, error) {
	return func(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, string, error) {
		if _, err := u.CheckEventMedia(ctx, kind, eventID, fileID); err != nil {
			return nil, "", err
		}
		return u.OpenEventMedia(ctx, fileID)
	}
}

// The public flag decides how long the browser may keep the image: published = for good, staff-only = revalidate.
func TestCheckEventMediaTellsPublicFromStaffOnly(t *testing.T) {
	eventID, logoID, staff := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	build := func(t *testing.T, e postgres.Event) *event.EventUseCase {
		q := newFormGateMock(gomock.NewController(t))
		q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(e, nil).AnyTimes()
		media := &brandMediaFake{file: mediaModel.File{ID: logoID}, refs: []uuid.UUID{logoID}}
		return event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	}
	public, err := build(t, startedEvent(eventID, time.Now())).CheckEventMedia(context.Background(), event.EventMediaLogo, eventID, logoID)
	if err != nil || !public {
		t.Fatalf("published event: public=%v err=%v", public, err)
	}
	other := uuid.Must(uuid.NewV7())
	if _, err := build(t, startedEvent(eventID, time.Now())).CheckEventMedia(context.Background(), event.EventMediaLogo, eventID, other); err == nil {
		t.Fatal("a file that is not the event's logo must be refused")
	}
	adminCtx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: staff, Role: rbac.RoleAdmin})
	public, err = build(t, postgres.Event{ID: eventID, LifecycleConfigured: false}).CheckEventMedia(adminCtx, event.EventMediaLogo, eventID, logoID)
	if err != nil || public {
		t.Fatalf("unpublished event for platform staff: public=%v err=%v", public, err)
	}
}
