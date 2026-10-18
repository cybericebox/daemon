package imageRegistryUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// recordingNotifier records the alerts a check raised.
type recordingNotifier struct {
	mu       sync.Mutex
	broken   []inboxUseCase.RegistryCredentialAlert
	restored []inboxUseCase.RegistryCredentialAlert
	closed   []string
}

func (n *recordingNotifier) RegistryCredentialBroken(_ context.Context, a inboxUseCase.RegistryCredentialAlert) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.broken = append(n.broken, a)
	return nil
}

func (n *recordingNotifier) RegistryCredentialRestored(_ context.Context, a inboxUseCase.RegistryCredentialAlert) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.restored = append(n.restored, a)
	return nil
}

func (n *recordingNotifier) RegistryCredentialClosed(_ context.Context, key string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = append(n.closed, key)
	return nil
}

// statusRegistry answers every request with one status, switchable: a registry that is down, throttling or
// refusing every credential.
type statusRegistry struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
}

func newStatusRegistry(t *testing.T, status int) *statusRegistry {
	s := &statusRegistry{status: status}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.WriteHeader(s.status)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *statusRegistry) client(repository, username, password string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{
		Host: strings.TrimPrefix(s.srv.URL, "http://"), Repository: repository,
		Username: username, Password: password, Insecure: true, AllowInternal: true,
	})
}

