package secretkeys_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/useCase/secretkeys"
	"github.com/cybericebox/daemon/pkg/secret"
)

var snapshotAt = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// cleanReport is a database where every platform value is on k2, k1 is only loaded, and nothing else holds anything.
func cleanReport() secretkeys.Report {
	return secretkeys.Report{
		Opened:     true,
		SnapshotAt: snapshotAt,
		Keys: map[secretkeys.Family][]string{
			secretkeys.FamilyPlatform: {"k2", "k1"},
			secretkeys.FamilyVPN:      {"k2", "k1"},
			secretkeys.FamilyExercise: {"k2", "k1"},
		},
		Columns: []secretkeys.Column{
			{Family: secretkeys.FamilyPlatform, Name: "mail_smtp_configs.password_ciphertext", Total: 3, ByKey: map[string]int{"k2": 3}},
			{Family: secretkeys.FamilyVPN, Name: "user_vpn_configs.config", Total: 2, ByKey: map[string]int{"k2": 2}},
		},
		ShortLived: []secretkeys.ShortLived{
			{Family: secretkeys.FamilyPlatform, Name: "temporal_codes.data (not expired)", ByKey: map[string]int{}},
			{Family: secretkeys.FamilyPlatform, Name: "river_job.args (not finished)", ByKey: map[string]int{}},
		},
		Rotation: []secretkeys.RotationState{
			{Family: "platform", Source: "mail_smtp_configs.password_ciphertext", KeyID: "k2", State: "finished", Total: 3, Done: 3, UpdatedAt: snapshotAt.Add(-2 * time.Hour), LastMovedAt: at(2026, 11, 14, 7, 0)},
		},
	}
}

func verdict(t *testing.T, rep secretkeys.Report, f secretkeys.Family, id string) secretkeys.Verdict {
	t.Helper()
	v, err := rep.Verdict(f, id)
	require.NoError(t, err)
	return v
}

// 1. everything on the current key, nothing legacy, broken, short-lived or unfinished: the old key may be removed.
func TestVerdict_RetirableWhenNothingNeedsTheKey(t *testing.T) {
	v := verdict(t, cleanReport(), secretkeys.FamilyPlatform, "k1")
	require.True(t, v.Retirable, "%v", v.Blockers)
	require.Empty(t, v.Blockers)
}

// 2. a value left on the old key in a persistent column.
func TestVerdict_ValueLeftOnTheKeyBlocks(t *testing.T) {
	rep := cleanReport()
	rep.Columns[0].ByKey = map[string]int{"k2": 2, "k1": 1}
	v := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
	require.False(t, v.Retirable)
	require.Contains(t, v.Blockers[0], "1 value(s) in mail_smtp_configs.password_ciphertext are sealed by it")
	// the current key and a key of another family are unaffected by it
	require.True(t, verdict(t, rep, secretkeys.FamilyVPN, "k1").Retirable)
}

// 3. a legacy value (no key id) blocks every key of the family that does not seal.
func TestVerdict_LegacyValueBlocks(t *testing.T) {
	rep := cleanReport()
	rep.Columns[0].Legacy = 2
	v := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
	require.False(t, v.Retirable)
	require.Contains(t, strings.Join(v.Blockers, "\n"), "2 value(s) in mail_smtp_configs.password_ciphertext are in the older form without a key id")
}

// 4. a value that cannot be opened blocks every key of its family, whatever key it names.
func TestVerdict_UnopenableValueBlocks(t *testing.T) {
	rep := cleanReport()
	rep.Columns[0].Problems = 1
	rep.Columns[0].ByReason = map[secret.Reason]int{secret.ReasonUnknownKey: 1}
	v := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
	require.False(t, v.Retirable)
	require.Contains(t, strings.Join(v.Blockers, "\n"), "cannot be opened (unknown_key_id: 1)")
}

