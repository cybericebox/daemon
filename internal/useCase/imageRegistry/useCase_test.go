package imageRegistryUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/provider"
	"github.com/cybericebox/daemon/pkg/secret"
)

var t0 = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

const (
	pushToken = "push-token-fixed"
	pullToken = "pull-token-fixed"
)

// fakeRegistry is an in-process registry behind basic auth: the account
// "acme" with the push or the pull token is accepted; it records the
// passwords it saw.
type fakeRegistry struct {
	srv   *httptest.Server
	mu    sync.Mutex
	seen  []string
	allow map[string]bool
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	f := &fakeRegistry{allow: map[string]bool{pushToken: true, pullToken: true}}
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if ok {
			f.mu.Lock()
			f.seen = append(f.seen, p)
			f.mu.Unlock()
		}
		if !ok || u != "acme" || !f.allow[p] {
			w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRegistry) passwords() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func (f *fakeRegistry) newClient(_ imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{
		Host: strings.TrimPrefix(f.srv.URL, "http://"), Repository: repository,
		Username: username, Password: password, Insecure: true, AllowInternal: true,
	})
}

type fakeAdapter struct {
	repos  []string
	err    error
	tokens []string
}

func (a *fakeAdapter) ListRepositories(_ context.Context, _, token string) ([]string, error) {
	a.tokens = append(a.tokens, token)
	return a.repos, a.err
}

func (a *fakeAdapter) DeleteTags(context.Context, string, string, string, []string) error { return nil }

func testCipher(t *testing.T) *secret.Cipher {
	c, err := secret.NewFromBytes([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type fixture struct {
	uc      *ImageRegistryUseCase
	q       *postgresMocks.MockQuerier
	reg     *fakeRegistry
	adapter *fakeAdapter
	cipher  *secret.Cipher
}

func newFixture(t *testing.T, withCipher bool) *fixture {
	ctrl := gomock.NewController(t)
	f := &fixture{q: postgresMocks.NewMockQuerier(ctrl), reg: newFakeRegistry(t), adapter: &fakeAdapter{}}
	deps := Dependencies{
		Repo:      f.q,
		NewClient: f.reg.newClient,
		Adapter:   func(imageRegistryModel.Provider) provider.Adapter { return f.adapter },
		Now:       func() time.Time { return t0 },
	}
	if withCipher {
		f.cipher = testCipher(t)
		deps.Cipher = f.cipher
	}
	f.uc = New(deps)
	return f
}

func (f *fixture) seal(t *testing.T, token, kind string) string {
	ct, err := f.cipher.EncryptWithContext([]byte(token), []byte("image-registry:"+kind))
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

func (f *fixture) open(t *testing.T, ct, kind string) string {
	pt, err := f.cipher.DecryptWithContext(ct, []byte("image-registry:"+kind))
	if err != nil {
		t.Fatalf("open %s: %v", kind, err)
	}
	return string(pt)
}

func (f *fixture) stored(row postgres.ImageRegistrySetting) {
	f.q.EXPECT().GetImageRegistrySettings(gomock.Any()).Return(row, nil).AnyTimes()
}

func (f *fixture) empty() {
	f.q.EXPECT().GetImageRegistrySettings(gomock.Any()).Return(postgres.ImageRegistrySetting{}, pgx.ErrNoRows).AnyTimes()
}

func publicReason(t *testing.T, e error) map[string]any {
	t.Helper()
	var pe interface{ PublicContext() map[string]any }
	if !errors.As(e, &pe) {
		t.Fatalf("not an app error: %T %v", e, e)
	}
	return pe.PublicContext()
}

func TestSaveSealsTokens(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	var written postgres.UpsertImageRegistrySettingsParams
	f.q.EXPECT().UpsertImageRegistrySettings(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpsertImageRegistrySettingsParams) error { written = p; return nil })

	view, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushToken: pushToken, PullToken: pullToken,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if written.PushTokenCiphertext == "" || written.PushTokenCiphertext == pushToken || written.PullTokenCiphertext == pullToken {
		t.Fatalf("tokens not sealed: %+v", written)
	}
	if f.open(t, written.PushTokenCiphertext, "push") != pushToken || f.open(t, written.PullTokenCiphertext, "pull") != pullToken {
		t.Fatal("sealed tokens do not open with their contexts")
	}
	if written.Provider != "dockerhub" || written.Account != "acme" || written.Repository != "acme/platform" || !written.UpdatedAt.Equal(t0) {
		t.Fatalf("written row: %+v", written)
	}
	if !view.Configured || !view.PushTokenSet || !view.PullTokenSet || view.UpdatedAt == nil || !view.UpdatedAt.Equal(t0) {
		t.Fatalf("view: %+v", view)
	}
}

func TestSettingsViewNeverCarriesATokenField(t *testing.T) {
	f := newFixture(t, true)
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
		PushTokenCiphertext: f.seal(t, pushToken, "push"), UpdatedAt: t0,
	})
	view, err := f.uc.GetImageRegistrySettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(view)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "Account,Configured,Provider,PullTokenSet,PushTokenSet,Repository,SecretsKeyMissing,UpdatedAt"
	if strings.Join(keys, ",") != want {
		t.Fatalf("view fields %v, want %s", keys, want)
	}
	if strings.Contains(string(b), pushToken) || !view.PushTokenSet || view.PullTokenSet || !view.Configured {
		t.Fatalf("view %s", b)
	}
}

func TestGetSettingsNotConfigured(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	view, err := f.uc.GetImageRegistrySettings(context.Background())
	if err != nil || view.Configured || view.UpdatedAt != nil {
		t.Fatalf("view %+v err %v", view, err)
	}
}

func TestEmptyTokenKeepsTheStoredOne(t *testing.T) {
	f := newFixture(t, true)
	oldPush, oldPull := f.seal(t, pushToken, "push"), f.seal(t, pullToken, "pull")
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/old",
		PushTokenCiphertext: oldPush, PullTokenCiphertext: oldPull, UpdatedAt: t0.Add(-time.Hour),
	})
	var written postgres.UpsertImageRegistrySettingsParams
	f.q.EXPECT().UpsertImageRegistrySettings(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpsertImageRegistrySettingsParams) error { written = p; return nil })

	if _, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if written.PushTokenCiphertext != oldPush || written.PullTokenCiphertext != oldPull || written.Repository != "acme/platform" {
		t.Fatalf("written %+v", written)
	}
	// The stored push token was opened to verify the new repository.
	if p := f.reg.passwords(); len(p) == 0 || p[0] != pushToken {
		t.Fatalf("ping used %v", p)
	}
}

