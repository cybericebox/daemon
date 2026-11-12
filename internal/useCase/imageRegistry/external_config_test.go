package imageRegistryUseCase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	imageRegistryRepo "github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

type storedExternal struct{ reference, scope string }

type memoryExternalConfigs struct {
	rows map[storedExternal]imageRegistryRepo.ExternalImageConfig
	err  error
}

func (m *memoryExternalConfigs) Save(_ context.Context, reference, scope, digest string, service exerciseModel.ImageService, now time.Time) error {
	if m.err != nil {
		return m.err
	}
	if m.rows == nil {
		m.rows = map[storedExternal]imageRegistryRepo.ExternalImageConfig{}
	}
	m.rows[storedExternal{reference, scope}] = imageRegistryRepo.ExternalImageConfig{Digest: digest, Service: service, ResolvedAt: now}
	return nil
}

func (m *memoryExternalConfigs) Get(_ context.Context, references, scopes []string) (map[string]imageRegistryRepo.ExternalImageConfig, error) {
	out := map[string]imageRegistryRepo.ExternalImageConfig{}
	for _, scope := range scopes {
		for _, reference := range references {
			if row, ok := m.rows[storedExternal{reference, scope}]; ok {
				if _, taken := out[reference]; !taken {
					out[reference] = row
				}
			}
		}
	}
	return out, nil
}

func configJSON(os, arch, user string) []byte {
	return []byte(fmt.Sprintf(`{"architecture":%q,"os":%q,"config":{"User":%q,"Entrypoint":["/app"],"Cmd":["--serve"],"WorkingDir":"/srv"},"rootfs":{"type":"layers","diff_ids":[]}}`, arch, os, user))
}

