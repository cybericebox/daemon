package imageRegistryUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

const secretToken = "registry-token-fixed"

var (
	eventA = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-00000000000a"))
	eventB = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-00000000000b"))
	secID  = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e1"))
)

func (f *fixture) sealSecret(t *testing.T, id uuid.UUID, token string) string {
	ct, err := f.cipher.EncryptWithContext([]byte(token), []byte("event-registry:"+id.String()))
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

func (f *fixture) row(t *testing.T, id uuid.UUID, host, prefix, token string) postgres.EventRegistrySecret {
	return postgres.EventRegistrySecret{
		ID: id, EventID: eventA, Host: host, RepositoryPrefix: prefix, Username: "bob",
		TokenCiphertext: f.sealSecret(t, id, token), CreatedAt: t0, UpdatedAt: t0,
	}
}

func uniqueViolation() error {
	return &pgconn.PgError{Code: pgerrcode.UniqueViolation, Detail: "Key (event_id, host, repository_prefix)=(a, ghcr.io, acme) already exists."}
}

func TestCreateEventSecretSealsTheToken(t *testing.T) {
	f := newFixture(t, true)
	var written postgres.InsertEventRegistrySecretParams
	f.q.EXPECT().InsertEventRegistrySecret(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.InsertEventRegistrySecretParams) error { written = p; return nil })

	view, err := f.uc.CreateEventRegistrySecret(context.Background(), eventA, EventSecretInput{
		Host: "GHCR.io", RepositoryPrefix: "Acme/", Username: "bob", Token: secretToken,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if written.EventID != eventA || written.Host != "ghcr.io" || written.RepositoryPrefix != "acme" || written.Username != "bob" {
		t.Fatalf("written %+v", written)
	}
	if written.TokenCiphertext == "" || strings.Contains(written.TokenCiphertext, secretToken) {
		t.Fatalf("token not sealed: %q", written.TokenCiphertext)
	}
	pt, err := f.cipher.DecryptWithContext(written.TokenCiphertext, []byte("event-registry:"+written.ID.String()))
	if err != nil || string(pt) != secretToken {
		t.Fatalf("does not open with its own context: %v", err)
	}
	if _, err := f.cipher.DecryptWithContext(written.TokenCiphertext, []byte("event-registry:"+secID.String())); err == nil {
		t.Fatal("opens with another secret's context")
	}
	if view.ID != written.ID.String() || view.Host != "ghcr.io" || view.Username != "bob" || !view.UpdatedAt.Equal(t0) {
		t.Fatalf("view %+v", view)
	}
}

func TestCreateEventSecretRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("no cipher", func(t *testing.T) {
		f := newFixture(t, false)
		_, err := f.uc.CreateEventRegistrySecret(ctx, eventA, EventSecretInput{Host: "ghcr.io", Username: "bob", Token: secretToken})
		if !errors.Is(err, imageRegistryModel.ErrEventSecretKeyMissing.Err()) || errors.Is(err, imageRegistryModel.ErrSecretsKeyMissing.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid host", func(t *testing.T) {
		f := newFixture(t, true)
		_, err := f.uc.CreateEventRegistrySecret(ctx, eventA, EventSecretInput{Host: "https://ghcr.io", Username: "bob", Token: secretToken})
		if !errors.Is(err, imageRegistryModel.ErrInvalidEventSecret.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("empty token", func(t *testing.T) {
		f := newFixture(t, true)
		_, err := f.uc.CreateEventRegistrySecret(ctx, eventA, EventSecretInput{Host: "ghcr.io", Username: "bob"})
		if !errors.Is(err, imageRegistryModel.ErrEventSecretTokenMissing.Err()) || errors.Is(err, imageRegistryModel.ErrInvalidEventSecret.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("duplicate host and prefix", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().InsertEventRegistrySecret(gomock.Any(), gomock.Any()).Return(uniqueViolation())
		_, err := f.uc.CreateEventRegistrySecret(ctx, eventA, EventSecretInput{Host: "ghcr.io", RepositoryPrefix: "acme", Username: "bob", Token: secretToken})
		if !errors.Is(err, imageRegistryModel.ErrDuplicateEventSecret.Err()) {
			t.Fatalf("got %v", err)
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatal("token in error")
		}
	})
}

func TestListEventSecretsNeverCarriesAToken(t *testing.T) {
	f := newFixture(t, true)
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).
		Return([]postgres.EventRegistrySecret{f.row(t, secID, "ghcr.io", "acme", secretToken)}, nil)
	views, err := f.uc.ListEventRegistrySecrets(context.Background(), eventA)
	if err != nil || len(views) != 1 {
		t.Fatalf("list %v %v", views, err)
	}
	b, _ := json.Marshal(views[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "Check,Host,ID,RepositoryPrefix,UpdatedAt,Username" {
		t.Fatalf("fields %v", keys)
	}
	if strings.Contains(string(b), secretToken) || strings.Contains(strings.ToLower(string(b)), "ciphertext") {
		t.Fatalf("view leaks: %s", b)
	}
}

func TestUpdateEventSecretKeepsOrReplacesTheToken(t *testing.T) {
	ctx := context.Background()
	t.Run("empty token keeps the stored one", func(t *testing.T) {
		f := newFixture(t, true)
		row := f.row(t, secID, "ghcr.io", "acme", secretToken)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), postgres.GetEventRegistrySecretParams{ID: secID, EventID: eventA}).Return(row, nil)
		var written postgres.UpdateEventRegistrySecretParams
		f.q.EXPECT().UpdateEventRegistrySecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, p postgres.UpdateEventRegistrySecretParams) (int64, error) {
				written = p
				return 1, nil
			})
		view, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "ghcr.io", RepositoryPrefix: "team", Username: "alice"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if written.TokenCiphertext != row.TokenCiphertext || written.RepositoryPrefix != "team" || written.Username != "alice" || written.ID != secID || written.EventID != eventA {
			t.Fatalf("written %+v", written)
		}
		if view.RepositoryPrefix != "team" || view.Username != "alice" {
			t.Fatalf("view %+v", view)
		}
	})
	t.Run("new token is resealed", func(t *testing.T) {
		f := newFixture(t, true)
		row := f.row(t, secID, "ghcr.io", "acme", secretToken)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(row, nil)
		var written postgres.UpdateEventRegistrySecretParams
		f.q.EXPECT().UpdateEventRegistrySecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, p postgres.UpdateEventRegistrySecretParams) (int64, error) {
				written = p
				return 1, nil
			})
		if _, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "ghcr.io", RepositoryPrefix: "acme", Username: "bob", Token: "new-token"}); err != nil {
			t.Fatal(err)
		}
		pt, err := f.cipher.DecryptWithContext(written.TokenCiphertext, []byte("event-registry:"+secID.String()))
		if err != nil || string(pt) != "new-token" {
			t.Fatalf("resealed: %v", err)
		}
	})
	t.Run("new token without a cipher is refused", func(t *testing.T) {
		f := newFixture(t, false)
		_, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "ghcr.io", Username: "bob", Token: "new-token"})
		if !errors.Is(err, imageRegistryModel.ErrEventSecretTokenKeyMissing.Err()) || errors.Is(err, imageRegistryModel.ErrEventSecretKeyMissing.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no token and no cipher still edits the rest", func(t *testing.T) {
		f := newFixture(t, false)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(postgres.EventRegistrySecret{
			ID: secID, EventID: eventA, Host: "ghcr.io", Username: "bob", TokenCiphertext: "stored", CreatedAt: t0, UpdatedAt: t0,
		}, nil)
		var written postgres.UpdateEventRegistrySecretParams
		f.q.EXPECT().UpdateEventRegistrySecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, p postgres.UpdateEventRegistrySecretParams) (int64, error) {
				written = p
				return 1, nil
			})
		if _, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "ghcr.io", Username: "alice"}); err != nil || written.TokenCiphertext != "stored" {
			t.Fatalf("%v %+v", err, written)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.row(t, secID, "ghcr.io", "acme", secretToken), nil)
		f.q.EXPECT().UpdateEventRegistrySecret(gomock.Any(), gomock.Any()).Return(int64(0), uniqueViolation())
		_, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "ghcr.io", RepositoryPrefix: "other", Username: "bob"})
		if !errors.Is(err, imageRegistryModel.ErrDuplicateEventSecret.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid host", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.row(t, secID, "ghcr.io", "acme", secretToken), nil)
		_, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "ghcr.io/acme", Username: "bob"})
		if !errors.Is(err, imageRegistryModel.ErrInvalidEventSecret.Err()) {
			t.Fatalf("got %v", err)
		}
	})
}

