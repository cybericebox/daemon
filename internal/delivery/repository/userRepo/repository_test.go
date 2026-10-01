package userRepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

var now = time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*userRepo.Repository, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	return userRepo.New(q), q
}

func TestCreate_MapsDomainUserToRow(t *testing.T) {
	repo, q := setup(t)
	u := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "a@b.test", now)

	q.EXPECT().CreateUser(gomock.Any(), gomock.AssignableToTypeOf(postgres.CreateUserParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.CreateUserParams) (postgres.User, error) {
			assert.Equal(t, u.ID, arg.ID)
			assert.Equal(t, "a@b.test", arg.Email)
			assert.Equal(t, string(rbac.RoleUser), arg.Role)
			assert.Equal(t, string(userModel.UserStatusIncomplete), arg.Status)
			assert.False(t, arg.HashedPassword.Valid, "empty password must map to NULL")
			assert.Equal(t, now, arg.LastSeen)
			assert.Equal(t, now, arg.CreatedAt)
			assert.True(t, arg.UpdatedAt.Valid)
			return postgres.User{ID: arg.ID, Email: arg.Email, Role: arg.Role, Status: arg.Status,
				LastSeen: arg.LastSeen, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})

	got, err := repo.Create(context.Background(), u)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, userModel.UserStatusIncomplete, got.Status)
}

func TestUpdate_WritesWholeAggregate(t *testing.T) {
	repo, q := setup(t)
	u := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "a@b.test", now.Add(-time.Hour))
	require.NoError(t, u.CompleteSetup("Ivan", "Test", "hash", 2, func() (bool, error) { return false, nil }, now))

	expected := now.Add(-time.Hour) // snapshot taken BEFORE the mutation touched UpdatedAt
	q.EXPECT().UpdateUser(gomock.Any(), gomock.AssignableToTypeOf(postgres.UpdateUserParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			assert.True(t, arg.ExpectedUpdatedAt.Valid, "optimistic-lock snapshot must be sent")
			assert.Equal(t, expected, arg.ExpectedUpdatedAt.Time)
			assert.Equal(t, u.ID, arg.ID)
			assert.Equal(t, string(userModel.UserStatusActive), arg.Status)
			assert.True(t, arg.EmailConfirmed)
			assert.Equal(t, "hash", arg.HashedPassword.String)
			assert.True(t, arg.TosAcceptedAt.Valid)
			assert.Equal(t, int32(2), arg.TosVersion.Int32)
			assert.True(t, arg.UpdatedAt.Valid, "updated_at comes from the domain touch")
			assert.Equal(t, now, arg.UpdatedAt.Time)
			assert.False(t, arg.DeletedAt.Valid)
			return 1, nil
		})

	affected, err := repo.Update(context.Background(), u, expected)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)
}

func TestGetByID_MapsRowToDomain(t *testing.T) {
	repo, q := setup(t)
	id := uuid.Must(uuid.NewV7())
	tos := now.Add(-time.Hour)

	q.EXPECT().GetUserByID(gomock.Any(), id).Return(postgres.User{
		ID: id, Email: "x@y.test", Role: "admin", Status: "active",
		HashedPassword: pgtype.Text{String: "h", Valid: true},
		TosAcceptedAt:  pgtype.Timestamptz{Time: tos, Valid: true},
		TosVersion:     pgtype.Int4{Int32: 3, Valid: true},
		UpdatedAt:      pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)

	got, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, rbac.RoleAdmin, got.Role)
	assert.Equal(t, userModel.UserStatusActive, got.Status)
	assert.True(t, got.HasPassword())
	require.NotNil(t, got.TosAcceptedAt)
	assert.Equal(t, tos, *got.TosAcceptedAt)
	assert.Equal(t, int32(3), got.TosVersion)
	assert.Nil(t, got.DeletedAt)
}
