package secretkeys

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cybericebox/daemon/pkg/secret"
)

// Write prints the report for an operator: the loaded key ids per family, then per column how many values each key
// sealed, how many are in the older form without a key id, and the values that cannot be opened with their rows.
func (r Report) Write(w io.Writer) {
	mode := "key id only (not opened)"
	if r.Opened {
		mode = "every value opened with its row context"
	}
	fmt.Fprintf(w, "Check: %s\n\nLoaded keys (the first one seals):\n", mode)
	for _, f := range []Family{FamilyPlatform, FamilyVPN, FamilyExercise} {
		if ids := r.Keys[f]; len(ids) > 0 {
			labels := make([]string, len(ids))
			for i, id := range ids {
				labels[i] = keyLabel(id)
			}
			fmt.Fprintf(w, "  %-9s %s\n", f, strings.Join(labels, ", "))
		} else {
			fmt.Fprintf(w, "  %-9s no key configured\n", f)
		}
	}
	fmt.Fprintln(w, "\nStored values:")
	if len(r.Columns) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, c := range r.Columns {
		ids := make([]string, 0, len(c.ByKey))
		for id := range c.ByKey {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		parts := make([]string, 0, len(ids)+1)
		for _, id := range ids {
			parts = append(parts, fmt.Sprintf("key %s: %d", keyLabel(id), c.ByKey[id]))
		}
		if c.Legacy > 0 {
			parts = append(parts, fmt.Sprintf("no key id (older form): %d", c.Legacy))
		}
		fmt.Fprintf(w, "  [%s] %s: %d value(s); %s\n", c.Family, c.Name, c.Total, strings.Join(parts, "; "))
		if c.Problems > 0 {
			fmt.Fprintf(w, "    CANNOT BE OPENED: %d (%s)\n", c.Problems, reasonCounts(c.ByReason))
			for _, p := range c.ProblemRefs {
				fmt.Fprintf(w, "      %s (%s)\n", p.Ref, p.describe())
			}
			if c.Problems > len(c.ProblemRefs) {
				fmt.Fprintf(w, "      ... and %d more\n", c.Problems-len(c.ProblemRefs))
			}
		}
	}
	if len(r.Rotation) > 0 {
		fmt.Fprintln(w, "\nRe-encryption runs:")
		for _, s := range r.Rotation {
			state := s.State
			if s.ProcessGone {
				state += " (process gone)"
			}
			fmt.Fprintf(w, "  [%s] %s: %s, to key %s: %d scanned, %d rewritten, %d failed, %d changed meanwhile (%s), %s\n",
				s.Family, s.Source, state, s.KeyID, s.Total, s.Done, s.Failed, s.Conflicts, reasonCountsOf(s.FailedByReason),
				s.UpdatedAt.UTC().Format("2006-01-02 15:04:05Z"))
		}
	}
	fmt.Fprintln(w, "\nNot covered (short-lived):")
	for _, s := range NotScanned {
		fmt.Fprintf(w, "  %s\n", s)
	}
	if n := r.Problems(); n > 0 {
		fmt.Fprintf(w, "\nRESULT: %d value(s) cannot be opened with the loaded keys. Do not remove any key; load the missing one.\n", n)
		return
	}
	fmt.Fprintln(w, "\nRESULT: every stored value opens with the loaded keys.")
}

// keyLabel tells a key derived from the master key (its id says so and names the master version) from an explicit one.
func keyLabel(id string) string {
	if master, derived := secret.IsDerivedID(id); derived {
		return fmt.Sprintf("%s (derived from master %s)", id, master)
	}
	return id + " (explicit)"
}

func (p Problem) describe() string {
	switch p.Kind {
	case KeyNotLoaded:
		return fmt.Sprintf("sealed by key %q, which is not loaded", p.KeyID)
	case NoKeyOpens:
		return "older form without a key id; no loaded key opens it"
	case CannotOpen:
		return fmt.Sprintf("key %q is loaded but the value does not open: damaged, or its row context changed", p.KeyID)
	case NoFamilyKey:
		return "no key is configured for this family"
	}
	return p.Kind
}

// reasonCounts lists the counts per reason class in a fixed order, zero ones left out.
func reasonCounts(by map[secret.Reason]int) string {
	m := make(map[string]int, len(by))
	for k, v := range by {
		m[string(k)] = v
	}
	return reasonCountsOf(m)
}

func reasonCountsOf(by map[string]int) string {
	var parts []string
	for _, r := range secret.Reasons {
		if n := by[string(r)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", r, n))
		}
	}
	if len(parts) == 0 {
		return "no failures"
	}
	return strings.Join(parts, ", ")
}
