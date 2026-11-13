package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

const exportHash = "$2a$04$thisIsTheSecretPasswordHashThatMustNeverLeaveTheServerXXXXXXXX"

func newExportUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo: repo,
		Token: token.MustNew(
			token.Config{TokenSignature: "test-signing-key-that-is-long-enough"},
		),
		Password: password.New(password.Config{HashCost: 4}),
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour, Hosts: testHosts("example.test")},
		Sessions: testSessions(t),
	})
	return uc, repo
}

func freshCtx(uid uuid.UUID, signedInAgo time.Duration) context.Context {
	return rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{
		UserID:     uid,
		SessionID:  uuid.Must(uuid.NewV7()),
		Role:       rbac.RoleUser,
		SignedInAt: time.Now().Add(-signedInAgo),
	})
}

// expectOwnRows stubs every read of the export for uid ONLY: a call keyed on any other id has no expectation, so
// gomock fails the test.
func expectOwnRows(repo *postgresMocks.MockQuerier, uid uuid.UUID) {
	now := time.Now()
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{
		ID:             uid,
		Email:          "me@b.test",
		FirstName:      "Me",
		LastName:       "Myself",
		Role:           "user",
		Status:         "active",
		HashedPassword: pgtype.Text{String: exportHash, Valid: true},
		TosAcceptedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		LegalDocuments: []string{"terms", "privacy"},
		CreatedAt:      now,
	}, nil)
	repo.EXPECT().GetUserProviders(gomock.Any(), uid).Return([]postgres.UserProvider{
		{UserID: uid, Provider: "google", ProviderUserID: "google-subject-123456"},
	}, nil)
	repo.EXPECT().ListUserPasskeys(gomock.Any(), uid).Return([]postgres.UserPasskey{
		{
			ID:           uuid.Must(uuid.NewV7()),
			UserID:       uid,
			Name:         "Laptop",
			CredentialID: []byte("credential-id-bytes"),
			PublicKey:    []byte("public-key-bytes"),
			CreatedAt:    now,
		},
	}, nil)
	repo.EXPECT().GetSessionsByUser(gomock.Any(), uid).Return([]postgres.Session{
		{
			ID:        uuid.Must(uuid.NewV7()),
			UserID:    uid,
			Metadata:  []byte(`{"user_agent":"Firefox","ip":"203.0.113.7"}`),
			CreatedAt: now,
			LastSeen:  now,
		},
	}, nil)
	repo.EXPECT().
		ExportUserParticipations(gomock.Any(), uid).
		Return([]postgres.ExportUserParticipationsRow{
			{
				EventID:   eventID,
				EventName: "CTF",
				EventTag:  "ctf",
				TeamName:  "Red Team",
				CreatedAt: now,
				IsCaptain: true,
			},
		}, nil)
	repo.EXPECT().ExportUserEventStaffRoles(gomock.Any(), uid).Return(nil, nil)
	repo.EXPECT().
		ExportUserFlagAttempts(gomock.Any(), uid).
		Return([]postgres.ExportUserFlagAttemptsRow{
			{
				EventID:       eventID,
				EventName:     "CTF",
				TeamName:      "Red Team",
				ChallengeName: "Warmup",
				Answer:        "flag{mine}",
				Correct:       true,
				ReceivedAt:    now,
			},
		}, nil)
	repo.EXPECT().
		ExportUserFormAnswers(gomock.Any(), uid).
		Return([]postgres.ExportUserFormAnswersRow{
			{
				EventID:       eventID,
				EventName:     "CTF",
				FormVersionID: uuid.Must(uuid.NewV7()),
				Answers:       []byte(`{"school":"No 5"}`),
				SubmittedAt:   now,
			},
		}, nil)
	repo.EXPECT().ExportUserInbox(gomock.Any(), uid).Return([]postgres.ExportUserInboxRow{
		{
			Title:            "Welcome",
			Body:             "Hi",
			NotificationType: "welcome",
			Category:         "info",
			CreatedAt:        now,
		},
	}, nil)
	repo.EXPECT().ExportUserNotificationSettings(gomock.Any(), uid).Return(nil, nil)
	repo.EXPECT().ExportUserTablePrefs(gomock.Any(), uid).Return([]postgres.ExportUserTablePrefsRow{
		{TableKey: "admin.users", Prefs: []byte(`{"PageSize":50}`), UpdatedAt: now},
	}, nil)
}

