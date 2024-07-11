package model

import (
	"github.com/cybericebox/daemon/internal/appError"
)

var (
	ErrNotFound      = appError.NewError().WithCode(appError.CodeNotFound)
	ErrAlreadyExists = appError.NewError().WithCode(appError.CodeAlreadyExists)
	ErrForbidden     = appError.NewError().WithCode(appError.CodeForbidden)
)
