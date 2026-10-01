package model

import "github.com/cybericebox/daemon/pkg/err"

var ErrPlatform = err.ErrInternal.WithObjectCode(PlatformObjectCode)
