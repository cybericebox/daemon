package event

import (
	"fmt"
	"testing"
	"time"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/gofrs/uuid"
)

func TestVPNPeerConnectedUsesOnlyTheOwnFreshHandshake(t *testing.T) {
	now := time.Now()
	user, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	peer := func(id uuid.UUID, last time.Time) string {
		return fmt.Sprintf(`{"name":%q,"status":{"statistics":{"lastHandshakeUnix":%q}}}`, labBindingModel.ParticipantClientName(id), fmt.Sprint(last.Unix()))
	}
	for _, tc := range []struct {
		name, payload string
		age           time.Duration
		want          *bool
	}{
		{"own connected", `{"clients":[` + peer(user, now.Add(-time.Minute)) + `]}`, 0, boolPointer(true)},
		{"own disconnected", `{"clients":[` + peer(user, now.Add(-4*time.Minute)) + `]}`, 0, boolPointer(false)},
		{"other connected", `{"clients":[` + peer(other, now) + `]}`, 0, boolPointer(false)},
		{"stale snapshot", `{"clients":[` + peer(user, now) + `]}`, 4 * time.Minute, nil},
		{"future handshake", `{"clients":[` + peer(user, now.Add(10*time.Minute)) + `]}`, 0, nil},
		{"invalid snapshot", `{`, 0, nil},
		{"null snapshot", `null`, 0, nil},
		{"missing clients", `{}`, 0, nil},
		{"null clients", `{"clients":null}`, 0, nil},
		{"null client", `{"clients":[null]}`, 0, nil},
		{"unnamed client", `{"clients":[{}]}`, 0, nil},
		{"duplicate own peer", `{"clients":[` + peer(user, now) + `,` + peer(user, now) + `]}`, 0, nil},
		{"negative timestamp", fmt.Sprintf(`{"clients":[{"name":%q,"status":{"statistics":{"lastHandshakeUnix":"-1"}}}]}`, labBindingModel.ParticipantClientName(user)), 0, nil},
		{"null timestamp", fmt.Sprintf(`{"clients":[{"name":%q,"status":{"statistics":{"lastHandshakeUnix":null}}}]}`, labBindingModel.ParticipantClientName(user)), 0, nil},
		{"zero timestamp", fmt.Sprintf(`{"clients":[{"name":%q,"status":{"statistics":{"lastHandshakeUnix":"0"}}}]}`, labBindingModel.ParticipantClientName(user)), 0, boolPointer(false)},
		{"empty clients", `{"clients":[]}`, 0, boolPointer(false)},
		{"never connected", fmt.Sprintf(`{"clients":[{"name":%q,"status":{}}]}`, labBindingModel.ParticipantClientName(user)), 0, boolPointer(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := vpnPeerConnected([]byte(tc.payload), user, now.Add(-tc.age), now)
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("connection=%v, want %v", got, tc.want)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
