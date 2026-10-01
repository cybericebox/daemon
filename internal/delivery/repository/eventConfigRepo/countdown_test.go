package eventConfigRepo

import (
	"testing"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

func TestToDomainCountdown(t *testing.T) {
	got := ToDomain(postgres.EventConfig{ShowStartCountdown: false, ShowFinishCountdown: true, FinishCountdownMinutes: 25}).Countdown
	if want := (eventConfigModel.CountdownSettings{ShowFinish: true, FinishMinutes: 25}); got != want {
		t.Fatalf("countdown = %+v, want %+v", got, want)
	}
	// Zero-valued generated rows fall back to the migration defaults.
	if got := ToDomain(postgres.EventConfig{}).Countdown; got != eventConfigModel.DefaultCountdownSettings() {
		t.Fatalf("zero row countdown = %+v", got)
	}
}
