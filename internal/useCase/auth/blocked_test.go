package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/oauth"
)

// No CreateSession is expected in any of these: a blocked account never gets a session.

func TestSignIn_BlockedCorrectPassword_ReturnsBlocked(t *testing.T) {
	uc, repo, pw := newUC(t)
	hashed, _ := pw.Hash("Secret!1")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusBlocked),
		HashedPassword: pgtype.Text{String: hashed, Valid: true},
	}, nil)

	_, _, err := uc.SignIn(context.Background(), "a@b.test", "Secret!1", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthAccountBlocked.Err()) {
		t.Fatalf("want ErrAuthAccountBlocked, got %v", err)
	}
}

// Blocked is revealed only after the password matched.
func TestSignIn_BlockedWrongPassword_ReturnsInvalidCredentials(t *testing.T) {
	uc, repo, pw := newUC(t)
	hashed, _ := pw.Hash("Secret!1")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusBlocked),
		HashedPassword: pgtype.Text{String: hashed, Valid: true},
	}, nil)

	_, _, err := uc.SignIn(context.Background(), "a@b.test", "wrong", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthInvalidUserCredentials.Err()) {
		t.Fatalf("want ErrAuthInvalidUserCredentials, got %v", err)
	}
}

func TestGoogleAuth_Blocked_ReturnsBlocked(t *testing.T) {
	uc, repo := newGoogleUCRedirect(t, &oauth.GoogleUser{GoogleID: "g-1", Email: "a@b.test"}, "https://event.test/e/1")
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).
		Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusBlocked)}, nil)

	_, redirect, err := uc.GoogleAuth(context.Background(), "code", "state", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthAccountBlocked.Err()) {
		t.Fatalf("want ErrAuthAccountBlocked, got %v", err)
	}
	if redirect != "https://event.test/e/1" {
		t.Fatalf("safe redirect must survive the error, got %q", redirect)
	}
}

func TestBeginGoogleRegistration_LinkedBlocked_ReturnsBlocked(t *testing.T) {
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-1", Email: "a@b.test"})
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).
		Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusBlocked)}, nil)

	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthAccountBlocked.Err()) {
		t.Fatalf("want ErrAuthAccountBlocked, got %v (res=%+v)", err, res)
	}
}
