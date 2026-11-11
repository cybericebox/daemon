package imageRegistryUseCase

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

type memoryConfigs struct {
	rows map[string]exerciseModel.ImageService
}

func (m *memoryConfigs) Save(_ context.Context, digest string, s exerciseModel.ImageService, _ time.Time) error {
	if m.rows == nil {
		m.rows = map[string]exerciseModel.ImageService{}
	}
	m.rows[digest] = s
	return nil
}

func (m *memoryConfigs) Get(_ context.Context, digests []string) (map[string]exerciseModel.ImageService, error) {
	out := map[string]exerciseModel.ImageService{}
	for _, d := range digests {
		if s, ok := m.rows[d]; ok {
			out[d] = s
		}
	}
	return out, nil
}

// The start configuration read from the config blob when an image is published is what a lab built from it is told to run.
func TestPlatformImageStartConfigurationReachesTheDeployTopology(t *testing.T) {
	configs := &memoryConfigs{}
	f := newAccessFixture(t, true, func(d *Dependencies) { d.Configs = configs })
	ref, digest := f.platformImage(t)

	// What the published image's config blob says: the entrypoint, cmd, user and working directory of its "config" object.
	body := []byte(`{"architecture":"amd64","os":"linux","config":{"User":"www","Entrypoint":["/entry.sh"],"Cmd":["serve","--port=80"],"WorkingDir":"/srv","Env":["A=1"]},"rootfs":{"type":"layers","diff_ids":[]}}`)
	f.uc.keepServiceConfig(context.Background(), digest, body)
	require.Equal(t, exerciseModel.ImageService{
		Entrypoint: []string{"/entry.sh"}, Cmd: []string{"serve", "--port=80"}, User: "www", WorkingDir: "/srv",
	}, configs.rows[digest])

	got, err := f.uc.PrepareDeployImages(context.Background(), eventScope(eventA), topo(ref))
	require.NoError(t, err)
	require.NotNil(t, got.Topology.Devices[0].ImageService)
	require.Equal(t, "www", got.Topology.Devices[0].ImageService.User)
	require.Equal(t, []string{"/entry.sh"}, got.Topology.Devices[0].ImageService.Entrypoint)
	require.Nil(t, topo(ref).Devices[0].ImageService, "the stored topology carries none")
}

// An image by reference to an external registry has no stored configuration: nothing reads it, so the device carries none.
func TestExternalImageHasNoStartConfiguration(t *testing.T) {
	configs := &memoryConfigs{}
	f := newAccessFixture(t, true, func(d *Dependencies) { d.Configs = configs })
	configs.rows = map[string]exerciseModel.ImageService{"sha256:" + strings.Repeat("a", 64): {User: "x"}}
	original := topo("docker.io/acme/app:1", "platform:web@sha256:"+strings.Repeat("a", 64))
	out := original
	out.Devices = slices.Clone(original.Devices)
	f.uc.fillImageServices(context.Background(), original, &out)
	require.Nil(t, out.Devices[0].ImageService, "nothing reads the configuration of an external image")
	require.NotNil(t, out.Devices[1].ImageService, "a platform image has its stored one")
}

func TestKeepServiceConfigIgnoresAnUnreadableBlob(t *testing.T) {
	configs := &memoryConfigs{}
	f := newAccessFixture(t, true, func(d *Dependencies) { d.Configs = configs })
	f.uc.keepServiceConfig(context.Background(), "sha256:x", []byte("not json"))
	require.Empty(t, configs.rows)
}
