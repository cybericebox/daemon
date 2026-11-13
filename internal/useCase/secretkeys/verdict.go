package secretkeys

import (
	"errors"
	"fmt"
	"time"

	"github.com/cybericebox/daemon/pkg/secret"
)

// The reasons a verdict cannot be given.
var (
	ErrUnknownFamily = errors.New("unknown key family")
	ErrKeyNotLoaded  = errors.New("the key is not loaded for this family")
	ErrNotOpened     = errors.New("the report was not made by a full check")
)

// Families lists the key families in the order a report names them.
var Families = []Family{FamilyPlatform, FamilyVPN, FamilyExercise}

// Verdict answers one question about one loaded key: may it be removed from the servers.
type Verdict struct {
	Family  Family
	KeyID   string
	Current bool
	// Derived is true for a key derived from a master version; MasterID is that version.
	Derived  bool
	MasterID string
	// Retirable: nothing stored, nothing short-lived and no unfinished run needs the key any more.
	Retirable bool
	// Blockers are the reasons it is not, one line each; they name a column, a count and a state, never a value.
	Blockers []string
	// LeftAt is when the last value left the key, as far as the progress rows know; zero when they do not.
	LeftAt time.Time
	// SnapshotAt is when the scan that this answer comes from began.
	SnapshotAt time.Time
}

// Verdict decides whether the key may be removed from the servers. It reads only the report: one snapshot, no second scan.
// It never says a key may be destroyed: a backup taken before the last value left the key still needs it.
func (r Report) Verdict(f Family, keyID string) (Verdict, error) {
	v := Verdict{Family: f, KeyID: secret.SafeKeyID(keyID), SnapshotAt: r.SnapshotAt}
	known := false
	for _, family := range Families {
		known = known || family == f
	}
	if !known {
		return v, fmt.Errorf("%w: %q", ErrUnknownFamily, string(f))
	}
	if !r.Opened {
		return v, ErrNotOpened
	}
	ids := r.Keys[f]
	loaded := false
	for _, id := range ids {
		loaded = loaded || id == keyID
	}
	if !loaded {
		return v, fmt.Errorf("%w: %s %q", ErrKeyNotLoaded, f, v.KeyID)
	}
	v.Current = ids[0] == keyID
	v.MasterID, v.Derived = secret.IsDerivedID(keyID)
	v.MasterID = secret.SafeKeyID(v.MasterID)

	block := func(format string, args ...any) { v.Blockers = append(v.Blockers, fmt.Sprintf(format, args...)) }
	if v.Current {
		block("it is the sealing key of the family: every new value is sealed with it")
	}
	for _, c := range r.Columns {
		if c.Family != f {
			continue
		}
		if c.Problems > 0 {
			block("%d value(s) in %s cannot be opened (%s): nothing can be retired before they are fixed", c.Problems, c.Name, reasonCounts(c.ByReason))
		}
		if c.Legacy > 0 {
			block("%d value(s) in %s are in the older form without a key id: run keys reencrypt %s first", c.Legacy, c.Name, f)
		}
		if n := c.ByKey[v.KeyID]; n > 0 {
			block("%d value(s) in %s are sealed by it: run keys reencrypt %s", n, c.Name, f)
		}
	}
	for _, s := range r.ShortLived {
		if s.Family == f && s.ByKey[v.KeyID] > 0 {
			block("%d short-lived value(s) in %s are sealed by it: they expire or finish by themselves, wait", s.ByKey[v.KeyID], s.Name)
		}
	}
	for _, s := range r.Rotation {
		if Family(s.Family) != f {
			continue
		}
		state := s.State
		if s.ProcessGone {
			state += " (process gone)"
		}
		if s.State != "finished" {
			block("the re-encryption of %s is %s: run keys reencrypt %s until it is finished", secret.Printable(s.Source, 120), state, f)
		}
		if s.LastMovedAt != nil && s.LastMovedAt.After(v.LeftAt) {
			v.LeftAt = *s.LastMovedAt
		}
	}
	v.Retirable = len(v.Blockers) == 0
	return v, nil
}

// BackupSentence is the last step for a retirable key. Retirable means removable from the servers, never destroyable.
func (v Verdict) BackupSentence() string {
	cutoff := fmt.Sprintf("before this check (%s)", v.SnapshotAt.UTC().Format("2006-01-02 15:04:05Z"))
	if !v.LeftAt.IsZero() {
		cutoff = fmt.Sprintf("before %s, when the last value left this key", v.LeftAt.UTC().Format("2006-01-02 15:04:05Z"))
	}
	return fmt.Sprintf("keep one offline copy of the key for as long as any database backup taken %s is kept (restoring such a backup "+
		"needs it); destroy the copy only after the last such backup is gone", cutoff)
}

// KeysToJudge lists, per family, the loaded keys that are not the sealing one, in the order of the keyring.
func (r Report) KeysToJudge() map[Family][]string {
	out := map[Family][]string{}
	for _, f := range Families {
		if ids := r.Keys[f]; len(ids) > 1 {
			out[f] = append([]string(nil), ids[1:]...)
		}
	}
	return out
}

// MasterRule is the verdict on a master version: the derived key of every scanned purpose, and the rule of every other.
type MasterRule struct {
	MasterID string
	// Scanned are the verdicts of the derived key d-<MasterID> in each scanned purpose's family, in the order of AllPurposes.
	Scanned []Verdict
	// Unscanned are the purposes the daemon cannot speak for, with the sentence that tells the operator what to do.
	Unscanned []string
	// AllScannedRetirable: every scanned purpose says the derived key may go.
	AllScannedRetirable bool
}

// Master walks every purpose of the derivation through the one table PurposeRules.
func (r Report) Master(masterID string, l Lifetimes) (MasterRule, error) {
	m := MasterRule{MasterID: secret.SafeKeyID(masterID), AllScannedRetirable: true}
	derived := "d-" + masterID
	for _, p := range secret.AllPurposes {
		rule, ok := PurposeRules[p]
		if !ok {
			return m, fmt.Errorf("the purpose %q has no rule in PurposeRules", p)
		}
		switch rule.Kind {
		case ScannedPurpose:
			v, err := r.Verdict(rule.Family, derived)
			if err != nil {
				if errors.Is(err, ErrKeyNotLoaded) {
					continue // this family does not hold that master version
				}
				return m, err
			}
			m.Scanned = append(m.Scanned, v)
			m.AllScannedRetirable = m.AllScannedRetirable && v.Retirable
		default:
			text := fmt.Sprintf("%s: not covered by the daemon", p)
			if rule.Rule != nil {
				text = fmt.Sprintf("%s: %s", p, rule.Rule(l))
			}
			m.Unscanned = append(m.Unscanned, text)
		}
	}
	return m, nil
}
