package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/errorJournal"
	passkeyModel "github.com/cybericebox/daemon/internal/model/passkey"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	liberr "github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

const (
	passkeyRPID   = "example.test"
	passkeyOrigin = "https://id.example.test"
	passkeyPass   = "Secret!1"
)

// passkeyEnv is the use case on a small in-memory store of the tables a passkey ceremony touches.
type passkeyEnv struct {
	t       *testing.T
	uc      *auth.AuthUseCase
	user    postgres.User
	gone    bool
	codes   map[string]postgres.TemporalCode
	keys    map[uuid.UUID]postgres.UserPasskey
	session uuid.UUID
	signIns int
}

type journalSpy struct{ events []errorJournal.Event }

func (j *journalSpy) Report(e errorJournal.Event) { j.events = append(j.events, e) }

func newPasskeyEnv(t *testing.T, withPassword bool) *passkeyEnv {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	pw := password.New(password.Config{HashCost: 4})
	e := &passkeyEnv{
		t:       t,
		codes:   map[string]postgres.TemporalCode{},
		keys:    map[uuid.UUID]postgres.UserPasskey{},
		session: uuid.Must(uuid.NewV7()),
	}
	e.user = postgres.User{
		ID:        uuid.Must(uuid.NewV7()),
		Email:     "a@b.test",
		FirstName: "Ann",
		Role:      "user",
		Status:    string(userModel.UserStatusActive),
	}
	if withPassword {
		hashed, _ := pw.Hash(passkeyPass)
		e.user.HashedPassword = pgtype.Text{String: hashed, Valid: true}
	}

	e.stubCodes(repo)
	e.stubAccount(repo)
	e.stubPasskeys(repo)
	e.uc = auth.NewAuthUseCase(auth.Dependencies{
		Repo: repo,
		Token: token.MustNew(
			token.Config{TokenSignature: "test-signing-key-that-is-long-enough"},
		),
		Password: pw,
		Config: config.AuthConfig{
			SessionIdleTTL:     time.Hour,
			SessionAbsoluteTTL: 48 * time.Hour,
			Hosts:              testHosts(passkeyRPID),
		},
		Sessions: testSessions(t),
	})
	return e
}

// stubCodes backs the temporal-code table (the ceremonies' server halves).
func (e *passkeyEnv) stubCodes(repo *postgresMocks.MockQuerier) {
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, a postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error) {
			row := postgres.TemporalCode{
				ID:        a.ID,
				Code:      a.Code,
				Type:      a.Type,
				Data:      a.Data,
				ExpiresAt: a.ExpiresAt,
			}
			e.codes[a.Code] = row
			return row, nil
		})
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, code string) (postgres.TemporalCode, error) {
			if row, ok := e.codes[code]; ok {
				return row, nil
			}
			return postgres.TemporalCode{}, pgx.ErrNoRows
		})
	repo.EXPECT().DeleteTemporalCode(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, id uuid.UUID) (int64, error) {
			for code, row := range e.codes {
				if row.ID == id {
					delete(e.codes, code)
					return 1, nil
				}
			}
			return 0, nil
		})
}

// stubAccount backs the account reads and the session a sign-in opens.
func (e *passkeyEnv) stubAccount(repo *postgresMocks.MockQuerier) {
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, id uuid.UUID) (postgres.User, error) {
			if e.gone || id != e.user.ID {
				return postgres.User{}, pgx.ErrNoRows
			}
			return e.user, nil
		})
	repo.EXPECT().CountUserLoginMethods(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, id uuid.UUID) (int64, error) {
			n := int64(0)
			if e.user.HashedPassword.Valid {
				n++
			}
			for _, k := range e.keys {
				if k.UserID == id {
					n++
				}
			}
			return n, nil
		})
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, _ postgres.CreateSessionParams) (postgres.Session, error) {
			e.signIns++
			return postgres.Session{
				ID:        uuid.Must(uuid.NewV7()),
				UserID:    e.user.ID,
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil
		})
}

