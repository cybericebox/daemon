package resourceCalendar

import (
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestStorageReservationArithmeticAndAdmission(t *testing.T) {
	r, err := NewEventReservation(EventInput{EventID: uuid.Must(uuid.NewV7()), Window: Window{Start: time.Unix(0, 0), End: time.Unix(3600, 0)}, Teams: 3, PerTeam: Amount{CPUMillicores: 100}, PerTeamSnapshotQuotaBytes: 1 << 30, DynamicSnapshotQuotaBytes: 2 << 30, BufferPercent: 10}, uuid.Nil, time.Unix(0, 0))
	require.NoError(t, err)
	require.EqualValues(t, 5<<30, r.SizeSnapshotQuotaBytes)
	require.Error(t, r.SetSnapshotQuota(2<<30, time.Unix(1, 0)))
	require.NoError(t, r.SetSnapshotQuota(6<<30, time.Unix(1, 0)))
}
