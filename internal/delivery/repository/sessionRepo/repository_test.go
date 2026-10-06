package sessionRepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

var now = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*sessionRepo.Repository, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	return sessionRepo.New(q), q
}

// Create passes the WHOLE domain entity through: ids and timestamps from the
// factory, metadata serialized here (JSON never leaks into the use case).
func TestCreate_MapsDomainSessionToRow(t *testing.T) {
	repo, q := setup(t)
	s := authModel.NewSession(
		uuid.Must(uuid.NewV7()),
		authModel.SessionMetadata{UserAgent: "ua", IP: "9.9.9.9"},
		time.Hour,
		now,
	)

	q.EXPECT().CreateSession(gomock.Any(), gomock.AssignableToTypeOf(postgres.CreateSessionParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.CreateSessionParams) (postgres.Session, error) {
			assert.Equal(t, s.ID, arg.ID)
			assert.Equal(t, s.UserID, arg.UserID)
			assert.Equal(t, s.ExpiresAt, arg.ExpiresAt)
			assert.Equal(t, s.LastSeen, arg.LastSeen)
			assert.Equal(t, s.CreatedAt, arg.CreatedAt)
			assert.JSONEq(t, `{"user_agent":"ua","ip":"9.9.9.9"}`, string(arg.Metadata))
			return postgres.Session{
				ID: arg.ID, UserID: arg.UserID, ExpiresAt: arg.ExpiresAt,
				LastSeen: arg.LastSeen, CreatedAt: arg.CreatedAt, Metadata: arg.Metadata,
			}, nil
		})

	got, err := repo.Create(context.Background(), s)
	require.NoError(t, err)
	assert.Equal(t, s, got)
}

func TestGetByID_UnmarshalsMetadata(t *testing.T) {
	repo, q := setup(t)
	sid := uuid.Must(uuid.NewV7())
	uid := uuid.Must(uuid.NewV7())

	q.EXPECT().GetSessionByID(gomock.Any(), sid).Return(postgres.Session{
		ID: sid, UserID: uid, ExpiresAt: now.Add(time.Hour), LastSeen: now, CreatedAt: now,
		Metadata: []byte(`{"user_agent":"ua","ip":"1.1.1.1"}`),
	}, nil)

	got, err := repo.GetByID(context.Background(), sid)
	require.NoError(t, err)
	assert.Equal(t, authModel.SessionMetadata{UserAgent: "ua", IP: "1.1.1.1"}, got.Metadata)
	assert.True(t, got.BelongsTo(uid))
}

// Corrupt metadata must not fail the read: validations evolve, historical
// rows must always load (go-ddd: don't validate on the read side).
func TestGetByID_CorruptMetadataIsForgiven(t *testing.T) {
	repo, q := setup(t)
	sid := uuid.Must(uuid.NewV7())

	q.EXPECT().GetSessionByID(gomock.Any(), sid).Return(postgres.Session{
		ID: sid, Metadata: []byte(`{broken`),
	}, nil)

	got, err := repo.GetByID(context.Background(), sid)
	require.NoError(t, err)
	assert.Equal(t, authModel.SessionMetadata{}, got.Metadata)
}

func TestListByUser_MapsRows(t *testing.T) {
	repo, q := setup(t)
	uid := uuid.Must(uuid.NewV7())

	q.EXPECT().GetSessionsByUser(gomock.Any(), uid).Return([]postgres.Session{
		{ID: uuid.Must(uuid.NewV7()), UserID: uid, Metadata: []byte(`{"user_agent":"a","ip":"1.1.1.1"}`), CreatedAt: now},
		{ID: uuid.Must(uuid.NewV7()), UserID: uid, Metadata: []byte(`{"user_agent":"b","ip":"2.2.2.2"}`), CreatedAt: now},
	}, nil)

	got, err := repo.ListByUser(context.Background(), uid)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Metadata.UserAgent)
	assert.Equal(t, "2.2.2.2", got[1].Metadata.IP)
}
