package labBindingModel

import (
	"testing"

	"github.com/gofrs/uuid"
)

func TestParticipantClientNameRoundTrip(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	name := ParticipantClientName(id)
	if len(name) != 2+ShortIDLen {
		t.Fatalf("name %q has length %d", name, len(name))
	}
	if got, ok := ParseParticipantClientName(name); !ok || got != id {
		t.Fatalf("round trip: %v %v", got, ok)
	}
	for _, bad := range []string{"", "p-", "p-nope", "p-" + id.String(), "m-" + ShortID(id), "tester", name + "0", "P-" + ShortID(id)} {
		if _, ok := ParseParticipantClientName(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}