// A secret of event A is not found through event B's route: the queries are
// scoped by event, so another event's id simply matches no row.
func TestSecretOfAnotherEventIsNotFound(t *testing.T) {
	ctx := context.Background()
	notFound := imageRegistryModel.ErrEventSecretNotFound.Err()
	t.Run("update", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), postgres.GetEventRegistrySecretParams{ID: secID, EventID: eventB}).Return(postgres.EventRegistrySecret{}, pgx.ErrNoRows)
		_, err := f.uc.UpdateEventRegistrySecret(ctx, eventB, secID, EventSecretInput{Host: "ghcr.io", Username: "bob"})
		if !errors.Is(err, notFound) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("update raced with a delete", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.row(t, secID, "ghcr.io", "acme", secretToken), nil)
		f.q.EXPECT().UpdateEventRegistrySecret(gomock.Any(), gomock.Any()).Return(int64(0), nil)
		_, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "ghcr.io", Username: "bob"})
		if !errors.Is(err, notFound) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().DeleteEventRegistrySecret(gomock.Any(), postgres.DeleteEventRegistrySecretParams{ID: secID, EventID: eventB}).Return(int64(0), nil)
		if err := f.uc.DeleteEventRegistrySecret(ctx, eventB, secID); !errors.Is(err, notFound) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("delete own", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().DeleteEventRegistrySecret(gomock.Any(), postgres.DeleteEventRegistrySecretParams{ID: secID, EventID: eventA}).Return(int64(1), nil)
		if err := f.uc.DeleteEventRegistrySecret(ctx, eventA, secID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEventRegistrySecretsForDeploy(t *testing.T) {
	ctx := context.Background()
	other := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e2"))
	broken := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e3"))
	t.Run("opens the readable tokens and skips the rest", func(t *testing.T) {
		f := newFixture(t, true)
		bad := f.row(t, broken, "quay.io", "", "x")
		bad.TokenCiphertext = f.sealSecret(t, secID, "sealed for another secret")
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			f.row(t, secID, "ghcr.io", "acme", secretToken), bad, f.row(t, other, "docker.io", "", "hub-token"),
		}, nil)
		secrets, tokens, err := f.uc.EventRegistrySecretsForDeploy(ctx, eventA)
		if err != nil {
			t.Fatal(err)
		}
		if len(secrets) != 2 || len(tokens) != 2 || tokens[secID] != secretToken || tokens[other] != "hub-token" {
			t.Fatalf("secrets %v tokens %v", secrets, tokens)
		}
		for _, s := range secrets {
			if s.ID == broken || s.EventID != eventA {
				t.Fatalf("unexpected %+v", s)
			}
		}
		if _, ok := tokens[broken]; ok {
			t.Fatal("unopenable token returned")
		}
	})
	t.Run("no cipher: nothing usable", func(t *testing.T) {
		f := newFixture(t, false)
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			{ID: secID, EventID: eventA, Host: "ghcr.io", Username: "bob", TokenCiphertext: "x", CreatedAt: t0, UpdatedAt: t0},
		}, nil)
		secrets, tokens, err := f.uc.EventRegistrySecretsForDeploy(ctx, eventA)
		if err != nil || len(secrets) != 0 || len(tokens) != 0 {
			t.Fatalf("%v %v %v", secrets, tokens, err)
		}
	})
}

// A stored token must not be repointed at another registry host: changing the
// (normalized) host needs a new token. Prefix or username alone may keep it.
func TestUpdateEventSecretHostChangeNeedsANewToken(t *testing.T) {
	ctx := context.Background()
	tokenRequired := imageRegistryModel.ErrEventSecretTokenRequired.Err()
	t.Run("host changed, empty token", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.row(t, secID, "ghcr.io", "acme", secretToken), nil)
		_, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "evil.example.com", RepositoryPrefix: "acme", Username: "bob"})
		if !errors.Is(err, tokenRequired) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("host changed with a new token", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.row(t, secID, "ghcr.io", "acme", secretToken), nil)
		f.q.EXPECT().UpdateEventRegistrySecret(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		if _, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "quay.io", Username: "bob", Token: "new"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("alias of the same host keeps the token", func(t *testing.T) {
		f := newFixture(t, true)
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.row(t, secID, "docker.io", "", secretToken), nil)
		f.q.EXPECT().UpdateEventRegistrySecret(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		if _, err := f.uc.UpdateEventRegistrySecret(ctx, eventA, secID, EventSecretInput{Host: "Index.Docker.io", Username: "bob"}); err != nil {
			t.Fatal(err)
		}
	})
}