// pushConfigImage pushes an image with the given config and returns its manifest digest.
func pushConfigImage(t *testing.T, c *ociregistry.Client, config []byte, tag string) string {
	t.Helper()
	sum := sha256.Sum256(config)
	configDigest := "sha256:" + hex.EncodeToString(sum[:])
	require.NoError(t, c.PushBlob(context.Background(), configDigest, int64(len(config)), bytes.NewReader(config)))
	manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`, configDigest, len(config))
	digest, err := c.PutManifest(context.Background(), tag, "application/vnd.oci.image.manifest.v1+json", []byte(manifest))
	require.NoError(t, err)
	return digest
}

func publicClient(host, repository, user, pass string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{Host: host, Repository: repository, Username: user, Password: pass, Insecure: true, AllowInternal: true})
}

type externalFixture struct {
	*accessFixture
	store *memoryExternalConfigs
	hub   *testRegistry
}

func newExternalFixture(t *testing.T, registryUser string) *externalFixture {
	store := &memoryExternalConfigs{}
	f := newAccessFixture(t, true, func(d *Dependencies) { d.ExternalConfigs = store })
	hub := newTestRegistry(t, registryUser, secretToken)
	f.hosts["registry.example"] = hub.host()
	return &externalFixture{accessFixture: f, store: store, hub: hub}
}

// An external image is read from its registry (manifest and config blob, no layer) the first time a publish asks, stored by
// reference, and then known to the deploy without the registry.
func TestResolveImageServicesReadsAndStoresAnExternalImage(t *testing.T) {
	ctx := context.Background()
	f := newExternalFixture(t, "")
	digest := pushConfigImage(t, publicClient(f.hub.host(), "acme/app", "", ""), configJSON("linux", "amd64", "1000:1000"), "1")

	infos := f.uc.ResolveImageServices(ctx, Scope{}, []string{"registry.example/acme/app:1"})
	require.Len(t, infos, 1)
	require.True(t, infos[0].Known, "reason %q", infos[0].Reason)
	require.Equal(t, "1000:1000", infos[0].Service.User)
	require.Equal(t, []string{"/app"}, infos[0].Service.Entrypoint)
	require.Equal(t, "/srv", infos[0].Service.WorkingDir)
	row, ok := f.store.rows[storedExternal{"registry.example/acme/app:1", ""}]
	require.True(t, ok, "stored by the normalized reference, in the public scope")
	require.Equal(t, digest, row.Digest, "with the digest the reference had")

	// The registry is gone: the stored configuration serves the next publish and the deploy.
	f.hub.srv.Close()
	infos = f.uc.ResolveImageServices(ctx, Scope{}, []string{"registry.example/acme/app:1"})
	require.True(t, infos[0].Known)
	original := topo("registry.example/acme/app:1")
	out := original
	out.Devices = append(out.Devices[:0:0], original.Devices...)
	f.uc.fillImageServices(ctx, Scope{}, original, &out)
	require.NotNil(t, out.Devices[0].ImageService)
	require.Equal(t, "1000:1000", out.Devices[0].ImageService.User)
	user, known := out.Devices[0].ResolveServiceUser(out.Devices[0].ImageService)
	require.True(t, known)
	require.Equal(t, "1000:1000", user)
}

// An image with both linux platforms: the same user is fine, a different one is refused, not guessed; neither is stored then.
func TestResolveImageServicesOfAnIndex(t *testing.T) {
	ctx := context.Background()
	index := func(t *testing.T, f *externalFixture, repo, amdUser, armUser string) {
		c := publicClient(f.hub.host(), repo, "", "")
		amd := pushConfigImage(t, c, configJSON("linux", "amd64", amdUser), "amd")
		arm := pushConfigImage(t, c, configJSON("linux", "arm64", armUser), "arm")
		body := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[`+
			`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":10,"platform":{"os":"linux","architecture":"amd64"}},`+
			`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":10,"platform":{"os":"linux","architecture":"arm64"}}]}`, amd, arm)
		_, err := c.PutManifest(ctx, "1", "application/vnd.oci.image.index.v1+json", []byte(body))
		require.NoError(t, err)
	}
	f := newExternalFixture(t, "")
	index(t, f, "acme/same", "app", "app")
	index(t, f, "acme/root", "", "root")
	index(t, f, "acme/differ", "app", "1000")

	for image, want := range map[string]struct {
		known  bool
		reason string
		user   string
	}{
		"registry.example/acme/same:1":   {true, "", "app"},
		"registry.example/acme/root:1":   {true, "", ""}, // "" and "root" are the same account
		"registry.example/acme/differ:1": {false, ServicePlatformDependent, ""},
	} {
		got := f.uc.ResolveImageServices(ctx, Scope{}, []string{image})[0]
		require.Equal(t, want.known, got.Known, image)
		require.Equal(t, want.reason, got.Reason, image)
		if want.known {
			require.Equal(t, want.user, got.Service.User, image)
		}
	}
	require.NotContains(t, f.store.rows, storedExternal{"registry.example/acme/differ:1", ""}, "nothing is stored for a user that depends on the platform")
}

// What the registry does with a request, and what it holds, decides the reason; none of them stores anything.
func TestResolveImageServicesReasons(t *testing.T) {
	ctx := context.Background()
	f := newExternalFixture(t, "")
	c := publicClient(f.hub.host(), "acme/app", "", "")
	pushConfigImage(t, c, configJSON("linux", "amd64", "app"), "1")
	big := append([]byte(`{"architecture":"amd64","os":"linux","pad":"`), bytes.Repeat([]byte("x"), ociregistry.MaxConfigBytes)...)
	pushConfigImage(t, publicClient(f.hub.host(), "acme/big", "", ""), append(big, []byte(`"}`)...), "1")

	for image, want := range map[string]string{
		"registry.example/acme/missing:1": ServiceNotFound,
		"registry.example/acme/big:1":     ServiceUnsupported,
		"not a reference":                 ServiceUnsupported,
	} {
		got := f.uc.ResolveImageServices(ctx, Scope{}, []string{image})[0]
		require.False(t, got.Known, image)
		require.Equal(t, want, got.Reason, image)
	}

	f.hub.mu.Lock()
	f.hub.status = 503
	f.hub.mu.Unlock()
	got := f.uc.ResolveImageServices(ctx, Scope{}, []string{"registry.example/acme/app:2"})[0]
	require.False(t, got.Known)
	require.Equal(t, ServiceUnreachable, got.Reason)
	require.Empty(t, f.store.rows)

	// A platform image the platform never read is not something the registry can tell.
	got = f.uc.ResolveImageServices(ctx, Scope{}, []string{"platform:old@sha256:" + strings.Repeat("a", 64)})[0]
	require.False(t, got.Known)
	require.Equal(t, ServiceNotRead, got.Reason)
}