// stubPasskeys backs the user_passkeys table with the same rules the SQL enforces.
func (e *passkeyEnv) stubPasskeys(repo *postgresMocks.MockQuerier) {
	repo.EXPECT().CreateUserPasskey(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, a postgres.CreateUserPasskeyParams) (postgres.UserPasskey, error) {
			count := int64(0)
			for _, k := range e.keys {
				if string(k.CredentialID) == string(a.CredentialID) {
					return postgres.UserPasskey{}, &pgconn.PgError{Code: "23505"}
				}
				if k.UserID == a.UserID {
					count++
				}
			}
			if count >= a.MaxPerUser {
				return postgres.UserPasskey{}, pgx.ErrNoRows
			}
			row := postgres.UserPasskey{
				ID:             a.ID,
				UserID:         a.UserID,
				CredentialID:   a.CredentialID,
				PublicKey:      a.PublicKey,
				SignCount:      a.SignCount,
				Aaguid:         a.Aaguid,
				Transports:     a.Transports,
				UserVerified:   a.UserVerified,
				BackupEligible: a.BackupEligible,
				BackupState:    a.BackupState,
				Name:           a.Name,
				CreatedAt:      a.CreatedAt,
			}
			e.keys[a.ID] = row
			return row, nil
		})
	repo.EXPECT().ListUserPasskeys(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, userID uuid.UUID) ([]postgres.UserPasskey, error) {
			out := []postgres.UserPasskey{}
			for _, k := range e.keys {
				if k.UserID == userID {
					out = append(out, k)
				}
			}
			return out, nil
		})
	repo.EXPECT().GetUserPasskeyByCredentialID(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, credID []byte) (postgres.UserPasskey, error) {
			for _, k := range e.keys {
				if string(k.CredentialID) == string(credID) {
					return k, nil
				}
			}
			return postgres.UserPasskey{}, pgx.ErrNoRows
		})
	repo.EXPECT().UseUserPasskey(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, a postgres.UseUserPasskeyParams) (int64, error) {
			k, ok := e.keys[a.ID]
			if !ok || k.CompromisedAt.Valid ||
				(k.SignCount >= a.SignCount && (k.SignCount != 0 || a.SignCount != 0)) {
				return 0, nil
			}
			k.SignCount, k.BackupState, k.LastUsedAt = a.SignCount, a.BackupState, a.UsedAt
			e.keys[a.ID] = k
			return 1, nil
		})
	e.stubPasskeyChanges(repo)
}

// stubPasskeyChanges backs the counter, compromise and delete statements.
func (e *passkeyEnv) stubPasskeyChanges(repo *postgresMocks.MockQuerier) {
	repo.EXPECT().MarkUserPasskeyCompromised(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, a postgres.MarkUserPasskeyCompromisedParams) (int64, error) {
			k, ok := e.keys[a.ID]
			if !ok {
				return 0, nil
			}
			if !k.CompromisedAt.Valid {
				k.CompromisedAt = a.At
			}
			e.keys[a.ID] = k
			return 1, nil
		})
	repo.EXPECT().DeleteUserPasskey(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, a postgres.DeleteUserPasskeyParams) (int64, error) {
			k, ok := e.keys[a.ID]
			if !ok || k.UserID != a.UserID {
				return 0, nil
			}
			delete(e.keys, a.ID)
			return 1, nil
		})
}

// signedIn is the context of a request made with the person's session; recent says how long ago they signed in.
func (e *passkeyEnv) signedIn(recent time.Duration) context.Context {
	return rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{
		UserID:     e.user.ID,
		SessionID:  e.session,
		Role:       rbac.RoleUser,
		SignedInAt: time.Now().Add(-recent),
	})
}

func (e *passkeyEnv) authenticator() *softAuthenticator {
	a := newSoftAuthenticator(e.t, passkeyOrigin, passkeyRPID)
	a.userHandle = e.user.ID.Bytes()
	return a
}

