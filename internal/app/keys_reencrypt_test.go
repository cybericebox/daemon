package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
	"github.com/cybericebox/daemon/pkg/secret"
)

type fakeRun struct {
	calls int
	opts  keyrotation.Options
	rep   keyrotation.Report
	err   error
}

func (f *fakeRun) Run(_ context.Context, family string, opts keyrotation.Options) (keyrotation.Report, error) {
	f.calls++
	f.opts = opts
	f.rep.Family = family
	return f.rep, f.err
}

func report() keyrotation.Report {
	return keyrotation.Report{KeyID: "k2", Sources: []secret.Result{{
		Progress: secret.Progress{Source: "mail_smtp_configs.password_ciphertext", Total: 4, Done: 2, Failed: 2, Conflicts: 1,
			ByReason: map[secret.Reason]int{secret.ReasonAAD: 1, secret.ReasonUnknownKey: 1}},
		AlreadyCurrent: 1,
		FailedRows: []secret.Failed{
			{Source: "mail_smtp_configs.password_ciphertext", RowID: "row-a", KeyID: "k1", Reason: secret.ReasonAAD},
			{Source: "mail_smtp_configs.password_ciphertext", RowID: "row-b", KeyID: "k0", Reason: secret.ReasonUnknownKey},
		},
	}}}
}

// 28. the three precondition lines are always printed; a real run without --yes does not start.
func TestReencryptCommand_PreconditionsAndYes(t *testing.T) {
	f := &fakeRun{rep: report()}
	var out bytes.Buffer
	_, err := reencryptCommand(context.Background(), f, "platform", false, false, &out)
	require.ErrorIs(t, err, ErrNeedsYes)
	require.Zero(t, f.calls, "nothing runs without --yes")
	for _, line := range keyrotation.Preconditions {
		require.Contains(t, out.String(), line)
	}
	require.Len(t, keyrotation.Preconditions, 3)
	require.Contains(t, keyrotation.Help, "Every instance must have the new key loaded before reencrypt starts.")
	require.Contains(t, keyrotation.Help, "Take a database backup first.")
	require.Contains(t, keyrotation.Help, "Keep the old key file until `keys check` passes.")
}

// 23. dry run needs no --yes and says so; a real run with --yes runs.
func TestReencryptCommand_DryRunAndYes(t *testing.T) {
	f := &fakeRun{rep: report()}
	var out bytes.Buffer
	_, err := reencryptCommand(context.Background(), f, "platform", true, false, &out)
	require.NoError(t, err)
	require.True(t, f.opts.DryRun)
	f2 := &fakeRun{rep: report()}
	_, err = reencryptCommand(context.Background(), f2, "vpn", false, true, &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, 1, f2.calls)
	require.False(t, f2.opts.DryRun)
}

// 26. failed rows are listed with source, row id, key id and reason; the count per reason is printed.
func TestReencryptCommand_ListsFailedRows(t *testing.T) {
	var out bytes.Buffer
	n, err := reencryptCommand(context.Background(), &fakeRun{rep: report()}, "platform", false, true, &out)
	require.NoError(t, err)
	require.Equal(t, 3, n, "failed rows and rows changed meanwhile give a non-zero exit")
	text := out.String()
	require.Contains(t, text, `mail_smtp_configs.password_ciphertext row "row-a": aad_mismatch (key id: "k1")`)
	require.Contains(t, text, `mail_smtp_configs.password_ciphertext row "row-b": unknown_key_id (key id: "k0")`)
	require.Contains(t, text, "unknown_key_id: 1, aad_mismatch: 1")
}

// A stopped run prints what it did and returns the error (exit 2), not a row count.
func TestReencryptCommand_StoppedRun(t *testing.T) {
	var out bytes.Buffer
	_, err := reencryptCommand(context.Background(), &fakeRun{rep: report(), err: errors.New("connection reset")}, "platform", false, true, &out)
	require.Error(t, err)
	require.Contains(t, out.String(), "mail_smtp_configs.password_ciphertext")
}

// 25. nothing in the output is a plaintext or a ciphertext: the report carries only source, row id, key id, class.
func TestReencryptCommand_OutputHoldsNoValues(t *testing.T) {
	var out bytes.Buffer
	_, _ = reencryptCommand(context.Background(), &fakeRun{rep: report()}, "platform", false, true, &out)
	require.False(t, strings.Contains(out.String(), "v1:"), "no ciphertext form in the output")
}
