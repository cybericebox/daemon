package errorJournalUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole journal
	"github.com/rs/zerolog/log"
)

// RelayMessage tells the other replicas that a group changed. It carries ids only (a NOTIFY payload is limited to
// 8000 bytes and a sample may be large): the receiver reads the rows. Origin is the instance that published it, so
// it does not show the event twice to its own streams.
type RelayMessage struct {
	Origin   string    `json:"o"`
	GroupID  uuid.UUID `json:"g"`
	SampleID uuid.UUID `json:"s,omitempty"`
	New      bool      `json:"n,omitempty"`
}

// Relay carries the live stream between the replicas of the daemon (Postgres LISTEN/NOTIFY in production). Each
// replica's own Hub only knows the streams opened on that replica; the relay lets an error recorded on one replica
// reach the browsers connected to the others. It is best effort: a message lost while a replica reconnects is not
// replayed (the page refetches the list when it reconnects).
type Relay interface {
	Publish(ctx context.Context, m RelayMessage) error
	// Listen delivers the messages of every replica to handle until ctx ends, reconnecting by itself.
	Listen(ctx context.Context, handle func(RelayMessage))
}

const relayTimeout = 3 * time.Second

// publish shows an event to the streams of this replica and tells the others.
func (j *Journal) publish(ctx context.Context, e StreamEvent) {
	j.hub.Publish(e)
	if j.relay == nil {
		return
	}
	m := RelayMessage{Origin: j.instance, GroupID: e.Group.ID, New: e.New}
	if e.Sample != nil {
		m.SampleID = e.Sample.ID
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), relayTimeout)
	defer cancel()
	if err := j.relay.Publish(ctx, m); err != nil {
		log.Debug().Err(err).Msg("Failed to relay an error journal event to the other replicas")
	}
}

// Listen feeds the streams of this replica with what the other replicas record. Start it once per process.
func (j *Journal) Listen(ctx context.Context) {
	if j.relay == nil {
		return
	}
	j.relay.Listen(ctx, j.fromPeer)
}

// fromPeer turns a message of another replica into a stream event by reading the rows it names.
func (j *Journal) fromPeer(m RelayMessage) {
	if m.Origin == j.instance || j.hub.Len() == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), relayTimeout)
	defer cancel()
	g, err := j.repo.GetGroup(ctx, m.GroupID)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to read the group of a relayed error journal event")
		return
	}
	ev := StreamEvent{Group: g, New: m.New}
	if m.SampleID != uuid.Nil {
		samples, listErr := j.repo.ListSamples(ctx, g.ID, j.cfg.SamplesPerGroup)
		if listErr == nil {
			for i := range samples {
				if samples[i].ID == m.SampleID {
					ev.Sample = &samples[i]
					break
				}
			}
		}
	}
	j.hub.Publish(ev)
}