// register runs a whole registration ceremony and returns the stored passkey.
func (e *passkeyEnv) register(a *softAuthenticator, name string) passkeyModel.Passkey {
	e.t.Helper()
	ceremony, err := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)
	if err != nil {
		e.t.Fatalf("BeginPasskeyRegistration: %v", err)
	}
	created, err := e.uc.FinishPasskeyRegistration(
		e.signedIn(0),
		e.user.ID,
		ceremony.CeremonyID,
		name,
		a.create(ceremony.Options),
	)
	if err != nil {
		e.t.Fatalf("FinishPasskeyRegistration: %v", err)
	}
	return created
}

func (e *passkeyEnv) signIn(a *softAuthenticator) (string, error) {
	e.t.Helper()
	ceremony, err := e.uc.BeginPasskeySignIn(context.Background())
	if err != nil {
		e.t.Fatalf("BeginPasskeySignIn: %v", err)
	}
	cookie, _, err := e.uc.PasskeySignIn(
		context.Background(),
		ceremony.CeremonyID,
		"",
		a.get(ceremony.Options),
		authModel.SessionMetadata{},
	)
	return cookie, err
}

func wantErr(t *testing.T, err error, want liberr.ErrorCreator) {
	t.Helper()
	if !errors.Is(err, want.Err()) {
		t.Fatalf("want %v, got %v", want.Err(), err)
	}
}

func TestPasskey_RegisterAndSignIn(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()

	created := e.register(a, "  Laptop  ")
	if created.Name != "Laptop" || !created.UserVerified || !created.BackupEligible ||
		!created.BackupState {
		t.Fatalf("unexpected passkey: %+v", created)
	}
	if len(created.Transports) != 2 || created.UserID != e.user.ID {
		t.Fatalf("unexpected passkey: %+v", created)
	}
	listed, err := e.uc.ListPasskeys(e.signedIn(0), e.user.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListPasskeys: %v %+v", err, listed)
	}

	a.counter = 1
	cookie, err := e.signIn(a)
	if err != nil {
		t.Fatalf("PasskeySignIn: %v", err)
	}
	if cookie == "" || e.signIns != 1 {
		t.Fatalf("want a session, got cookie %q, sessions %d", cookie, e.signIns)
	}
	used, _ := e.uc.ListPasskeys(e.signedIn(0), e.user.ID)
	if used[0].SignCount != 1 || used[0].LastUsedAt == nil {
		t.Fatalf("use was not recorded: %+v", used[0])
	}
}

func TestPasskey_SignInWithoutCounter(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator() // counter stays 0, like iCloud Keychain
	e.register(a, "Phone")
	for range 2 {
		if _, err := e.signIn(a); err != nil {
			t.Fatalf("PasskeySignIn: %v", err)
		}
	}
}

func TestPasskey_SignInReplayedChallenge(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	ceremony, _ := e.uc.BeginPasskeySignIn(context.Background())
	a.counter = 1
	assertion := a.get(ceremony.Options)
	if _, _, err := e.uc.PasskeySignIn(
		context.Background(),
		ceremony.CeremonyID,
		"",
		assertion,
		authModel.SessionMetadata{},
	); err != nil {
		t.Fatalf("first use: %v", err)
	}
	_, _, err := e.uc.PasskeySignIn(
		context.Background(),
		ceremony.CeremonyID,
		"",
		assertion,
		authModel.SessionMetadata{},
	)
	wantErr(t, err, passkeyModel.ErrPasskeyCeremonyInvalid)
	if e.signIns != 1 {
		t.Fatalf("a replay must not open a session, got %d", e.signIns)
	}
}

func TestPasskey_SignInExpiredChallenge(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	ceremony, _ := e.uc.BeginPasskeySignIn(context.Background())
	for code, row := range e.codes {
		row.ExpiresAt = time.Now().Add(-time.Second)
		e.codes[code] = row
	}
	a.counter = 1
	_, _, err := e.uc.PasskeySignIn(
		context.Background(),
		ceremony.CeremonyID,
		"",
		a.get(ceremony.Options),
		authModel.SessionMetadata{},
	)
	wantErr(t, err, passkeyModel.ErrPasskeyCeremonyInvalid)
}

