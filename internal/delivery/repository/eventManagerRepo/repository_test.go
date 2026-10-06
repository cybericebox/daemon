package eventManagerRepo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventManagerRepo "github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
)

type fakeQueries struct {
	row     postgres.EventManager
	err     error
	created postgres.EventManager
}

func (f fakeQueries) ListEventManagers(_ context.Context, _ uuid.UUID) ([]postgres.EventManager, error) {
	return nil, f.err
}

func (f fakeQueries) UpsertEventManager(_ context.Context, _ postgres.UpsertEventManagerParams) (postgres.EventManager, error) {
	return f.created, f.err
}

func (f fakeQueries) DeleteNonOwnerEventManager(_ context.Context, _ postgres.DeleteNonOwnerEventManagerParams) (int64, error) {
	return 1, f.err
}

func (f fakeQueries) GetEventManager(_ context.Context, _ postgres.GetEventManagerParams) (postgres.EventManager, error) {
	return f.row, f.err
}

func (f fakeQueries) CreateEventManager(_ context.Context, _ postgres.CreateEventManagerParams) (postgres.EventManager, error) {
	return f.created, f.err
}

func TestGetMapsMembershipAndKeepsItScopedToEventAndUser(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	createdAt := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	repo := eventManagerRepo.New(fakeQueries{row: postgres.EventManager{
		EventID: eventID, UserID: userID, Role: int16(eventManagerModel.RoleManager), CreatedAt: createdAt,
	}})

	m, err := repo.Get(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.EventID != eventID || m.UserID != userID || m.Role != eventManagerModel.RoleManager || !m.CreatedAt.Equal(createdAt) {
		t.Fatalf("membership: %+v", m)
	}
}

func TestGetPreservesNotFoundForAccessUseCaseToHide(t *testing.T) {
	repo := eventManagerRepo.New(fakeQueries{err: errors.New("not found")})
	if _, err := repo.Get(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("Get must return the repository error")
	}
}

func TestCreatePersistsTheOwnerMembership(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	createdAt := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	membership, _ := eventManagerModel.New(eventID, userID, eventManagerModel.RoleOwner, createdAt)
	repo := eventManagerRepo.New(fakeQueries{created: postgres.EventManager{
		EventID: eventID, UserID: userID, Role: int16(eventManagerModel.RoleOwner), CreatedAt: createdAt,
	}})

	got, err := repo.Create(context.Background(), membership)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got != membership {
		t.Fatalf("created membership: got %+v, want %+v", got, membership)
	}
}
