package exerciseModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// Publish gates for linked variants. Both name the 1-based position of the variant in the version (and the subtask
// by name) in the public context, so the author knows where to look.
var (
	// ErrLinkedAttachmentMissing: a subtask of the base variant has files and the same subtask of a linked variant has
	// none, so the teams given that variant would miss the files.
	ErrLinkedAttachmentMissing = err.ErrInvalidData.WithObjectCode(model.LinkedVariantObjectCode).
					WithMessage("A linked variant has no files for a subtask whose base variant has files").WithDetailCode(1)
	// ErrLinkedVariantIdentical: a linked variant has no flag, files or image of its own that differ from its base.
	ErrLinkedVariantIdentical = err.ErrInvalidData.WithObjectCode(model.LinkedVariantObjectCode).
					WithMessage("A linked variant must differ from its base variant").WithDetailCode(2)
)
