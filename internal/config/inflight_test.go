package config_test

import (
	"testing"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/cybericebox/daemon/internal/config"
)

func TestInflightConfigDefaultsAndValidation(t *testing.T) {
	var c config.InflightConfig
	if err := env.ParseWithOptions(
		&c,
		env.Options{Prefix: "HTTP_INFLIGHT_", Environment: map[string]string{}},
	); err != nil {
		t.Fatal(err)
	}
	if c.Max != 64 || c.Wait != 50*time.Millisecond || c.StreamsMax != 200 || c.TransfersMax != 32 {
		t.Fatalf("defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (config.InflightConfig{}).Validate(); err != nil {
		t.Fatalf("zero turns every cap off and is valid: %v", err)
	}
	for name, bad := range map[string]config.InflightConfig{
		"negative max":       {Max: -1},
		"negative streams":   {StreamsMax: -1},
		"negative transfers": {TransfersMax: -1},
		"negative wait":      {Wait: -time.Millisecond},
		"wait over 1s":       {Wait: time.Second + time.Millisecond},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	vars := map[string]string{
		"HTTP_INFLIGHT_MAX":           "7",
		"HTTP_INFLIGHT_WAIT":          "1s",
		"HTTP_INFLIGHT_STREAMS_MAX":   "0",
		"HTTP_INFLIGHT_TRANSFERS_MAX": "3",
	}
	if err := env.ParseWithOptions(
		&c,
		env.Options{Prefix: "HTTP_INFLIGHT_", Environment: vars},
	); err != nil {
		t.Fatal(err)
	}
	if c.Max != 7 || c.Wait != time.Second || c.StreamsMax != 0 || c.TransfersMax != 3 {
		t.Fatalf("env keys: %+v", c)
	}
}
