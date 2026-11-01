package auth_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/passkeyRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/tablePrefsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	passkeyModel "github.com/cybericebox/daemon/internal/model/passkey"
	"github.com/cybericebox/daemon/internal/model/rbac"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

// The address of a deleted account is free at once: the same person (or anyone) signs up with it again and gets a
// clean account, with no tie to the old one: not its pseudonymised row, links, keys, views, codes or invitations.
func TestDeletedAccountAddressIsFreeAndTheNewAccountIsClean(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	notifier := &fakeNotifier{}
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo: db.Queries,
		Token: token.MustNew(
			token.Config{TokenSignature: "test-signing-key-that-is-long-enough"},
		),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: notifier,
		Sessions: testSessions(t),
		Config: config.AuthConfig{
			SessionIdleTTL:      time.Hour,
			TemporalCodeTTL:     time.Hour,
			SignupSetupTokenTTL: time.Hour,
			Hosts:               testHosts("example.test"),
		},
	})

	const address = "same@example.test"
	oldID := uuid.Must(uuid.NewV7())
	_, err := userRepo.New(db.Queries).
		Create(ctx, userModel.NewGoogleUser(oldID, address, "Old", "Owner", time.Now()))
	require.NoError(t, err)
	_, err = db.Queries.CreateUserProvider(ctx, postgres.CreateUserProviderParams{
		ID: uuid.Must(uuid.NewV7()), UserID: oldID, Provider: "google", ProviderUserID: "g-old",
	})
	require.NoError(t, err)
	_, ok, err := passkeyRepo.New(db.Queries).Create(ctx, passkeyModel.Passkey{
		ID: uuid.Must(
			uuid.NewV7(),
		),
		UserID:       oldID,
		CredentialID: []byte("old-credential"),
		PublicKey:    []byte("pk"),
		Name:         "Old key",
		UserVerified: true,
		CreatedAt:    time.Now(),
	}, passkeyModel.MaxPerUser)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = tablePrefsRepo.New(db.Queries).
		Save(ctx, oldID, "admin.users", []byte(`{}`), time.Now(), 10)
	require.NoError(t, err)
	codes := temporalCodeRepo.New(db.Queries)
	for _, codeType := range []int32{temporalCodeModel.PasswordResettingCodeType, temporalCodeModel.EmailChangeCodeType, temporalCodeModel.SetupLinkCodeType} {
		data, _ := json.Marshal(map[string]uuid.UUID{"UserID": oldID})
		require.NoError(
			t,
			codes.Create(
				ctx,
				temporalCodeModel.NewCode(
					uuid.Must(uuid.NewV7()),
					"old-code-"+string(rune('a'+codeType)),
					codeType,
					data,
					time.Now().Add(time.Hour),
				),
			),
		)
	}

	owner := rbac.ContextWithCurrentUserSession(ctx, rbac.Claims{
		UserID:     oldID,
		SessionID:  uuid.Must(uuid.NewV7()),
		Role:       rbac.RoleUser,
		SignedInAt: time.Now(),
	})
	require.NoError(t, uc.DeleteAccount(owner, oldID, ""))

	// Nothing of the old account is left to hang on to.
	count := func(sql string) int {
		var n int
		require.NoError(t, db.Pool.QueryRow(ctx, sql, oldID).Scan(&n))
		return n
	}
	require.Zero(t, count(`SELECT count(*) FROM user_providers WHERE user_id = $1`))
	require.Zero(t, count(`SELECT count(*) FROM user_passkeys WHERE user_id = $1`))
	require.Zero(t, count(`SELECT count(*) FROM user_table_prefs WHERE user_id = $1`))
	require.Zero(t, count(`SELECT count(*) FROM temporal_codes WHERE data ->> 'UserID' = $1::text`))
	require.Zero(t, count(`SELECT count(*) FROM sessions WHERE user_id = $1`))
	var oldEmail string
	require.NoError(
		t,
		db.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, oldID).Scan(&oldEmail),
	)
	require.NotEqual(t, address, oldEmail, "the old row no longer holds the address")

	// The same address signs up again at once.
	require.NoError(t, uc.BeginEmailRegistration(ctx, address, ""))
	fresh, err := db.Queries.GetUserByEmail(ctx, address)
	require.NoError(t, err)
	require.NotEqual(t, oldID, fresh.ID, "a new account, not the old row")
	require.Equal(t, string(userModel.UserStatusIncomplete), fresh.Status)
	require.Empty(t, fresh.FirstName)
	require.Equal(t, 1, notifier.calls, "the setup mail goes to the address")

	newCount := func(sql string) int {
		var n int
		require.NoError(t, db.Pool.QueryRow(ctx, sql, fresh.ID).Scan(&n))
		return n
	}
	for _, sql := range []string{
		`SELECT count(*) FROM user_providers WHERE user_id = $1`,
		`SELECT count(*) FROM user_passkeys WHERE user_id = $1`,
		`SELECT count(*) FROM user_table_prefs WHERE user_id = $1`,
		`SELECT count(*) FROM event_participants WHERE user_id = $1`,
		`SELECT count(*) FROM challenge_attempts WHERE user_id = $1`,
		`SELECT count(*) FROM sessions WHERE user_id = $1`,
	} {
		require.Zero(t, newCount(sql), sql)
	}
}
