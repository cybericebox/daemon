package laboratoryModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	VPNClient struct {
		UserID   uuid.UUID
		GroupID  uuid.UUID
		Banned   bool
		LastSeen time.Time
	}
)

var (
	ErrVPN = err.ErrInternal.WithObjectCode(model.VPNObjectCode)
)