func mustCheck(t *testing.T, c imageRegistryModel.Check) []byte {
	t.Helper()
	raw, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func parseCheck(t *testing.T, raw []byte) imageRegistryModel.Check {
	t.Helper()
	c, err := imageRegistryModel.ParseCheck(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var (
	okAt0     = imageRegistryModel.Check{State: imageRegistryModel.CheckOK, CheckedAt: t0.Add(-6 * time.Hour), AttemptedAt: t0.Add(-6 * time.Hour)}
	brokenAt0 = imageRegistryModel.Check{State: imageRegistryModel.CheckBroken, CheckedAt: t0.Add(-6 * time.Hour), AttemptedAt: t0.Add(-6 * time.Hour), Reason: imageRegistryModel.ReasonUnauthorized}
)

// platformRow is the stored settings: both tokens set, with the given earlier check results.
func (f *fixture) platformRow(t *testing.T, push, pull imageRegistryModel.Check) postgres.ImageRegistrySetting {
	return postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
		PushTokenCiphertext: f.seal(t, pushToken, "push"), PullTokenCiphertext: f.seal(t, pullToken, "pull"),
		UpdatedAt: t0.Add(-24 * time.Hour), PushCheck: mustCheck(t, push), PullCheck: mustCheck(t, pull),
	}
}

type savedChecks struct {
	params postgres.UpdateImageRegistryChecksParams
	calls  int
}

func (f *fixture) expectChecksSaved(rows int64) *savedChecks {
	saved := &savedChecks{}
	f.q.EXPECT().UpdateImageRegistryChecks(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpdateImageRegistryChecksParams) (int64, error) {
			saved.params, saved.calls = p, saved.calls+1
			return rows, nil
		}).AnyTimes()
	return saved
}

func newCheckFixture(t *testing.T) (*fixture, *recordingNotifier) {
	f := newFixture(t, true)
	n := &recordingNotifier{}
	f.uc.SetCheckNotifier(n)
	return f, n
}

func TestCheckPlatformWorkingRecordsOKSilently(t *testing.T) {
	f, n := newCheckFixture(t)
	f.stored(f.platformRow(t, imageRegistryModel.Check{}, imageRegistryModel.Check{}))
	saved := f.expectChecksSaved(1)

	view, err := f.uc.CheckPlatformRegistry(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	push, pull := parseCheck(t, saved.params.PushCheck), parseCheck(t, saved.params.PullCheck)
	want := imageRegistryModel.Check{State: imageRegistryModel.CheckOK, CheckedAt: t0, AttemptedAt: t0}
	if push != want || pull != want {
		t.Fatalf("push %+v pull %+v", push, pull)
	}
	// the result is written for the settings as they were read
	if !saved.params.UpdatedAt.Equal(t0.Add(-24 * time.Hour)) {
		t.Fatalf("guard %v", saved.params.UpdatedAt)
	}
	if len(n.broken)+len(n.restored) != 0 {
		t.Fatalf("a first ok must be silent: %+v", n)
	}
	if view.PushCheck.State != "ok" || view.PullCheck.State != "ok" || view.PushCheck.CheckedAt == nil || !view.PushCheck.CheckedAt.Equal(t0) {
		t.Fatalf("view %+v", view)
	}
}

func TestCheckPlatformRefusedTokenBreaksOnceAndNamesNoToken(t *testing.T) {
	f, n := newCheckFixture(t)
	f.reg.allow[pushToken] = false // revoked
	f.stored(f.platformRow(t, okAt0, okAt0))
	saved := f.expectChecksSaved(1)

	if _, err := f.uc.CheckPlatformRegistry(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	push, pull := parseCheck(t, saved.params.PushCheck), parseCheck(t, saved.params.PullCheck)
	if push.State != imageRegistryModel.CheckBroken || push.Reason != imageRegistryModel.ReasonUnauthorized || !push.CheckedAt.Equal(t0) {
		t.Fatalf("push %+v", push)
	}
	if pull.State != imageRegistryModel.CheckOK {
		t.Fatalf("pull %+v", pull)
	}
	if len(n.broken) != 1 || len(n.restored) != 0 {
		t.Fatalf("alerts %+v", n)
	}
	a := n.broken[0]
	if a.Key != "platform_push" || a.Kind != "platform_push" || a.EventID != uuid.Nil || a.Reason != "unauthorized" || a.Label != "docker.io/acme/platform" {
		t.Fatalf("alert %+v", a)
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), pushToken) || strings.Contains(string(raw), pullToken) {
		t.Fatalf("alert carries a token: %s", raw)
	}

	// still broken on the next pass: no second signal
	f2, n2 := newCheckFixture(t)
	f2.reg.allow[pushToken] = false
	f2.stored(f2.platformRow(t, brokenAt0, okAt0))
	f2.expectChecksSaved(1)
	if _, err := f2.uc.CheckPlatformRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n2.broken)+len(n2.restored) != 0 {
		t.Fatalf("a repeat must be silent: %+v", n2)
	}
}

func TestCheckPlatformRestoredSendsOneRestoredSignal(t *testing.T) {
	f, n := newCheckFixture(t)
	f.stored(f.platformRow(t, brokenAt0, imageRegistryModel.Check{}))
	saved := f.expectChecksSaved(1)

	if _, err := f.uc.CheckPlatformRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := parseCheck(t, saved.params.PushCheck); got.State != imageRegistryModel.CheckOK || got.Reason != "" {
		t.Fatalf("push %+v", got)
	}
	if len(n.restored) != 1 || n.restored[0].Key != "platform_push" || len(n.broken) != 0 {
		t.Fatalf("alerts %+v", n)
	}
}

func TestCheckPlatformProviderTroubleKeepsThePreviousState(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		failure imageRegistryModel.Reason
	}{
		"server error": {http.StatusBadGateway, imageRegistryModel.ReasonUnreachable},
		"throttled":    {http.StatusTooManyRequests, imageRegistryModel.ReasonRateLimited},
	} {
		for _, prev := range []imageRegistryModel.Check{okAt0, brokenAt0} {
			t.Run(name+"/"+string(prev.State), func(t *testing.T) {
				f, n := newCheckFixture(t)
				down := newStatusRegistry(t, tc.status)
				f.uc.newClient = func(_ imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client {
					return down.client(repository, username, password)
				}
				f.stored(f.platformRow(t, prev, prev))
				saved := f.expectChecksSaved(1)

				if _, err := f.uc.CheckPlatformRegistry(context.Background()); err != nil {
					t.Fatalf("a registry in trouble is not an error of the call: %v", err)
				}
				for _, raw := range [][]byte{saved.params.PushCheck, saved.params.PullCheck} {
					got := parseCheck(t, raw)
					if got.State != prev.State || !got.CheckedAt.Equal(prev.CheckedAt) || got.Failure != tc.failure || !got.AttemptedAt.Equal(t0) {
						t.Fatalf("%+v", got)
					}
				}
				if len(n.broken)+len(n.restored) != 0 {
					t.Fatalf("no signal for a failed attempt: %+v", n)
				}
			})
		}
	}
}

func TestCheckPlatformForbiddenCountsAsRefused(t *testing.T) {
	f, n := newCheckFixture(t)
	forbidden := newStatusRegistry(t, http.StatusForbidden)
	f.uc.newClient = func(_ imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client {
		return forbidden.client(repository, username, password)
	}
	f.stored(f.platformRow(t, okAt0, okAt0))
	f.expectChecksSaved(1)
	if _, err := f.uc.CheckPlatformRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.broken) != 2 {
		t.Fatalf("both tokens refused: %+v", n.broken)
	}
}

func TestCheckPlatformWithoutPullTokenChecksPushOnly(t *testing.T) {
	f, n := newCheckFixture(t)
	row := f.platformRow(t, okAt0, brokenAt0)
	row.PullTokenCiphertext = ""
	f.stored(row)
	saved := f.expectChecksSaved(1)

	if _, err := f.uc.CheckPlatformRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := parseCheck(t, saved.params.PullCheck); got != (imageRegistryModel.Check{}) {
		t.Fatalf("pull check must be cleared without a pull token: %+v", got)
	}
	if len(n.restored) != 0 {
		t.Fatalf("a removed pull token is not a restore: %+v", n)
	}
}

func TestCheckPlatformDropsAResultForReplacedSettings(t *testing.T) {
	f, n := newCheckFixture(t)
	f.reg.allow[pushToken] = false
	f.stored(f.platformRow(t, okAt0, okAt0))
	f.expectChecksSaved(0) // settings were saved again while the check ran

	if _, err := f.uc.CheckPlatformRegistry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.broken) != 0 {
		t.Fatalf("a dropped result must not alert: %+v", n)
	}
}