func TestPasskey_ChallengeLivesFiveMinutes(t *testing.T) {
	e := newPasskeyEnv(t, true)
	if _, err := e.uc.BeginPasskeySignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, row := range e.codes {
		if ttl := time.Until(row.ExpiresAt); ttl > 5*time.Minute || ttl < 4*time.Minute {
			t.Fatalf("challenge lives %v, want about 5 minutes", ttl)
		}
	}
}

func TestPasskey_SignInWrongOrigin(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	a.origin = "https://evil.example.test"
	a.counter = 1
	_, err := e.signIn(a)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
}

func TestPasskey_SignInWrongRPID(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	a.rpID = "evil.test"
	a.counter = 1
	_, err := e.signIn(a)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
}

func TestPasskey_SignInWithoutUserVerification(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	a.uv = false
	a.counter = 1
	_, err := e.signIn(a)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
	if e.signIns != 0 {
		t.Fatal("a sign-in without user verification must not open a session")
	}
}

func TestPasskey_RegisterWithoutUserVerification(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	a.uv = false

	ceremony, err := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.uc.FinishPasskeyRegistration(
		e.signedIn(0),
		e.user.ID,
		ceremony.CeremonyID,
		"Key",
		a.create(ceremony.Options),
	)
	wantErr(t, err, passkeyModel.ErrPasskeyRegistrationFailed)
	if len(e.keys) != 0 {
		t.Fatal("nothing may be stored")
	}
}

func TestPasskey_RegisterWrongOrigin(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	a.origin = "https://evil.example.test"

	ceremony, _ := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)
	_, err := e.uc.FinishPasskeyRegistration(
		e.signedIn(0),
		e.user.ID,
		ceremony.CeremonyID,
		"Key",
		a.create(ceremony.Options),
	)
	wantErr(t, err, passkeyModel.ErrPasskeyRegistrationFailed)
}

func TestPasskey_RegisterReplayedChallenge(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	ceremony, _ := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)
	attestation := a.create(ceremony.Options)
	if _, err := e.uc.FinishPasskeyRegistration(
		e.signedIn(0),
		e.user.ID,
		ceremony.CeremonyID,
		"Key",
		attestation,
	); err != nil {
		t.Fatal(err)
	}
	_, err := e.uc.FinishPasskeyRegistration(
		e.signedIn(0),
		e.user.ID,
		ceremony.CeremonyID,
		"Key",
		attestation,
	)
	wantErr(t, err, passkeyModel.ErrPasskeyCeremonyInvalid)
}

func TestPasskey_RegisterCeremonyFromAnotherSession(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	ceremony, _ := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)

	other := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{
		UserID:     e.user.ID,
		SessionID:  uuid.Must(uuid.NewV7()),
		Role:       rbac.RoleUser,
		SignedInAt: time.Now(),
	})
	_, err := e.uc.FinishPasskeyRegistration(
		other,
		e.user.ID,
		ceremony.CeremonyID,
		"Key",
		a.create(ceremony.Options),
	)
	wantErr(t, err, passkeyModel.ErrPasskeyCeremonyInvalid)
}

func TestPasskey_RegisterDuplicateKeyRefused(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	ceremony, err := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.uc.FinishPasskeyRegistration(
		e.signedIn(0),
		e.user.ID,
		ceremony.CeremonyID,
		"Again",
		a.create(ceremony.Options),
	)
	wantErr(t, err, passkeyModel.ErrPasskeyRegistrationFailed)
}

func TestPasskey_RegisterNeedsReauthentication(t *testing.T) {
	e := newPasskeyEnv(t, true)
	_, err := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, "wrong")
	wantErr(t, err, authModel.ErrAuthInvalidOldPassword)

	// An account without a password confirms with a recent sign-in.
	e = newPasskeyEnv(t, false)
	_, err = e.uc.BeginPasskeyRegistration(e.signedIn(time.Hour), e.user.ID, "")
	wantErr(t, err, authModel.ErrAuthReauthRequired)
	if _, err = e.uc.BeginPasskeyRegistration(e.signedIn(time.Minute), e.user.ID, ""); err != nil {
		t.Fatalf("recent sign-in: %v", err)
	}
	// And no session at all is refused.
	_, err = e.uc.BeginPasskeyRegistration(context.Background(), e.user.ID, passkeyPass)
	wantErr(t, err, authModel.ErrAuthReauthRequired)
}

