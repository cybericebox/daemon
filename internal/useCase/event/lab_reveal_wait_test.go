package event

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// A cohort that cannot be reserved because the agents offer no state persistence says so, not "no room".
func TestRevealWait(t *testing.T) {
	reason, waiting := revealWait(fmt.Errorf("reserve: %w", errPersistenceUnavailable))
	assert.True(t, waiting)
	assert.Equal(t, eventStandModel.WaitPersistenceUnavailable, reason)

	reason, waiting = revealWait(calModel.ErrNotEnoughReserved.Err())
	assert.True(t, waiting)
	assert.Equal(t, eventStandModel.WaitNotEnoughReserved, reason)

	reason, waiting = revealWait(snapshotQuotaShort())
	assert.True(t, waiting)
	assert.Equal(t, eventStandModel.WaitSnapshotQuota, reason, "the short snapshot room is named, and it still is a not-enough-reserved")
	assert.ErrorIs(t, snapshotQuotaShort(), calModel.ErrNotEnoughReserved.Err())

	_, waiting = revealWait(errors.New("database down"))
	assert.False(t, waiting)
	assert.Contains(t, eventStandModel.WaitReasonText(eventStandModel.WaitPersistenceUnavailable), "persistence")
}