func TestCheckPlatformNotConfigured(t *testing.T) {
	f, _ := newCheckFixture(t)
	f.empty()
	_, err := f.uc.CheckPlatformRegistry(context.Background())
	if !errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
		t.Fatalf("err %v", err)
	}
}

// ---- organizer secrets ----

func (f *fixture) secretRow(t *testing.T, check imageRegistryModel.Check) postgres.EventRegistrySecret {
	row := f.row(t, secID, "registry.example.org", "acme", secretToken)
	row.CredentialCheck = mustCheck(t, check)
	return row
}

func (f *fixture) expectEventLabel() {
	f.q.EXPECT().GetEventByID(gomock.Any(), eventA).Return(postgres.Event{ID: eventA, Tag: "ctf-2026", Name: "Spring CTF"}, nil).AnyTimes()
}

func TestCheckEventSecretRefusedBreaksAndTellsTheEventsManagers(t *testing.T) {
	f, n := newCheckFixture(t)
	revoked := newStatusRegistry(t, http.StatusUnauthorized)
	var asked struct{ host, repo, user, token string }
	f.uc.external = func(host, repository, username, password string) *ociregistry.Client {
		asked.host, asked.repo, asked.user, asked.token = host, repository, username, password
		return revoked.client(repository, username, password)
	}
	f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), postgres.GetEventRegistrySecretParams{ID: secID, EventID: eventA}).
		Return(f.secretRow(t, okAt0), nil)
	f.expectEventLabel()
	var saved postgres.UpdateEventRegistrySecretCheckParams
	f.q.EXPECT().UpdateEventRegistrySecretCheck(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpdateEventRegistrySecretCheckParams) (int64, error) {
			saved = p
			return 1, nil
		})

	view, err := f.uc.CheckEventRegistrySecret(context.Background(), eventA, secID)
	if err != nil {
		t.Fatal(err)
	}
	if asked.host != "registry.example.org" || asked.user != "bob" || asked.token != secretToken {
		t.Fatalf("asked %+v", asked)
	}
	got := parseCheck(t, saved.CredentialCheck)
	if got.State != imageRegistryModel.CheckBroken || got.Reason != imageRegistryModel.ReasonUnauthorized || !got.CheckedAt.Equal(t0) {
		t.Fatalf("saved %+v", got)
	}
	if saved.ID != secID || !saved.UpdatedAt.Equal(t0) {
		t.Fatalf("guard %+v", saved)
	}
	if view.Check.State != "broken" || view.Check.Reason != "unauthorized" {
		t.Fatalf("view %+v", view.Check)
	}
	if len(n.broken) != 1 {
		t.Fatalf("alerts %+v", n)
	}
	a := n.broken[0]
	if a.Key != "event_secret:"+secID.String() || a.Kind != "event_secret" || a.EventID != eventA || a.EventTag != "ctf-2026" ||
		a.EventName != "Spring CTF" || a.Label != "registry.example.org/acme" {
		t.Fatalf("alert %+v", a)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), secretToken) {
		t.Fatalf("view carries the token: %s", raw)
	}
}

