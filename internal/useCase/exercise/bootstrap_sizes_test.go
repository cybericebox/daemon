package exercise

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

type sizedFiles struct {
	sizes map[uuid.UUID]int64
	asked int
}

func (f *sizedFiles) StreamFile(context.Context, uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	return nil, mediaModel.File{}, errors.New("not used")
}

func (f *sizedFiles) GetFile(_ context.Context, id uuid.UUID) (mediaModel.File, error) {
	f.asked++
	size, ok := f.sizes[id]
	if !ok {
		return mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return mediaModel.File{ID: id, SizeBytes: size}, nil
}

// A draft refuses an attached bootstrap file over the ceiling, by the stored size, and asks for each file once.
func TestRequireBootstrapSizes(t *testing.T) {
	fine, big, ghost := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	files := &sizedFiles{sizes: map[uuid.UUID]int64{fine: 10, big: exerciseModel.MaxBootstrapFileBytes + 1}}
	u := &ExerciseUseCase{files: files}
	variant := func(ids ...uuid.UUID) []exerciseModel.Variant {
		var list []exerciseModel.BootstrapFile
		for _, id := range ids {
			list = append(list, exerciseModel.BootstrapFile{Path: "/f/" + id.String(), Source: &exerciseModel.AttachmentRef{FileID: id}})
		}
		return []exerciseModel.Variant{{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
			Name: "web", Type: exerciseModel.DeviceTypeNode, Bootstrap: exerciseModel.Bootstrap{Files: list},
		}}}}}
	}
	ctx := context.Background()
	require.NoError(t, u.requireBootstrapSizes(ctx, variant(fine, fine)))
	require.Equal(t, 1, files.asked, "the same file is asked for once")
	require.ErrorIs(t, u.requireBootstrapSizes(ctx, variant(fine, big)), exerciseModel.ErrBootstrapTooLarge.Err())
	require.ErrorIs(t, u.requireBootstrapSizes(ctx, variant(ghost)), mediaModel.ErrFileNotFound.Err())
	require.NoError(t, u.requireBootstrapSizes(ctx, nil))
}
