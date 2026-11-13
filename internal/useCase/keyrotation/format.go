package keyrotation

import (
	"fmt"
	"io"
	"strings"

	"github.com/cybericebox/daemon/pkg/secret"
)

// Preconditions are the three lines every operator reads before a run. The job cannot see which instances hold the
// new key, so this is a deployment rule; the same lines go into the runbook of the infrastructure side.
var Preconditions = []string{
	"Every instance must have the new key loaded before reencrypt starts.",
	"Take a database backup first.",
	"Keep the old key file until `keys check` passes.",
}

// Help is the text of `daemon keys reencrypt`.
const Help = `usage: daemon keys reencrypt <family> [--dry-run] [--yes]

Moves every stored ciphertext of a key family (platform, vpn, exercise) to the current key of its keyring. Each value is
opened with its row context, sealed again, opened once more and compared before one compare-and-swap write; a value that
cannot be opened is left as it is and listed. No key and no row is deleted. Safe to stop and run again.

  --dry-run  open and count everything, write nothing (the progress table is not touched)
  --yes      required for a real run, also in scripts

Before a real run:
  1. ` + "Every instance must have the new key loaded before reencrypt starts." + `
  2. ` + "Take a database backup first." + `
  3. ` + "Keep the old key file until `keys check` passes." + `

Exit code: 0 all rows moved or already current, 1 some rows could not be opened or were changed meanwhile, 2 the run stopped.`

// Write prints a report: per source the counts, then every failed row as source, row id, key id and reason class. It
// never prints a value or any part of a ciphertext.
func (r Report) Write(w io.Writer) {
	mode := "REAL RUN"
	if r.DryRun {
		mode = "DRY RUN, nothing written"
	}
	fmt.Fprintf(w, "Re-encryption of %q to key %s (%s)\n\n", r.Family, r.KeyID, mode)
	for _, s := range r.Sources {
		verb := "rewritten"
		if s.DryRun {
			verb = "would be rewritten"
		}
		fmt.Fprintf(w, "  %s: %d scanned, %d %s, %d already current, %d changed meanwhile (retry on the next run), %d failed\n",
			s.Source, s.Total, s.Done, verb, s.AlreadyCurrent, s.Conflicts, s.Failed)
	}
	if r.ProgressNotRecorded {
		fmt.Fprintln(w, "\nProgress not recorded: the progress table could not be written during this run, so `keys status` shows a stale state. Trust this report, not the table.")
	}
	failed := 0
	for _, s := range r.Sources {
		failed += s.Failed
	}
	if failed == 0 {
		return
	}
	fmt.Fprintln(w, "\nRows that cannot be opened (left untouched):")
	for _, s := range r.Sources {
		for _, f := range s.FailedRows {
			key := "none"
			if f.KeyID != "" {
				key = f.KeyID
			}
			detail := ""
			if f.Detail != "" {
				detail = ", " + f.Detail
			}
			fmt.Fprintf(w, "  %s row %q%s: %s (key id: %q)\n", secret.Printable(f.Source, 120), f.RowID, detail, f.Reason, key)
		}
	}
	by := map[secret.Reason]int{}
	for _, s := range r.Sources {
		for k, v := range s.ByReason {
			by[k] += v
		}
	}
	var parts []string
	for _, reason := range secret.Reasons {
		if by[reason] > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", reason, by[reason]))
		}
	}
	fmt.Fprintf(w, "\nFailed by reason: %s. These block `keys check` until fixed.\n", strings.Join(parts, ", "))
}
