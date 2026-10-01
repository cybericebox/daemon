package platformAnalyticsModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// PlatformAnalyticsObjectCode — next free detail code: 3
var (
	ErrPlatformAnalyticsPeriodInvalid = err.ErrInvalidData.WithObjectCode(model.PlatformAnalyticsObjectCode).
						WithMessage("The analytics period is invalid").WithDetailCode(1)
	// ErrPlatformAnalyticsTableUnknown: a CSV export asked for a table the
	// section does not have.
	ErrPlatformAnalyticsTableUnknown = err.ErrInvalidData.WithObjectCode(model.PlatformAnalyticsObjectCode).
						WithMessage("The analytics export table is unknown").WithDetailCode(2)
)