func TestPasskey_RegisterInvalidName(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	ceremony, _ := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)
	for _, name := range []string{"", "   ", "bad\x00name", string(make([]rune, 65))} {
		_, err := e.uc.FinishPasskeyRegistration(
			e.signedIn(0),
			e.user.ID,
			ceremony.CeremonyID,
			name,
			a.create(ceremony.Options),
		)
		wantErr(t, err, passkeyModel.ErrPasskeyInvalidName)
	}
}

func TestPasskey_Limit(t *testing.T) {
	e := newPasskeyEnv(t, true)
	for range passkeyModel.MaxPerUser {
		e.register(e.authenticator(), "Key")
	}
	_, err := e.uc.BeginPasskeyRegistration(e.signedIn(0), e.user.ID, passkeyPass)
	wantErr(t, err, passkeyModel.ErrPasskeyLimit)
}

func TestPasskey_CounterRegressionRefused(t *testing.T) {
	spy := &journalSpy{}
	errorJournal.SetReporter(spy)
	t.Cleanup(func() { errorJournal.SetReporter(nil) })

	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	created := e.register(a, "Laptop")

	a.counter = 5
	if _, err := e.signIn(a); err != nil {
		t.Fatalf("forward counter: %v", err)
	}
	sessionsBefore := e.signIns

	a.counter = 3 // a second copy of the key is behind
	_, err := e.signIn(a)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
	if !e.keys[created.ID].CompromisedAt.Valid {
		t.Fatal("a regressed counter marks the key compromised")
	}
	// Compromised for good: even the correct, higher counter does not sign in any more.
	a.counter = 9
	_, err = e.signIn(a)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)

	if e.signIns != sessionsBefore {
		t.Fatal("a regressed counter must not open a session")
	}
	if got := e.keys[created.ID].SignCount; got != 5 {
		t.Fatalf("the stored counter must not move back, got %d", got)
	}
	if len(spy.events) != 1 || spy.events[0].Key != "passkey-clone:"+created.ID.String() ||
		spy.events[0].UserID == nil {
		t.Fatalf("the clone must reach the error journal once, got %+v", spy.events)
	}
	// It stays listed, flagged, until the person removes it.
	listed, _ := e.uc.ListPasskeys(e.signedIn(0), e.user.ID)
	if len(listed) != 1 || listed[0].CompromisedAt == nil {
		t.Fatalf("the key must stay listed as compromised: %+v", listed)
	}
	if err = e.uc.RemovePasskey(e.signedIn(0), e.user.ID, created.ID, passkeyPass); err != nil {
		t.Fatalf("a compromised key can be removed: %v", err)
	}
}

func TestPasskey_DeletedUserCannotSignIn(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	e.gone = true
	a.counter = 1
	_, err := e.signIn(a)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
	if e.signIns != 0 {
		t.Fatal("a deleted account must not get a session")
	}
}

func TestPasskey_BlockedUserCannotSignIn(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	e.user.Status = string(userModel.UserStatusBlocked)
	a.counter = 1
	_, err := e.signIn(a)
	wantErr(t, err, authModel.ErrAuthAccountBlocked)
	if e.signIns != 0 {
		t.Fatal("a blocked account must not get a session")
	}
}

func TestPasskey_UnknownCredentialLooksLikeAnyFailure(t *testing.T) {
	e := newPasskeyEnv(t, true)
	stranger := e.authenticator() // never registered
	stranger.counter = 1
	_, err := e.signIn(stranger)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
}

func TestPasskey_UserHandleOfAnotherPersonRefused(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	e.register(a, "Laptop")

	ceremony, _ := e.uc.BeginPasskeySignIn(context.Background())
	a.counter = 1
	assertion := a.getWith(ceremony.Options, uuid.Must(uuid.NewV7()).Bytes())
	_, _, err := e.uc.PasskeySignIn(
		context.Background(),
		ceremony.CeremonyID,
		"",
		assertion,
		authModel.SessionMetadata{},
	)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
}