func TestClearPullToken(t *testing.T) {
	f := newFixture(t, true)
	oldPush := f.seal(t, pushToken, "push")
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
		PushTokenCiphertext: oldPush, PullTokenCiphertext: f.seal(t, pullToken, "pull"), UpdatedAt: t0,
	})
	var written postgres.UpsertImageRegistrySettingsParams
	f.q.EXPECT().UpsertImageRegistrySettings(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpsertImageRegistrySettingsParams) error { written = p; return nil })
	view, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", ClearPullToken: true,
	})
	if err != nil || written.PullTokenCiphertext != "" || written.PushTokenCiphertext != oldPush || view.PullTokenSet {
		t.Fatalf("err %v written %+v view %+v", err, written, view)
	}
}

func TestAnotherAccountNeedsANewToken(t *testing.T) {
	f := newFixture(t, true)
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "other", Repository: "other/p",
		PushTokenCiphertext: f.seal(t, pushToken, "push"), UpdatedAt: t0,
	})
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{Provider: "dockerhub", Account: "acme", Repository: "acme/platform"})
	if !errors.Is(err, imageRegistryModel.ErrPushTokenRequired.Err()) {
		t.Fatalf("got %v", err)
	}
}

func TestFirstSaveRequiresAPushToken(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{Provider: "dockerhub", Account: "acme", Repository: "acme/platform"})
	if !errors.Is(err, imageRegistryModel.ErrPushTokenRequired.Err()) {
		t.Fatalf("got %v", err)
	}
}

func TestSaveWithoutCipherIsRefused(t *testing.T) {
	f := newFixture(t, false)
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushToken: pushToken,
	})
	if !errors.Is(err, imageRegistryModel.ErrSecretsKeyMissing.Err()) {
		t.Fatalf("got %v", err)
	}
}

