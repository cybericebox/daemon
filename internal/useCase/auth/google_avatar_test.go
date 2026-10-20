package auth_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/session"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/oauth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

type avatarRoundTripFunc func(*http.Request) (*http.Response, error)

func (f avatarRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newGoogleAvatarUC(t *testing.T, email string, downloadStatus int) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *fakeAvatarStorage, *session.Runtime, *int) {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	av := &fakeAvatarStorage{uploadFile: mediaModel.File{ID: uuid.Must(uuid.NewV7()), ContentType: "image/png"}}
	rt := testSessions(t)
	t.Cleanup(func() { rt.Stop(context.Background()) })
	downloads := 0
	t.Cleanup(auth.ExportSetAvatarDownloadClient(&http.Client{Transport: avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		downloads++
		if req.URL.String() != "https://lh3.googleusercontent.com/avatar" {
			t.Errorf("unexpected avatar URL: %s", req.URL)
		}
		return &http.Response{StatusCode: downloadStatus, Body: io.NopCloser(bytes.NewReader(pngBytes(0)))}, nil
	})}))
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo: repo, Sessions: rt,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		OAuth:    &fakeOAuth{user: &oauth.GoogleUser{GoogleID: "google-avatar", Email: email, Picture: "https://lh3.googleusercontent.com/avatar"}, redirect: "https://event.test/e/1"},
		Storage:  &fakeLegacyStorage{}, Avatar: av,
		Config: config.AuthConfig{SessionIdleTTL: time.Hour, Hosts: testHosts("test")},
	})
	return uc, repo, av, rt, &downloads
}

func TestLinkGoogleToSetupFromOAuth_AdoptsAvatar(t *testing.T) {
	for _, invited := range []bool{false, true} {
		name := "email_registration"
		if invited {
			name = "invitation"
		}
		t.Run(name, func(t *testing.T) {
			uid := uuid.Must(uuid.NewV7())
			uc, repo, av, _, downloads := newGoogleAvatarUC(t, "a@b.test", http.StatusOK)
			tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
			setupToken := liveSetupLink(t, repo, tk, uid)
			user := postgres.User{ID: uid, Email: "a@b.test", Status: "incomplete", Role: "user"}
			if invited {
				user.FirstName, user.LastName, user.Role, user.EmailConfirmed = "Invited", "Person", "admin_viewer", true
			}
			repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(user, nil).AnyTimes()
			repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
			repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)
			var savedPicture string
			repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
				savedPicture = arg.Picture
				if arg.Status != "incomplete" || arg.Role != user.Role || arg.FirstName != user.FirstName || arg.LastName != user.LastName || arg.EmailConfirmed != user.EmailConfirmed {
					t.Errorf("avatar import changed onboarding state: %+v", arg)
				}
				return 1, nil
			})
			returnTo, err := uc.LinkGoogleToSetupFromOAuth(context.Background(), setupToken, "code", "state")
			if err != nil || returnTo != "https://event.test/e/1" {
				t.Fatalf("onboarding link: returnTo=%q err=%v", returnTo, err)
			}
			if !strings.HasPrefix(savedPicture, "/api/auth/avatar/"+uid.String()+"?v=") {
				t.Fatalf("want a re-hosted avatar, got %q", savedPicture)
			}
			if *downloads != 1 || av.uploadedBy != uid || av.replacedRefType != mediaModel.RefTypeUserAvatar || av.replacedRefID != uid || len(av.replacedIDs) != 1 || av.replacedIDs[0] != av.uploadFile.ID {
				t.Fatalf("avatar was not re-hosted and referenced for this user: downloads=%d media=%+v", *downloads, av)
			}
		})
	}
}

func TestLinkGoogleToSetupFromOAuth_PreservesExistingAvatar(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo, av, _, downloads := newGoogleAvatarUC(t, "a@b.test", http.StatusOK)
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	setupToken := liveSetupLink(t, repo, tk, uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "a@b.test", Status: "incomplete", Picture: "/existing-avatar"}, nil)
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)
	if _, err := uc.LinkGoogleToSetupFromOAuth(context.Background(), setupToken, "code", "state"); err != nil {
		t.Fatal(err)
	}
	if *downloads != 0 || av.replaceCalls != 0 {
		t.Fatal("linking must preserve an existing avatar")
	}
}

func TestLinkGoogleToSetupFromOAuth_AvatarFailureDoesNotBlockSetup(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo, av, _, downloads := newGoogleAvatarUC(t, "a@b.test", http.StatusServiceUnavailable)
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	setupToken := liveSetupLink(t, repo, tk, uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "a@b.test", Status: "incomplete"}, nil)
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)
	if _, err := uc.LinkGoogleToSetupFromOAuth(context.Background(), setupToken, "code", "state"); err != nil {
		t.Fatalf("optional avatar failure must not block registration: %v", err)
	}
	if *downloads != 1 || av.replaceCalls != 0 {
		t.Fatalf("want one failed download and no avatar replacement, got downloads=%d replacements=%d", *downloads, av.replaceCalls)
	}
}

