package secretkeys

import (
	"fmt"
	"io"
	"strings"

	"github.com/cybericebox/daemon/pkg/secret"
)

// Locator says where a key of a family is kept, for the operator who has to remove it: a file with its mode, or the
// variable it comes from. It reads no key content.
type Locator func(f Family, keyID string) string

const stamp = "2006-01-02 15:04:05Z"

// WriteVerdict prints the answer for one key: the verdict, why not or what to do, and for a derived key the rule of the
// master version it comes from.
func WriteVerdict(w io.Writer, v Verdict, locate Locator, master *MasterRule) {
	status := "NOT YET"
	if v.Retirable {
		status = "RETIRABLE (may be removed from the servers)"
	}
	fmt.Fprintf(w, "Key %q of %s: %s\nSnapshot of %s; the answer holds for that moment and only while no instance seals with this key.\n",
		v.KeyID, v.Family, status, v.SnapshotAt.UTC().Format(stamp))
	if v.Derived {
		fmt.Fprintf(w, "It is derived from master key version %q.\n", v.MasterID)
	}
	if !v.Retirable {
		fmt.Fprintln(w, "\nWhy not:")
		for _, b := range v.Blockers {
			fmt.Fprintf(w, "  - %s\n", b)
		}
	} else {
		fmt.Fprintf(w, "\nWhere it is: %s\n\nDo, in this order:\n", locate(v.Family, v.KeyID))
		fmt.Fprintln(w, "  1. Remove the key from every instance (roll each one; the sealing key stays first).")
		sentence := v.BackupSentence()
		fmt.Fprintf(w, "  2. %s%s.\n", strings.ToUpper(sentence[:1]), sentence[1:])
		fmt.Fprintln(w, "  3. The daemon cannot see your backups; this check never says a key may be destroyed.")
	}
	if master != nil {
		fmt.Fprintf(w, "\nThe master key version %q seals more than this family:\n", master.MasterID)
		for _, s := range master.Scanned {
			state := "retirable"
			if !s.Retirable {
				state = fmt.Sprintf("not yet (%d reason(s); run keys check %s %s)", len(s.Blockers), s.Family, s.KeyID)
			}
			fmt.Fprintf(w, "  scanned      %-9s %s: %s\n", s.Family, s.KeyID, state)
		}
		for _, text := range master.Unscanned {
			fmt.Fprintf(w, "  not scanned  %s\n", text)
		}
		fmt.Fprintf(w, "The master version %q may leave the servers when every scanned purpose above is retirable AND the time of every "+
			"not scanned purpose has passed since all instances loaded the new master key. The same backup rule applies to it.\n",
			master.MasterID)
	}
}

// WriteOverview prints one line per loaded key that is not the sealing one, with its verdict, and what is not covered.
func WriteOverview(w io.Writer, rep Report, l Lifetimes) {
	fmt.Fprintf(w, "Keys that do not seal (snapshot of %s):\n", rep.SnapshotAt.UTC().Format(stamp))
	judged := false
	for _, f := range Families {
		for _, id := range rep.KeysToJudge()[f] {
			judged = true
			v, err := rep.Verdict(f, id)
			switch {
			case err != nil:
				fmt.Fprintf(w, "  [%s] %s: cannot be judged: %v\n", f, secret.SafeKeyID(id), err)
			case v.Retirable:
				fmt.Fprintf(w, "  [%s] %s: retirable (run keys check %s %s for the steps)\n", f, v.KeyID, f, v.KeyID)
			default:
				fmt.Fprintf(w, "  [%s] %s: not yet, %d reason(s) (run keys check %s %s)\n", f, v.KeyID, len(v.Blockers), f, v.KeyID)
			}
		}
	}
	if !judged {
		fmt.Fprintln(w, "  none: every family has only its sealing key, nothing to retire")
	}
	fmt.Fprintln(w, "\nPurposes the daemon does not scan:")
	for _, p := range secret.AllPurposes {
		if rule := PurposeRules[p]; rule.Kind != ScannedPurpose {
			text := "not covered"
			if rule.Rule != nil {
				text = rule.Rule(l)
			}
			fmt.Fprintf(w, "  %s: %s\n", p, text)
		}
	}
	fmt.Fprintln(w, "\nRetirable means removable from the servers, never destroyable: database backups and your offline copy are yours to judge.")
}
