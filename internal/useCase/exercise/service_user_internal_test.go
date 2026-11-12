package exercise

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

// serviceImages is an image registry that knows the start configuration of some images only; the others are unknown with
// the reason it gives.
type serviceImages struct {
	IImageAccess
	known  map[string]bool
	reason map[string]string
	scope  *imageRegistryUseCase.Scope
}

func (s serviceImages) ExerciseImageScope(context.Context, uuid.UUID) (imageRegistryUseCase.Scope, error) {
	return *s.scope, nil
}

func (s serviceImages) ResolveImageServices(_ context.Context, _ imageRegistryUseCase.Scope, images []string) []imageRegistryUseCase.ImageServiceInfo {
	out := make([]imageRegistryUseCase.ImageServiceInfo, 0, len(images))
	for _, image := range images {
		out = append(out, imageRegistryUseCase.ImageServiceInfo{Image: image, Known: s.known[image], Reason: s.reason[image]})
	}
	return out
}

// A device started through the launcher (a bootstrap list, a command or arguments) is published only on an image whose
// start configuration the platform knows: the error names the variant (its position) and the device, field "image". A
// device that needs no launcher, and a draft, are not checked.
func TestRequireServiceUsersKnown(t *testing.T) {
	ctx := context.Background()
	platform, external := "platform:web@sha256:"+repeat("a"), "docker.io/acme/web:1"
	node := func(name, image string, mutate func(*exerciseModel.Device)) exerciseModel.Device {
		d := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: exerciseModel.DeviceTypeNode, Image: image}
		mutate(&d)
		return d
	}
	withScript := func(d *exerciseModel.Device) { d.Bootstrap.Script = "true" }
	withCommand := func(d *exerciseModel.Device) { d.Command = []string{"/app"} }
	withArgs := func(d *exerciseModel.Device) { d.Args = []string{"-v"} }
	withEnv := func(d *exerciseModel.Device) { d.Bootstrap.Env = []exerciseModel.EnvVar{{Name: "A", Value: "1"}} }
	plain := func(*exerciseModel.Device) {}
	variantOfDevices := func(devices ...exerciseModel.Device) exerciseModel.Variant {
		return exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: devices}}
	}
	exerciseID := uuid.Must(uuid.NewV7())
	u := &ExerciseUseCase{images: serviceImages{
		known:  map[string]bool{platform: true, "docker.io/acme/read:1": true},
		reason: map[string]string{external: imageRegistryUseCase.ServiceUnreachable, "private.example/x:1": imageRegistryUseCase.ServiceUnauthorized},
		scope:  &imageRegistryUseCase.Scope{},
	}}

	require.NoError(t, u.requireServiceUsersKnown(ctx, exerciseID, []exerciseModel.Variant{
		variantOfDevices(node("a", platform, withScript), node("b", platform, withCommand), node("c", platform, withArgs)),
		variantOfDevices(node("d", external, plain), node("e", external, withEnv), node("f", "", withScript)),
	}), "known images, devices that need no launcher, no image at all")

	for name, tc := range map[string]struct {
		variants []exerciseModel.Variant
		variant  int
		device   string
		reason   string
	}{
		"external image with a script": {[]exerciseModel.Variant{variantOfDevices(node("x", external, withScript))}, 1, "x", "unreachable"},
		"private image with a command": {[]exerciseModel.Variant{variantOfDevices(node("ok", platform, withScript)), variantOfDevices(node("y", "private.example/x:1", withCommand))}, 2, "y", "unauthorized"},
		"unread platform image":        {[]exerciseModel.Variant{variantOfDevices(node("z", "platform:old@sha256:"+repeat("b"), withArgs))}, 1, "z", ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := u.requireServiceUsersKnown(ctx, exerciseID, tc.variants)
			require.ErrorIs(t, err, exerciseModel.ErrServiceUserUnknown.Err())
			var carrier interface{ PublicContext() map[string]any }
			require.ErrorAs(t, err, &carrier)
			want := map[string]any{"variant": tc.variant, "device": tc.device, "field": "image"}
			if tc.reason != "" {
				want["reason"] = tc.reason
			}
			require.Equal(t, want, carrier.PublicContext())
		})
	}

	// Narrow constructions have no registry that can tell: nothing to check.
	require.NoError(t, (&ExerciseUseCase{}).requireServiceUsersKnown(ctx, exerciseID, []exerciseModel.Variant{variantOfDevices(node("x", external, withScript))}))
}

func repeat(s string) string {
	out := ""
	for range 64 {
		out += s
	}
	return out
}
