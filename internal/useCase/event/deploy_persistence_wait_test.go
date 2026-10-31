package event

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// The agent refusing a persistent lab because state persistence is not available (yet) is a wait that is retried, not a
// failed stand; the wait alarm tells the organizers after three minutes.
func TestHandleDeployError_PersistenceUnavailableIsAWait(t *testing.T) {
	u := &EventUseCase{}
	pending := labBindingRepo.PendingLab{Binding: labBindingModel.Binding{LabGroupName: "g", LabName: "l"}}
	for name, err := range map[string]error{
		"the coded error":   infraModel.ErrDevicePersistenceUnavailable.Err(),
		"wrapped by deploy": fmt.Errorf("create lab: %w", infraModel.ReasonError(infraModel.ReasonPersistenceUnavailable)),
	} {
		held, wait, handled := u.handleDeployError(context.Background(), pending, eventLabModel.Lab{}, err, time.Now())
		require.NoError(t, handled, name)
		assert.False(t, held, name)
		assert.Equal(t, eventStandModel.WaitPersistenceUnavailable, wait, name)
	}
}