func TestPasskey_Remove(t *testing.T) {
	e := newPasskeyEnv(t, true)
	first := e.register(e.authenticator(), "One")

	// The owner is confirmed first: a wrong password removes nothing.
	wantErr(
		t,
		e.uc.RemovePasskey(e.signedIn(0), e.user.ID, first.ID, "wrong"),
		authModel.ErrAuthInvalidOldPassword,
	)
	if len(e.keys) != 1 {
		t.Fatal("a wrong password must not remove the key")
	}
	// Another person's id looks the same as an unknown one.
	foreign := uuid.Must(uuid.NewV7())
	e.keys[foreign] = postgres.UserPasskey{
		ID:           foreign,
		UserID:       uuid.Must(uuid.NewV7()),
		CredentialID: []byte("foreign"),
	}
	wantErr(
		t,
		e.uc.RemovePasskey(e.signedIn(0), e.user.ID, foreign, passkeyPass),
		passkeyModel.ErrPasskeyNotFound,
	)
	delete(e.keys, foreign)
	if err := e.uc.RemovePasskey(e.signedIn(0), e.user.ID, first.ID, passkeyPass); err != nil {
		t.Fatalf("RemovePasskey: %v", err)
	}
	if len(e.keys) != 0 {
		t.Fatal("the passkey must be gone")
	}
	wantErr(
		t,
		e.uc.RemovePasskey(e.signedIn(0), e.user.ID, first.ID, passkeyPass),
		passkeyModel.ErrPasskeyNotFound,
	)
}

func TestPasskey_RemoveWithoutPasswordNeedsAFreshSignIn(t *testing.T) {
	e := newPasskeyEnv(t, false)
	e.register(e.authenticator(), "One")
	second := e.register(e.authenticator(), "Two")

	wantErr(
		t,
		e.uc.RemovePasskey(e.signedIn(time.Hour), e.user.ID, second.ID, ""),
		authModel.ErrAuthReauthRequired,
	)
	if len(e.keys) != 2 {
		t.Fatal("a stale sign-in must not remove the key")
	}
	if err := e.uc.RemovePasskey(e.signedIn(time.Minute), e.user.ID, second.ID, ""); err != nil {
		t.Fatalf("fresh sign-in: %v", err)
	}
}

func TestPasskey_RemoveLastLoginMethodRefused(t *testing.T) {
	e := newPasskeyEnv(t, false) // no password
	e.user.EmailConfirmed = true
	only := e.register(e.authenticator(), "Only")
	wantErr(
		t,
		e.uc.RemovePasskey(e.signedIn(0), e.user.ID, only.ID, ""),
		authModel.ErrNoLoginMethod,
	)

	second := e.register(e.authenticator(), "Second")
	if err := e.uc.RemovePasskey(e.signedIn(0), e.user.ID, second.ID, ""); err != nil {
		t.Fatalf("with a second key the first may go: %v", err)
	}
}

func TestPasskey_RemovedKeyCannotSignIn(t *testing.T) {
	e := newPasskeyEnv(t, true)
	a := e.authenticator()
	created := e.register(a, "Laptop")
	if err := e.uc.RemovePasskey(e.signedIn(0), e.user.ID, created.ID, passkeyPass); err != nil {
		t.Fatal(err)
	}
	a.counter = 1
	_, err := e.signIn(a)
	wantErr(t, err, passkeyModel.ErrPasskeySignInFailed)
}

func TestPasskey_UnavailableWithoutHosts(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo: postgresMocks.NewMockQuerier(ctrl),
		Token: token.MustNew(
			token.Config{TokenSignature: "test-signing-key-that-is-long-enough"},
		),
		Password: password.New(password.Config{HashCost: 4}),
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour},
		Sessions: testSessions(t),
	})
	if _, err := uc.BeginPasskeySignIn(context.Background()); err == nil {
		t.Fatal("passkeys must refuse to start without resolved hosts")
	}
}
