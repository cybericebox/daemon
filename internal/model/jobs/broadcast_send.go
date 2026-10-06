package jobsModel

import "github.com/gofrs/uuid"

// BroadcastSendArgs sends one chunk of a broadcast to its next recipients and
// queues itself again while recipients remain (a River job is time-bounded).
type BroadcastSendArgs struct {
	BroadcastID uuid.UUID `json:"broadcast_id"`
}

func (BroadcastSendArgs) Kind() string { return "broadcast_send" }