// A private image: refused without a credential, read with the event's registry secret and then kept for that event only.
func TestResolveImageServicesOfAPrivateImage(t *testing.T) {
	ctx := context.Background()
	f := newExternalFixture(t, "bob")
	pushConfigImage(t, publicClient(f.hub.host(), "acme/app", "bob", secretToken), configJSON("linux", "amd64", "deploy"), "1")
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
		f.row(t, secID, "registry.example", "acme", secretToken),
	}, nil).AnyTimes()
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventB).Return(nil, nil).AnyTimes()
	image := "registry.example/acme/app:1"

	got := f.uc.ResolveImageServices(ctx, Scope{}, []string{image})[0]
	require.False(t, got.Known)
	require.Equal(t, ServiceUnauthorized, got.Reason, "the catalog has no credential for it")

	got = f.uc.ResolveImageServices(ctx, eventScope(eventA), []string{image})[0]
	require.True(t, got.Known, "reason %q", got.Reason)
	require.Equal(t, "deploy", got.Service.User)
	require.Contains(t, f.store.rows, storedExternal{"registry.example/acme/app:1", eventA.String()})
	require.NotContains(t, f.store.rows, storedExternal{"registry.example/acme/app:1", ""}, "a private image's configuration is not public")

	// Another scope does not get it from the store.
	other := f.uc.ResolveImageServices(ctx, eventScope(eventB), []string{image})[0]
	require.False(t, other.Known)
	require.False(t, f.uc.ImageServices(ctx, []string{image})[0].Known)
}

// The reference is the author's: a registry on a loopback address is never contacted by the default (guarded) client.
func TestExternalConfigsDoNotDialInternalAddresses(t *testing.T) {
	ctx := context.Background()
	store := &memoryExternalConfigs{}
	f := newAccessFixture(t, true, func(d *Dependencies) {
		d.ExternalConfigs = store
		d.NewExternalClient = DefaultExternalClient
	})
	loopback := newTestRegistry(t, "", "")
	pushConfigImage(t, publicClient(loopback.host(), "acme/app", "", ""), configJSON("linux", "amd64", "app"), "1")

	got := f.uc.ResolveImageServices(ctx, Scope{}, []string{loopback.host() + "/acme/app:1"})[0]
	require.False(t, got.Known)
	require.Equal(t, ServiceUnsupported, got.Reason, "plain http is refused before any request")
	require.Empty(t, store.rows)
	loopback.mu.Lock()
	defer loopback.mu.Unlock()
	require.Empty(t, loopback.seen)
}

// A configuration that cannot be stored cannot serve a deploy: the image stays unknown.
func TestResolveImageServicesWithoutAStore(t *testing.T) {
	ctx := context.Background()
	f := newExternalFixture(t, "")
	pushConfigImage(t, publicClient(f.hub.host(), "acme/app", "", ""), configJSON("linux", "amd64", "app"), "1")
	f.store.err = fmt.Errorf("database down")
	got := f.uc.ResolveImageServices(ctx, Scope{}, []string{"registry.example/acme/app:1"})[0]
	require.False(t, got.Known)
	require.Equal(t, ServiceNotRead, got.Reason)

	bare := newAccessFixture(t, true)
	require.Equal(t, ServiceNotRead, bare.uc.ResolveImageServices(ctx, Scope{}, []string{"registry.example/acme/app:1"})[0].Reason)
}
