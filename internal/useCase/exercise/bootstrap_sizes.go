package exercise

import (
	"context"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// requireBootstrapSizes refuses, at a draft save, a bootstrap file of the archive over the laboratory's size ceilings and a
// device whose script and files are over theirs in all (the same numbers the start of a lab checks). The sizes of the
// archive files come from the stored file records. Without a file store (narrow constructions) only the inline sizes count.
func (u *ExerciseUseCase) requireBootstrapSizes(ctx context.Context, variants []exerciseModel.Variant) error {
	sizes := map[uuid.UUID]int64{}
	size := func(ref exerciseModel.AttachmentRef) (int64, error) {
		if n, ok := sizes[ref.FileID]; ok {
			return n, nil
		}
		if u.files == nil {
			return 0, nil
		}
		file, err := u.files.GetFile(ctx, ref.FileID)
		if err != nil {
			return 0, err // GetFile already answers with a platform error or ErrFileNotFound
		}
		sizes[ref.FileID] = file.SizeBytes
		return file.SizeBytes, nil
	}
	for position, variant := range variants {
		if err := variant.ValidateBootstrapSizes(size); err != nil {
			return exerciseModel.InVariantIfScoped(err, position+1)
		}
	}
	return nil
}
