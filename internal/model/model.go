package model

import (
	"github.com/cybericebox/daemon/internal/tools"
)

var (
	ErrNotFound = tools.NewError("not found", 404)
)