func TestCheckEventSecretRestoredAndTrouble(t *testing.T) {
	t.Run("restored", func(t *testing.T) {
		f, n := newCheckFixture(t)
		ok := newStatusRegistry(t, http.StatusOK)
		f.uc.external = func(_, repository, username, password string) *ociregistry.Client {
			return ok.client(repository, username, password)
		}
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.secretRow(t, brokenAt0), nil)
		f.expectEventLabel()
		f.q.EXPECT().UpdateEventRegistrySecretCheck(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		if _, err := f.uc.CheckEventRegistrySecret(context.Background(), eventA, secID); err != nil {
			t.Fatal(err)
		}
		if len(n.restored) != 1 || n.restored[0].Key != "event_secret:"+secID.String() {
			t.Fatalf("alerts %+v", n)
		}
	})
	t.Run("provider trouble keeps broken and says nothing", func(t *testing.T) {
		f, n := newCheckFixture(t)
		down := newStatusRegistry(t, http.StatusServiceUnavailable)
		f.uc.external = func(_, repository, username, password string) *ociregistry.Client {
			return down.client(repository, username, password)
		}
		f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(f.secretRow(t, brokenAt0), nil)
		f.expectEventLabel()
		var saved postgres.UpdateEventRegistrySecretCheckParams
		f.q.EXPECT().UpdateEventRegistrySecretCheck(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, p postgres.UpdateEventRegistrySecretCheckParams) (int64, error) {
				saved = p
				return 1, nil
			})
		view, err := f.uc.CheckEventRegistrySecret(context.Background(), eventA, secID)
		if err != nil {
			t.Fatal(err)
		}
		got := parseCheck(t, saved.CredentialCheck)
		if got.State != imageRegistryModel.CheckBroken || got.Failure != imageRegistryModel.ReasonUnreachable || view.Check.Failure != "unreachable" {
			t.Fatalf("saved %+v view %+v", got, view.Check)
		}
		if len(n.broken)+len(n.restored) != 0 {
			t.Fatalf("alerts %+v", n)
		}
	})
}