func TestExportMyData_OwnDataOnlyAndNoSecrets(t *testing.T) {
	uc, repo := newExportUC(t)
	uid := uuid.Must(uuid.NewV7())
	expectOwnRows(repo, uid)

	data, e := uc.ExportMyData(freshCtx(uid, time.Minute), uid)
	if e != nil {
		t.Fatalf("ExportMyData: %v", e)
	}
	raw, _ := json.Marshal(data) //nolint:musttag // PascalCase field names are the file format
	text := string(raw)

	for _, want := range []string{"me@b.test", "Red Team", "flag{mine}", "Warmup", "Laptop", "Firefox", "203.0.113.7", "No 5", "Welcome", "admin.users"} {
		if !strings.Contains(text, want) {
			t.Errorf("the export must contain %q", want)
		}
	}
	if got := data.Profile.LegalDocuments; len(got) != 2 || got[0] != "terms" || got[1] != "privacy" || data.Profile.TosAcceptedAt == nil {
		t.Errorf("the export shows the acceptance time and the documents: %+v", data.Profile)
	}
	if strings.Contains(text, "TosVersion") {
		t.Error("the version number no longer exists")
	}
	// No secrets: the hash, the key material, the provider's subject id, credential ids.
	for _, secret := range []string{exportHash, "public-key", "credential-id", "google-subject", "HashedPassword", "PublicKey", "CredentialID", "ProviderUserID", "ExpectedFlag"} {
		if strings.Contains(text, secret) {
			t.Errorf("the export must not contain %q", secret)
		}
	}
	// The sign-in methods are kinds, names and dates.
	kinds := map[string]string{}
	for _, m := range data.SignInMethods {
		kinds[m.Type] = m.Name
	}
	if _, ok := kinds["password"]; !ok {
		t.Errorf("password missing: %+v", data.SignInMethods)
	}
	if _, ok := kinds["google"]; !ok {
		t.Errorf("google missing: %+v", data.SignInMethods)
	}
	if kinds["passkey"] != "Laptop" {
		t.Errorf("passkey missing: %+v", data.SignInMethods)
	}
	if len(data.Participations) != 1 || data.Participations[0].TeamName != "Red Team" ||
		!data.Participations[0].IsCaptain {
		t.Errorf("participation: %+v", data.Participations)
	}
}

func TestExportMyData_NeedsAFreshSignIn(t *testing.T) {
	uc, _ := newExportUC(t) // no repository call is expected
	uid := uuid.Must(uuid.NewV7())
	_, e := uc.ExportMyData(freshCtx(uid, time.Hour), uid)
	wantErr(t, e, authModel.ErrAuthReauthRequired)
	_, e = uc.ExportMyData(context.Background(), uid)
	wantErr(t, e, authModel.ErrAuthReauthRequired)
}

func TestExportMyData_OncePerTenMinutes(t *testing.T) {
	uc, repo := newExportUC(t)
	uid := uuid.Must(uuid.NewV7())
	expectOwnRows(repo, uid)
	if _, e := uc.ExportMyData(freshCtx(uid, time.Minute), uid); e != nil {
		t.Fatal(e)
	}
	_, e := uc.ExportMyData(freshCtx(uid, time.Minute), uid)
	tooMany(t, e)
	var ec interface{ StatusCode() err.StatusCode }
	if !errors.As(e, &ec) {
		t.Fatalf("not a status error: %T", e)
	}
	if secs, ok := ec.StatusCode().Details()[err.DetailRetryAfterSeconds].(int64); !ok ||
		secs < 1 ||
		secs > 600 {
		t.Fatalf("RetryAfterSeconds missing or off: %v", ec.StatusCode().Details())
	}
	// Another person has their own allowance.
	other := uuid.Must(uuid.NewV7())
	expectOwnRows(repo, other)
	if _, e = uc.ExportMyData(freshCtx(other, time.Minute), other); e != nil {
		t.Fatalf("another person: %v", e)
	}
}

// Every export query reads one person's rows by their own user id, and none selects another person's name.
func TestExportQueries_AreKeyedOnTheCallerAndNameNoOnes(t *testing.T) {
	raw, readErr := os.ReadFile("../../delivery/repository/postgres/queries/user_export.sql")
	if readErr != nil {
		t.Fatal(readErr)
	}
	blocks := regexp.MustCompile(`(?m)^-- name: `).Split(string(raw), -1)[1:]
	if len(blocks) < 6 {
		t.Fatalf("expected the six export queries, got %d", len(blocks))
	}
	owner := regexp.MustCompile(`(?i)WHERE\s+\w+\.user_id\s*=\s*\$1|WHERE\s+user_id\s*=\s*\$1`)
	people := regexp.MustCompile(
		`(?i)\b(first_name|last_name|email|users)\b|captain_id\s+AS|invited_by|decided_by|resolved_by`,
	)
	for _, b := range blocks {
		name := strings.Fields(b)[0]
		sqlOnly := regexp.MustCompile(`(?m)^--.*$`).ReplaceAllString(b, "")
		if !owner.MatchString(sqlOnly) {
			t.Errorf("%s: must be keyed on the caller's user id ($1)", name)
		}
		if people.MatchString(sqlOnly) {
			t.Errorf("%s: must not read another person's name or identity", name)
		}
	}
}