// 5. a re-encryption run that is not finished blocks; a finished one and a run of another family do not.
func TestVerdict_UnfinishedRunBlocks(t *testing.T) {
	for _, state := range []string{"running", "interrupted", "failed", "finished_with_failures"} {
		rep := cleanReport()
		rep.Rotation[0].State = state
		v := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
		require.False(t, v.Retirable, state)
		require.Contains(t, strings.Join(v.Blockers, "\n"), state)
	}
	gone := cleanReport()
	gone.Rotation[0].State, gone.Rotation[0].ProcessGone = "interrupted", true
	require.Contains(t, strings.Join(verdict(t, gone, secretkeys.FamilyPlatform, "k1").Blockers, "\n"), "process gone")

	other := cleanReport()
	other.Rotation = append(other.Rotation, secretkeys.RotationState{Family: "vpn", Source: "user_vpn_configs.config", State: "running"})
	require.True(t, verdict(t, other, secretkeys.FamilyPlatform, "k1").Retirable, "a run of another family does not block")
	require.False(t, verdict(t, other, secretkeys.FamilyVPN, "k1").Retirable)
}

// 6. the sealing key is never retirable; an unknown family or key, and a report that was not a full check, are errors.
func TestVerdict_CurrentKeyAndErrors(t *testing.T) {
	rep := cleanReport()
	cur := verdict(t, rep, secretkeys.FamilyPlatform, "k2")
	require.False(t, cur.Retirable)
	require.True(t, cur.Current)
	require.Contains(t, cur.Blockers[0], "sealing key")

	_, err := rep.Verdict("nope", "k1")
	require.ErrorIs(t, err, secretkeys.ErrUnknownFamily)
	_, err = rep.Verdict(secretkeys.FamilyPlatform, "k9")
	require.ErrorIs(t, err, secretkeys.ErrKeyNotLoaded)
	cheap := cleanReport()
	cheap.Opened = false
	_, err = cheap.Verdict(secretkeys.FamilyPlatform, "k1")
	require.True(t, errors.Is(err, secretkeys.ErrNotOpened))
}

// 7. short-lived values sealed by the key block it, per key id; other ids do not.
func TestVerdict_ShortLivedValuesBlock(t *testing.T) {
	rep := cleanReport()
	rep.ShortLived[0].ByKey = map[string]int{"k1": 2, "k2": 5}
	v := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
	require.False(t, v.Retirable)
	require.Contains(t, v.Blockers[0], "2 short-lived value(s) in temporal_codes.data (not expired)")

	rep = cleanReport()
	rep.ShortLived[1].ByKey = map[string]int{"k1": 1}
	require.Contains(t, verdict(t, rep, secretkeys.FamilyPlatform, "k1").Blockers[0], "river_job.args (not finished)")

	rep = cleanReport()
	rep.ShortLived[0].ByKey = map[string]int{"k2": 9}
	require.True(t, verdict(t, rep, secretkeys.FamilyPlatform, "k1").Retirable)
}

// 8. a derived key: the master version's purposes are walked through the one table.
func TestMaster_WalksEveryPurposeThroughTheTable(t *testing.T) {
	rep := cleanReport()
	for f := range rep.Keys {
		rep.Keys[f] = []string{"d-m2", "d-m1"}
	}
	rep.Columns[1].ByKey = map[string]int{"d-m1": 1, "d-m2": 1} // vpn still holds a value on the old master version
	v := verdict(t, rep, secretkeys.FamilyPlatform, "d-m1")
	require.True(t, v.Derived)
	require.Equal(t, "m1", v.MasterID)

	l := secretkeys.Lifetimes{SessionIdle: 12 * time.Hour, SessionAbsolute: 168 * time.Hour, EventRenderGrant: 5 * time.Minute}
	m, err := rep.Master("m1", l)
	require.NoError(t, err)
	require.False(t, m.AllScannedRetirable, "vpn is not done")
	require.Len(t, m.Scanned, 3)
	require.Len(t, m.Unscanned, 1)
	require.Contains(t, m.Unscanned[0], "session")
	require.Contains(t, m.Unscanned[0], "SESSION_IDLE_TTL=12h0m0s")
	require.Contains(t, m.Unscanned[0], "SESSION_ABSOLUTE_TTL=168h0m0s")
	require.Contains(t, m.Unscanned[0], "5m0s")
	require.Contains(t, m.Unscanned[0], "cannot know when the instances switched")

	// an explicit key has no master rule
	plain := verdict(t, cleanReport(), secretkeys.FamilyPlatform, "k1")
	require.False(t, plain.Derived)
}

func at(y, m, d, h, min int) *time.Time {
	t := time.Date(y, time.Month(m), d, h, min, 0, 0, time.UTC)
	return &t
}