func TestCheckEventSecretOfAnotherEventIsNotFound(t *testing.T) {
	f, _ := newCheckFixture(t)
	f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), postgres.GetEventRegistrySecretParams{ID: secID, EventID: eventB}).
		Return(postgres.EventRegistrySecret{}, pgx.ErrNoRows)
	_, err := f.uc.CheckEventRegistrySecret(context.Background(), eventB, secID)
	if !errors.Is(err, imageRegistryModel.ErrEventSecretNotFound.Err()) {
		t.Fatalf("err %v", err)
	}
}

func TestCheckEventSecretWithUnreadableTokenIsAFailedAttempt(t *testing.T) {
	f, n := newCheckFixture(t)
	row := f.secretRow(t, okAt0)
	row.TokenCiphertext = "not-a-ciphertext"
	f.q.EXPECT().GetEventRegistrySecret(gomock.Any(), gomock.Any()).Return(row, nil)
	f.expectEventLabel()
	var saved postgres.UpdateEventRegistrySecretCheckParams
	f.q.EXPECT().UpdateEventRegistrySecretCheck(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpdateEventRegistrySecretCheckParams) (int64, error) {
			saved = p
			return 1, nil
		})
	if _, err := f.uc.CheckEventRegistrySecret(context.Background(), eventA, secID); err != nil {
		t.Fatal(err)
	}
	if got := parseCheck(t, saved.CredentialCheck); got.State != imageRegistryModel.CheckOK || got.Failure != imageRegistryModel.ReasonNotConfigured {
		t.Fatalf("saved %+v", got)
	}
	if len(n.broken) != 0 {
		t.Fatal("an unreadable token is not an auth refusal")
	}
}

// ---- the periodic pass ----

func TestRunRegistryChecksCoversThePlatformAndEverySecretOfLiveEvents(t *testing.T) {
	f, n := newCheckFixture(t)
	f.reg.allow[pullToken] = false
	f.stored(f.platformRow(t, okAt0, okAt0))
	f.expectChecksSaved(1)

	bad := f.secretRow(t, okAt0)
	other := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e2"))
	good := f.row(t, other, "ghcr.io", "", secretToken)
	good.EventID = eventB
	good.CredentialCheck = mustCheck(t, okAt0)
	f.q.EXPECT().ListEventRegistrySecretsToCheck(gomock.Any(), t0).Return([]postgres.ListEventRegistrySecretsToCheckRow{
		{ID: bad.ID, EventID: eventA, Host: bad.Host, RepositoryPrefix: bad.RepositoryPrefix, Username: bad.Username, TokenCiphertext: bad.TokenCiphertext,
			CreatedAt: bad.CreatedAt, UpdatedAt: bad.UpdatedAt, CredentialCheck: bad.CredentialCheck, EventTag: "ctf-a", EventName: "A"},
		{ID: good.ID, EventID: eventB, Host: good.Host, RepositoryPrefix: good.RepositoryPrefix, Username: good.Username, TokenCiphertext: good.TokenCiphertext,
			CreatedAt: good.CreatedAt, UpdatedAt: good.UpdatedAt, CredentialCheck: good.CredentialCheck, EventTag: "ctf-b", EventName: "B"},
	}, nil)
	f.uc.external = func(host, repository, username, password string) *ociregistry.Client {
		status := http.StatusOK
		if host == "registry.example.org" {
			status = http.StatusUnauthorized
		}
		return newStatusRegistry(t, status).client(repository, username, password)
	}
	var saves []postgres.UpdateEventRegistrySecretCheckParams
	f.q.EXPECT().UpdateEventRegistrySecretCheck(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpdateEventRegistrySecretCheckParams) (int64, error) {
			saves = append(saves, p)
			return 1, nil
		}).Times(2)

	if err := f.uc.RunRegistryChecks(context.Background()); err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, a := range n.broken {
		keys[a.Key] = a.EventTag
	}
	if len(n.broken) != 2 || keys["platform_pull"] != "" || keys["event_secret:"+bad.ID.String()] != "ctf-a" || len(saves) != 2 {
		t.Fatalf("alerts %+v saves %d", n.broken, len(saves))
	}
}