func TestInvalidSettingsAreRefused(t *testing.T) {
	f := newFixture(t, true)
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "quay", Account: "acme", Repository: "acme/platform", PushToken: pushToken,
	})
	if !errors.Is(err, imageRegistryModel.ErrInvalidSettings.Err()) {
		t.Fatalf("got %v", err)
	}
}

func TestFailedPingStoresNothing(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	// No UpsertImageRegistrySettings expectation: a write fails the test.
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushToken: "revoked-token-fixed",
	})
	if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) {
		t.Fatalf("got %v", err)
	}
	ctx := publicReason(t, err)
	if ctx[imageRegistryModel.ContextReason] != string(imageRegistryModel.ReasonUnauthorized) || ctx[imageRegistryModel.ContextToken] != "push" {
		t.Fatalf("public context %v", ctx)
	}
	if strings.Contains(err.Error(), "revoked-token-fixed") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestFailedPullTokenStoresNothing(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushToken: pushToken, PullToken: "bad-pull-fixed",
	})
	if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) || publicReason(t, err)[imageRegistryModel.ContextToken] != "pull" {
		t.Fatalf("got %v", err)
	}
}

func TestUnreachableRegistryReason(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	f.reg.srv.Close()
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushToken: pushToken,
	})
	if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) ||
		publicReason(t, err)[imageRegistryModel.ContextReason] != string(imageRegistryModel.ReasonUnreachable) {
		t.Fatalf("got %v", err)
	}
}

