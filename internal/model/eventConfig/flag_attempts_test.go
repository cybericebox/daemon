package eventConfigModel_test

import (
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

func TestUpdateMaxFlagAttempts(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	if c.MaxFlagAttempts != nil {
		t.Fatalf("a new event is unlimited, got %d", *c.MaxFlagAttempts)
	}
	in := eventConfigModel.ConfigInput{MaxTeamSize: 5, ShowDifficulty: true, Countdown: c.Countdown}
	in.MaxFlagAttempts = new(int32)
	*in.MaxFlagAttempts = 7
	if err := c.Update(in, cfgNow, uuid.Must(uuid.NewV7())); err != nil || c.MaxFlagAttempts == nil || *c.MaxFlagAttempts != 7 {
		t.Fatalf("limit 7: %v %v", c.MaxFlagAttempts, err)
	}
	// The stored value is a copy: changing the input afterwards does not move it.
	*in.MaxFlagAttempts = 9
	if *c.MaxFlagAttempts != 7 {
		t.Fatal("the config must not alias the input")
	}
	for _, bad := range []int32{0, -3, challengeAttempt.MaxAttemptLimit + 1} {
		bad := bad
		in.MaxFlagAttempts = &bad
		if err := c.Update(in, cfgNow, uuid.Nil); !errors.Is(err, challengeAttempt.ErrAttemptLimitInvalid.Err()) {
			t.Fatalf("limit %d: err = %v", bad, err)
		}
	}
	if *c.MaxFlagAttempts != 7 {
		t.Fatal("a refused update must not change the limit")
	}
	in.MaxFlagAttempts = nil
	if err := c.Update(in, cfgNow, uuid.Nil); err != nil || c.MaxFlagAttempts != nil {
		t.Fatalf("clearing: %v %v", c.MaxFlagAttempts, err)
	}
}
