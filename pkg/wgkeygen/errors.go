package wgkeygen

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`.
//
// WgKeyGenObjectCode — next free detail code: 1
var (
	// multi-site: the generic key-generation failure (always 500); per-site
	// reason via WithMessage/WithError.
	ErrWgKeyGen = err.ErrInternal.WithObjectCode(model.WgKeyGenObjectCode)
)