func TestLinkGoogleToSetupFromOAuth_RejectsForeignEmailBeforeAvatar(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo, av, _, downloads := newGoogleAvatarUC(t, "foreign@b.test", http.StatusOK)
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	setupToken := liveSetupLink(t, repo, tk, uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "a@b.test", Status: "incomplete"}, nil)
	_, err := uc.LinkGoogleToSetupFromOAuth(context.Background(), setupToken, "code", "state")
	if !errors.Is(err, authModel.ErrAuthGoogleEmailMismatch.Err()) {
		t.Fatalf("want email mismatch, got %v", err)
	}
	if *downloads != 0 || av.replaceCalls != 0 {
		t.Fatal("a rejected Google identity must never change an avatar")
	}
}

func TestLinkGoogleToAccountFromOAuth_DoesNotImportAvatar(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo, av, rt, downloads := newGoogleAvatarUC(t, "a@b.test", http.StatusOK)
	cookie, err := rt.Codec.Seal(session.NewTicket(uuid.Must(uuid.NewV7()), uid, time.Now(), session.Lifetimes{Idle: time.Hour}))
	if err != nil {
		t.Fatal(err)
	}
	repo.EXPECT().GetUserAccess(gomock.Any(), uid).Return(postgres.GetUserAccessRow{Role: string(rbac.RoleUser), Status: string(userModel.UserStatusActive)}, nil)
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)
	repo.EXPECT().TouchSession(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().UpdateUserLastSeen(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	// The account is read only to compare its address; no UpdateUser: account linking never changes the
	// profile, whether its current avatar is empty or explicitly chosen.
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Email: "a@b.test", Status: "active"}, nil)
	if err := uc.LinkGoogleToAccountFromOAuth(context.Background(), cookie, "code", "state"); err != nil {
		t.Fatal(err)
	}
	if *downloads != 0 || av.replaceCalls != 0 {
		t.Fatal("linking to a completed account must not import or overwrite an avatar")
	}
}

func TestBeginGoogleRegistration_ResumedSetupAdoptsMissingAvatar(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo, av, _, downloads := newGoogleAvatarUC(t, "a@b.test", http.StatusOK)
	allowSetupLinkIssue(repo)
	user := postgres.User{ID: uid, Email: "a@b.test", Status: "incomplete", Role: "user"}
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(user, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(user, nil)
	var savedPicture string
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
		savedPicture = arg.Picture
		return 1, nil
	})
	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil || res.SetupToken == "" || res.SessionCookie != "" {
		t.Fatalf("resume registration: result=%+v err=%v", res, err)
	}
	if !strings.HasPrefix(savedPicture, "/api/auth/avatar/"+uid.String()+"?v=") || *downloads != 1 || av.replacedRefID != uid {
		t.Fatalf("resuming setup must backfill a missing Google avatar: picture=%q downloads=%d media=%+v", savedPicture, *downloads, av)
	}
}

// A signed-in account links exactly the Google account of its own address (the owner's rule); another one is
// refused before anything is linked. The comparison ignores case.
func TestLinkGoogleToAccountFromOAuth_LinksOnlyTheAccountEmail(t *testing.T) {
	for _, tc := range []struct {
		accountEmail string
		wantErr      bool
	}{
		{"other@b.test", true},
		{"A@B.test", false},
	} {
		uid := uuid.Must(uuid.NewV7())
		uc, repo, _, rt, _ := newGoogleAvatarUC(t, "a@b.test", http.StatusOK)
		lifetimes := session.Lifetimes{Idle: time.Hour}
		ticket := session.NewTicket(uuid.Must(uuid.NewV7()), uid, time.Now(), lifetimes)
		cookie, err := rt.Codec.Seal(ticket)
		if err != nil {
			t.Fatal(err)
		}
		repo.EXPECT().GetUserAccess(gomock.Any(), uid).
			Return(postgres.GetUserAccessRow{Role: string(rbac.RoleUser), Status: string(userModel.UserStatusActive)}, nil)
		repo.EXPECT().TouchSession(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
		repo.EXPECT().UpdateUserLastSeen(gomock.Any(), gomock.Any()).
			Return(int64(1), nil).AnyTimes()
		repo.EXPECT().GetUserByID(gomock.Any(), uid).
			Return(postgres.User{ID: uid, Email: tc.accountEmail, Status: "active"}, nil)
		if !tc.wantErr {
			repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).
				Return(postgres.User{}, pgx.ErrNoRows)
			repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).
				Return(postgres.UserProvider{}, nil)
		}
		err = uc.LinkGoogleToAccountFromOAuth(context.Background(), cookie, "code", "state")
		mismatch := errors.Is(err, authModel.ErrAuthGoogleEmailMismatch.Err())
		if tc.wantErr != mismatch || (!tc.wantErr && err != nil) {
			t.Fatalf("account %q: err = %v", tc.accountEmail, err)
		}
	}
}
