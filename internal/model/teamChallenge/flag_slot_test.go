package teamChallengeModel_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"math/big"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

var (
	slotSecret   = []byte("fixed-server-secret-for-tests-only")
	slotExercise = uuid.FromStringOrNil("01920000-0000-7000-8000-000000000001")
	slotTask     = uuid.FromStringOrNil("01920000-0000-7000-8000-000000000002")
)

func TestFlagSlotKey_IsTheDomainSeparatedHMAC(t *testing.T) {
	mac := hmac.New(sha256.New, slotSecret)
	mac.Write([]byte("cib-flag-slot"))
	mac.Write(slotExercise.Bytes())
	mac.Write(slotTask.Bytes())
	mac.Write([]byte{0, 0, 0, 3})
	if got := teamChallengeModel.FlagSlotKey(slotSecret, slotExercise, slotTask, 3); !bytes.Equal(got, mac.Sum(nil)) {
		t.Fatalf("key = %x", got)
	}
	if bytes.Equal(teamChallengeModel.FlagSlotKey(slotSecret, slotExercise, slotTask, 3), teamChallengeModel.FlagSlotKey(slotSecret, slotExercise, slotTask, 4)) {
		t.Fatal("variants share a key")
	}
}

func TestResolveTeamFlag_SlotModeAndFullRandomMode(t *testing.T) {
	candidates := []string{`ICE{a}`, `template:ICE{[ab][0-2]}`}
	key := teamChallengeModel.FlagSlotKey(slotSecret, slotExercise, slotTask, 0)
	seen := map[string]bool{}
	for slot := range uint64(7) {
		got, err := teamChallengeModel.ResolveTeamFlag(candidates, 20, slot, key)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := flagpattern.ResolveAt(candidates, new(big.Int).SetUint64(slot), key)
		if got != want || seen[got] {
			t.Fatalf("slot %d = %q (want %q, repeat %t)", slot, got, want, seen[got])
		}
		seen[got] = true
	}
	random, err := teamChallengeModel.ResolveTeamFlag(nil, 20, 0, key)
	if err != nil || len(random) != len("ICE{}")+40 {
		t.Fatalf("full random flag = %q, %v", random, err)
	}
}
