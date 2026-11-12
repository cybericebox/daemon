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
	reason, _, waiting := revealWait(fmt.Errorf("reserve: %w", errPersistenceUnavailable))
	assert.True(t, waiting)
	assert.Equal(t, eventStandModel.WaitPersistenceUnavailable, reason)

	reason, _, waiting = revealWait(calModel.ErrNotEnoughReserved.Err())
	assert.True(t, waiting)
	assert.Equal(t, eventStandModel.WaitNotEnoughReserved, reason)

	reason, _, waiting = revealWait(snapshotQuotaShort(2<<30, 1<<30))
	assert.True(t, waiting)
	assert.Equal(t, eventStandModel.WaitSnapshotQuota, reason, "the short snapshot room is named, and it still is a not-enough-reserved")
	assert.ErrorIs(t, snapshotQuotaShort(2<<30, 1<<30), calModel.ErrNotEnoughReserved.Err())

	_, _, waiting = revealWait(errors.New("database down"))
	assert.False(t, waiting)
	assert.Contains(t, eventStandModel.WaitReasonText(eventStandModel.WaitPersistenceUnavailable), "persistence")
}

// A refusal for lack of room carries what was needed and what there is, for the organizers to read.
func TestRevealWaitCarriesTheNeedAndTheRoom(t *testing.T) {
	shortage := reservationShortage{detail: eventStandModel.WaitDetail{NeedCPUMillicores: 1848, NeedMemoryBytes: 7 << 30, HaveCPUMillicores: 1352, HaveMemoryBytes: 5 << 30}}
	reason, detail, waiting := revealWait(fmt.Errorf("reserve: %w", shortage))
	assert.True(t, waiting)
	assert.Equal(t, eventStandModel.WaitNotEnoughReserved, reason)
	assert.Equal(t, int64(1848), detail.NeedCPUMillicores)
	assert.Equal(t, int64(5<<30), detail.HaveMemoryBytes)
	assert.ErrorIs(t, shortage, calModel.ErrNotEnoughReserved.Err())

	_, detail, _ = revealWait(snapshotQuotaShort(2<<30, 1<<30))
	assert.Equal(t, int64(2<<30), detail.NeedStorageBytes)

	encoded := withWaitDetail(reason, detail)
	back, backDetail := splitWaitDetail(encoded)
	assert.Equal(t, reason, back)
	assert.Equal(t, detail, backDetail)
	plain, none := splitWaitDetail(eventStandModel.WaitNotEnoughReserved)
	assert.Equal(t, eventStandModel.WaitNotEnoughReserved, plain)
	assert.Nil(t, none)

	assert.Contains(t, eventStandModel.WaitDetailText(`{"need_cpu_millicores":1848,"need_memory_bytes":7516192768,"have_cpu_millicores":1352,"have_memory_bytes":5368709120}`), "needs 1848m CPU and 7168 MiB, has 1352m CPU and 5120 MiB")
	assert.Empty(t, eventStandModel.WaitDetailText("not json"))
}