// 9. the time the last value left the key comes from the progress rows of the family; without them the check's own time.
func TestVerdict_BackupSentenceUsesTheProgressRows(t *testing.T) {
	rep := cleanReport()
	rep.Rotation = []secretkeys.RotationState{
		{Family: "platform", Source: "a", State: "finished", LastMovedAt: at(2026, 11, 13, 10, 0)},
		{Family: "platform", Source: "b", State: "finished", LastMovedAt: at(2026, 11, 13, 17, 30)},
		{Family: "platform", Source: "c", State: "finished", UpdatedAt: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}, // a later run that moved nothing
		{Family: "vpn", Source: "d", State: "finished", LastMovedAt: at(2026, 11, 14, 8, 30)},                          // another family
	}
	v := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
	require.Equal(t, time.Date(2026, 11, 13, 17, 30, 0, 0, time.UTC), v.LeftAt)
	s := v.BackupSentence()
	require.Contains(t, s, "before 2026-11-13 17:30:00Z, when the last value left this key")
	require.Contains(t, s, "keep one offline copy")
	require.Contains(t, s, "destroy the copy only after the last such backup is gone")

	rep.Rotation = nil
	none := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
	require.True(t, none.LeftAt.IsZero())
	require.Contains(t, none.BackupSentence(), "before this check (2026-11-14 09:00:00Z)")
}

// B. every purpose the derivation knows has a rule, and the table has nothing the derivation does not know: a purpose
// added later cannot be forgotten.
func TestPurposeRules_CoverEveryPurposeOfTheDerivation(t *testing.T) {
	listed := map[secret.Purpose]bool{}
	for _, p := range secret.AllPurposes {
		listed[p] = true
		rule, ok := secretkeys.PurposeRules[p]
		require.True(t, ok, "purpose %q is derived but has no rule in PurposeRules: say whether `keys check` scans it, bounds it by a lifetime, or does not cover it", p)
		if rule.Kind == secretkeys.ScannedPurpose {
			require.NotEmpty(t, rule.Family, p)
		} else {
			require.NotNil(t, rule.Rule, "an unscanned purpose needs the sentence an operator acts on: %q", p)
			require.NotEmpty(t, rule.Rule(secretkeys.Lifetimes{}), p)
		}
	}
	for p := range secretkeys.PurposeRules {
		require.True(t, listed[p], "PurposeRules has %q, which is not in secret.AllPurposes", p)
	}
}

// The text of the answers: no value anywhere; a retirable key gets the steps and the backup rule; the overview names what
// is not scanned.
func TestWriteVerdictAndOverview(t *testing.T) {
	rep := cleanReport()
	v := verdict(t, rep, secretkeys.FamilyPlatform, "k1")
	var out strings.Builder
	secretkeys.WriteVerdict(&out, v, func(secretkeys.Family, string) string { return "/keys/k1.key (mode 0400)" }, nil)
	text := out.String()
	require.Contains(t, text, "RETIRABLE (may be removed from the servers)")
	require.Contains(t, text, "/keys/k1.key (mode 0400)")
	require.Contains(t, text, "Remove the key from every instance")
	require.Contains(t, text, "Keep one offline copy of the key for as long as any database backup taken")
	require.Contains(t, text, "never says a key may be destroyed")

	rep.Columns[0].ByKey = map[string]int{"k1": 1, "k2": 2}
	out.Reset()
	secretkeys.WriteVerdict(&out, verdict(t, rep, secretkeys.FamilyPlatform, "k1"), nil, nil)
	require.Contains(t, out.String(), "NOT YET")
	require.NotContains(t, out.String(), "Do, in this order")

	out.Reset()
	l := secretkeys.Lifetimes{SessionIdle: time.Hour, SessionAbsolute: 2 * time.Hour, EventRenderGrant: time.Minute}
	secretkeys.WriteOverview(&out, rep, l)
	require.Contains(t, out.String(), "[platform] k1: not yet")
	require.Contains(t, out.String(), "[vpn] k1: retirable")
	require.Contains(t, out.String(), "Purposes the daemon does not scan")
	require.Contains(t, out.String(), "session:")

	only := secretkeys.Report{Opened: true, SnapshotAt: snapshotAt, Keys: map[secretkeys.Family][]string{secretkeys.FamilyPlatform: {"k2"}}}
	out.Reset()
	secretkeys.WriteOverview(&out, only, l)
	require.Contains(t, out.String(), "nothing to retire")
}
