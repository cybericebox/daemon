package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The admin user table filters by confirmed email, sign-in method and inactivity, counts the same rows, sorts by
// email and sign-in, and lists the providers of the page in one query.
func TestUsersPage_FiltersBySignInConfirmationAndInactivity(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := userRepo.New(db.Queries)
	now := time.Now()
	seed := func(name string, password, confirmed bool, google bool, lastSeen time.Time) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		params := postgres.CreateUserParams{
			ID:        id,
			Email:     name + "@filters.test",
			FirstName: name,
			Role:      string(rbac.RoleUser),
			Status: string(
				userModel.UserStatusActive,
			),
			EmailConfirmed: confirmed,
			CreatedAt:      lastSeen,
		}
		if password {
			params.HashedPassword = pgtype.Text{String: "hash", Valid: true}
		}
		_, err := db.Queries.CreateUser(ctx, params)
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `UPDATE users SET last_seen = $2 WHERE id = $1`, id, lastSeen)
		require.NoError(t, err)
		if google {
			_, err = db.Pool.Exec(
				ctx,
				`INSERT INTO user_providers (id, user_id, provider, provider_user_id, created_at) VALUES ($1, $2, 'google', $3, now())`,
				uuid.Must(uuid.NewV7()),
				id,
				"g-"+name,
			)
			require.NoError(t, err)
		}
		return id
	}
	pw := seed("pw", true, true, false, now.Add(-1*time.Hour))
	both := seed("both", true, false, true, now.Add(-40*24*time.Hour))
	gg := seed("gg", false, true, true, now.Add(-90*24*time.Hour))

	page := func(p userRepo.PageParams) []uuid.UUID {
		p.Search, p.Roles, p.Limit, p.SortBy, p.SortDir = "@filters.test", []string{}, 50, "email", "asc"
		rows, err := repo.ListPage(ctx, p)
		require.NoError(t, err)
		total, err := repo.CountFiltered(ctx, p)
		require.NoError(t, err)
		require.EqualValues(t, len(rows), total, "the count follows the same filters")
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids
	}
	require.ElementsMatch(t, []uuid.UUID{pw, both, gg}, page(userRepo.PageParams{}))
	require.ElementsMatch(t, []uuid.UUID{pw, both}, page(userRepo.PageParams{SignIn: "password"}))
	require.ElementsMatch(t, []uuid.UUID{both, gg}, page(userRepo.PageParams{SignIn: "google"}))
	require.Empty(t, page(userRepo.PageParams{SignIn: "passkey"}), "no passkey is stored yet")
	require.ElementsMatch(t, []uuid.UUID{pw, gg}, page(userRepo.PageParams{EmailConfirmed: "true"}))
	require.ElementsMatch(t, []uuid.UUID{both}, page(userRepo.PageParams{EmailConfirmed: "false"}))
	require.ElementsMatch(t, []uuid.UUID{both, gg}, page(userRepo.PageParams{InactiveDays: 30}))
	require.ElementsMatch(
		t,
		[]uuid.UUID{gg},
		page(userRepo.PageParams{InactiveDays: 60, SignIn: "google"}),
	)
	require.Equal(t, []uuid.UUID{both, gg, pw}, page(userRepo.PageParams{}), "sorted by email")

	byMethod, err := repo.ListPage(
		ctx,
		userRepo.PageParams{
			Search:  "@filters.test",
			Roles:   []string{},
			Limit:   50,
			SortBy:  "signIn",
			SortDir: "asc",
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]uuid.UUID{both, pw, gg},
		[]uuid.UUID{byMethod[0].ID, byMethod[1].ID, byMethod[2].ID},
		"password+google, password, google",
	)

	providers, err := repo.ProvidersOf(ctx, []uuid.UUID{pw, both, gg})
	require.NoError(t, err)
	require.Empty(t, providers[pw])
	require.Equal(t, []string{"google"}, providers[both])
}

func TestUserParticipationsListEventsAndTeams(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "userparts")

	items, err := userRepo.New(db.Queries).Participations(ctx, f.user)

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, f.event, items[0].EventID)
	require.Equal(t, "captain", items[0].Role)
	require.Equal(t, "Blue", items[0].TeamName)
	require.NotNil(t, items[0].TeamID)
}

// The facet of a field counts the users per value under the other filters.
func TestUsersFacets(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := userRepo.New(db.Queries)
	seed := func(name, role string, confirmed, password, google bool) {
		id := uuid.Must(uuid.NewV7())
		params := postgres.CreateUserParams{ID: id, Email: name + "@facets.test", FirstName: name, Role: role,
			Status: string(userModel.UserStatusActive), EmailConfirmed: confirmed, CreatedAt: time.Now()}
		if password {
			params.HashedPassword = pgtype.Text{String: "hash", Valid: true}
		}
		_, err := db.Queries.CreateUser(ctx, params)
		require.NoError(t, err)
		if google {
			_, err = db.Pool.Exec(ctx, `INSERT INTO user_providers (id, user_id, provider, provider_user_id, created_at) VALUES ($1, $2, 'google', $3, now())`,
				uuid.Must(uuid.NewV7()), id, "g-"+name)
			require.NoError(t, err)
		}
	}
	seed("a", string(rbac.RoleUser), true, true, false)
	seed("b", string(rbac.RoleUser), false, true, true)
	seed("c", string(rbac.RoleAdmin), true, false, true)
	base := userRepo.PageParams{Search: "@facets.test", Roles: []string{}}

	roles, err := repo.Facet(ctx, "role", base)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"user": 2, "admin": 1}, roles)

	signIn, err := repo.Facet(ctx, "signIn", base)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"password": 2, "google": 2}, signIn, "a user counts once per method")

	confirmedOnly := base
	confirmedOnly.EmailConfirmed = "true"
	roles, err = repo.Facet(ctx, "role", confirmedOnly)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"user": 1, "admin": 1}, roles, "the other filters apply")

	confirmed, err := repo.Facet(ctx, "emailConfirmed", base)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"true": 2, "false": 1}, confirmed)

	none, err := repo.Facet(ctx, "bogus", base)
	require.NoError(t, err)
	require.Empty(t, none)
}