func TestRunRegistryChecksOneFailingSecretDoesNotStopTheRest(t *testing.T) {
	f, _ := newCheckFixture(t)
	f.empty() // no platform registry configured: skipped
	a, b := f.secretRow(t, okAt0), f.secretRow(t, okAt0)
	b.ID = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e3"))
	b.TokenCiphertext = f.sealSecret(t, b.ID, secretToken)
	toRow := func(r postgres.EventRegistrySecret) postgres.ListEventRegistrySecretsToCheckRow {
		return postgres.ListEventRegistrySecretsToCheckRow{ID: r.ID, EventID: r.EventID, Host: r.Host, Username: r.Username,
			TokenCiphertext: r.TokenCiphertext, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CredentialCheck: r.CredentialCheck}
	}
	f.q.EXPECT().ListEventRegistrySecretsToCheck(gomock.Any(), t0).Return([]postgres.ListEventRegistrySecretsToCheckRow{toRow(a), toRow(b)}, nil)
	ok := newStatusRegistry(t, http.StatusOK)
	f.uc.external = func(_, repository, username, password string) *ociregistry.Client {
		return ok.client(repository, username, password)
	}
	calls := 0
	f.q.EXPECT().UpdateEventRegistrySecretCheck(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, postgres.UpdateEventRegistrySecretCheckParams) (int64, error) {
			calls++
			if calls == 1 {
				return 0, errors.New("db down")
			}
			return 1, nil
		}).Times(2)
	if err := f.uc.RunRegistryChecks(context.Background()); err != nil {
		t.Fatalf("a failing item is logged, not raised: %v", err)
	}
}

func TestRunRegistryChecksWithoutCipherDoesNothing(t *testing.T) {
	f := newFixture(t, false)
	f.empty()
	f.q.EXPECT().ListEventRegistrySecretsToCheck(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	if err := f.uc.RunRegistryChecks(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// ---- saving verifies, deleting closes ----

func TestSaveSettingsRecordsTheVerifiedTokensAndRestoresABrokenOne(t *testing.T) {
	f, n := newCheckFixture(t)
	f.stored(f.platformRow(t, brokenAt0, imageRegistryModel.Check{}))
	var written postgres.UpsertImageRegistrySettingsParams
	f.q.EXPECT().UpsertImageRegistrySettings(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpsertImageRegistrySettingsParams) error { written = p; return nil })

	if _, err := f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{
		Provider: "dockerhub", Account: "acme", Repository: "acme/platform", PushToken: pushToken, PullToken: pullToken,
	}); err != nil {
		t.Fatal(err)
	}
	want := imageRegistryModel.Check{State: imageRegistryModel.CheckOK, CheckedAt: t0, AttemptedAt: t0}
	if parseCheck(t, written.PushCheck) != want || parseCheck(t, written.PullCheck) != want {
		t.Fatalf("written %s / %s", written.PushCheck, written.PullCheck)
	}
	if len(n.restored) != 1 || n.restored[0].Key != "platform_push" || len(n.broken) != 0 {
		t.Fatalf("alerts %+v", n)
	}
}

func TestClearingTheSettingsClosesTheOpenAlerts(t *testing.T) {
	f, n := newCheckFixture(t)
	f.q.EXPECT().DeleteImageRegistrySettings(gomock.Any()).Return(nil)
	if err := f.uc.ClearImageRegistrySettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(n.closed, ",") != "platform_push,platform_pull" {
		t.Fatalf("closed %v", n.closed)
	}
}

func TestDeletingASecretClosesItsAlert(t *testing.T) {
	f, n := newCheckFixture(t)
	f.q.EXPECT().DeleteEventRegistrySecret(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := f.uc.DeleteEventRegistrySecret(context.Background(), eventA, secID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(n.closed, ",") != "event_secret:"+secID.String() {
		t.Fatalf("closed %v", n.closed)
	}
}
