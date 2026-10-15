package jobsModel

import (
	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNotifyQueueSeparatesOnlyNewDurableEnvelopes(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name  string
		args  NotifyArgs
		queue string
	}{
		{"ordinary", NotifyArgs{DispatchID: id, Type: "password_reset"}, river.QueueDefault},
		{"journal", NotifyArgs{JournalDeliveryID: &id}, NotifyDurableQueue},
		{"auth", NotifyArgs{AuthDeliveryID: &id}, NotifyDurableQueue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.queue, tc.args.InsertOpts().Queue)
			require.Equal(t, "notify", tc.args.Kind())
		})
	}
}