func TestListRepositories(t *testing.T) {
	ctx := context.Background()
	t.Run("given token", func(t *testing.T) {
		f := newFixture(t, true)
		f.adapter.repos = []string{"acme/a", "acme/b"}
		got, err := f.uc.ListImageRegistryRepositories(ctx, ListRepositoriesInput{Provider: "ghcr", Account: "acme", Token: pushToken})
		if err != nil || strings.Join(got, ",") != "acme/a,acme/b" || f.adapter.tokens[0] != pushToken {
			t.Fatalf("got %v %v %v", got, err, f.adapter.tokens)
		}
	})
	t.Run("stored token for the same account", func(t *testing.T) {
		f := newFixture(t, true)
		f.stored(postgres.ImageRegistrySetting{
			ID: true, Provider: "ghcr", Account: "Acme", Repository: "acme/p", PushTokenCiphertext: f.seal(t, pushToken, "push"), UpdatedAt: t0,
		})
		if _, err := f.uc.ListImageRegistryRepositories(ctx, ListRepositoriesInput{Provider: "ghcr", Account: "acme"}); err != nil || f.adapter.tokens[0] != pushToken {
			t.Fatalf("err %v tokens %v", err, f.adapter.tokens)
		}
	})
	t.Run("stored token of another provider is not used", func(t *testing.T) {
		f := newFixture(t, true)
		f.stored(postgres.ImageRegistrySetting{
			ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/p", PushTokenCiphertext: f.seal(t, pushToken, "push"), UpdatedAt: t0,
		})
		_, err := f.uc.ListImageRegistryRepositories(ctx, ListRepositoriesInput{Provider: "ghcr", Account: "acme"})
		if !errors.Is(err, imageRegistryModel.ErrPushTokenRequired.Err()) || len(f.adapter.tokens) != 0 {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid provider", func(t *testing.T) {
		f := newFixture(t, true)
		_, err := f.uc.ListImageRegistryRepositories(ctx, ListRepositoriesInput{Provider: "quay", Account: "acme", Token: pushToken})
		if !errors.Is(err, imageRegistryModel.ErrInvalidRepositoryListing.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("provider refuses", func(t *testing.T) {
		f := newFixture(t, true)
		f.adapter.err = ociregistry.ErrRateLimited
		_, err := f.uc.ListImageRegistryRepositories(ctx, ListRepositoriesInput{Provider: "dockerhub", Account: "acme", Token: pushToken})
		if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) ||
			publicReason(t, err)[imageRegistryModel.ContextReason] != string(imageRegistryModel.ReasonRateLimited) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestClearSettings(t *testing.T) {
	f := newFixture(t, true)
	f.q.EXPECT().DeleteImageRegistrySettings(gomock.Any()).Return(nil)
	if err := f.uc.ClearImageRegistrySettings(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformClient(t *testing.T) {
	ctx := context.Background()
	t.Run("not configured", func(t *testing.T) {
		f := newFixture(t, true)
		f.empty()
		if _, _, err := f.uc.PlatformRegistryClient(ctx, true); !errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no cipher", func(t *testing.T) {
		f := newFixture(t, false)
		if _, _, err := f.uc.PlatformRegistryClient(ctx, true); !errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("push and pull tokens", func(t *testing.T) {
		f := newFixture(t, true)
		f.stored(postgres.ImageRegistrySetting{
			ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
			PushTokenCiphertext: f.seal(t, pushToken, "push"), PullTokenCiphertext: f.seal(t, pullToken, "pull"), UpdatedAt: t0,
		})
		push, s, err := f.uc.PlatformRegistryClient(ctx, true)
		if err != nil || s.Repository != "acme/platform" || s.PushTokenCiphertext == pushToken {
			t.Fatalf("push client: %v %+v", err, s)
		}
		if err := push.Ping(ctx); err != nil {
			t.Fatalf("push ping: %v", err)
		}
		pull, _, err := f.uc.PlatformRegistryClient(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := pull.PingPull(ctx); err != nil {
			t.Fatalf("pull ping: %v", err)
		}
		seen := f.reg.passwords()
		if seen[0] != pushToken || seen[len(seen)-1] != pullToken {
			t.Fatalf("tokens used %v", seen)
		}
	})
	t.Run("pull falls back to the push token", func(t *testing.T) {
		f := newFixture(t, true)
		f.stored(postgres.ImageRegistrySetting{
			ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
			PushTokenCiphertext: f.seal(t, pushToken, "push"), UpdatedAt: t0,
		})
		pull, _, err := f.uc.PlatformRegistryClient(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := pull.PingPull(ctx); err != nil {
			t.Fatalf("pull ping: %v", err)
		}
		if seen := f.reg.passwords(); seen[len(seen)-1] != pushToken {
			t.Fatalf("tokens used %v", seen)
		}
	})
}

func TestViewWithoutCipherReportsTokensUnreadable(t *testing.T) {
	f := newFixture(t, false)
	sealer := testCipher(t)
	ct, _ := sealer.EncryptWithContext([]byte(pushToken), []byte("image-registry:push"))
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushTokenCiphertext: ct, UpdatedAt: t0,
	})
	view, err := f.uc.GetImageRegistrySettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Configured || view.PushTokenSet || view.PullTokenSet || !view.SecretsKeyMissing || view.Repository != "acme/platform" {
		t.Fatalf("view %+v", view)
	}
}

func TestViewWithUnopenableTokenIsNotConfigured(t *testing.T) {
	f := newFixture(t, true)
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
		PushTokenCiphertext: "v1:not-a-valid-ciphertext", PullTokenCiphertext: "v1:also-not-valid", UpdatedAt: t0,
	})
	view, err := f.uc.GetImageRegistrySettings(context.Background())
	if err != nil || view.Configured || view.PushTokenSet || view.PullTokenSet || view.SecretsKeyMissing {
		t.Fatalf("view %+v err %v", view, err)
	}
}

func TestKeptPullTokenIsCheckedAgainstTheRepository(t *testing.T) {
	f := newFixture(t, true)
	// The stored pull token is refused by the registry: keeping it must refuse the save.
	f.reg.allow["stale-pull-fixed"] = false
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/old",
		PushTokenCiphertext: f.seal(t, pushToken, "push"), PullTokenCiphertext: f.seal(t, "stale-pull-fixed", "pull"), UpdatedAt: t0,
	})
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
	})
	if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) || publicReason(t, err)[imageRegistryModel.ContextToken] != "pull" {
		t.Fatalf("got %v", err)
	}
	seen := f.reg.passwords()
	if seen[len(seen)-1] != "stale-pull-fixed" {
		t.Fatalf("kept pull token not checked: %v", seen)
	}
}

func TestPullTokenWithClearIsRefused(t *testing.T) {
	f := newFixture(t, true)
	_, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushToken: pushToken, PullToken: pullToken, ClearPullToken: true,
	})
	if !errors.Is(err, imageRegistryModel.ErrConflictingPullToken.Err()) {
		t.Fatalf("got %v", err)
	}
}
