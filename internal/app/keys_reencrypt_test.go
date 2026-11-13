package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

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

func (f *fakeRun) Keys(string) (string, []string, error) { return "k2", []string{"k1"}, nil }

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
	_, err := reencryptCommand(context.Background(), f, "platform", false, false, "", &out)
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
	_, err := reencryptCommand(context.Background(), f, "platform", true, false, "", &out)
	require.NoError(t, err)
	require.True(t, f.opts.DryRun)
	f2 := &fakeRun{rep: report()}
	_, err = reencryptCommand(context.Background(), f2, "vpn", false, true, "", &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, 1, f2.calls)
	require.False(t, f2.opts.DryRun)
}

// 26. failed rows are listed with source, row id, key id and reason; the count per reason is printed.
func TestReencryptCommand_ListsFailedRows(t *testing.T) {
	var out bytes.Buffer
	n, err := reencryptCommand(context.Background(), &fakeRun{rep: report()}, "platform", false, true, "", &out)
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
	_, err := reencryptCommand(context.Background(), &fakeRun{rep: report(), err: errors.New("connection reset")}, "platform", false, true, "", &out)
	require.Error(t, err)
	require.Contains(t, out.String(), "mail_smtp_configs.password_ciphertext")
}

// 25. nothing in the output is a plaintext or a ciphertext: the report carries only source, row id, key id, class.
func TestReencryptCommand_OutputHoldsNoValues(t *testing.T) {
	var out bytes.Buffer
	_, _ = reencryptCommand(context.Background(), &fakeRun{rep: report()}, "platform", false, true, "", &out)
	require.False(t, strings.Contains(out.String(), "v1:"), "no ciphertext form in the output")
}

// 6. the key that will seal and the keys that only read are shown before the --yes gate and in a dry run, so a wrong ring
// order is seen before anything is written.
func TestReencryptCommand_ShowsSealingKeyBeforeTheGate(t *testing.T) {
	f := &fakeRun{rep: report()}
	var out bytes.Buffer
	_, err := reencryptCommand(context.Background(), f, "platform", false, false, "", &out)
	require.ErrorIs(t, err, ErrNeedsYes)
	require.Contains(t, out.String(), `Values will be sealed with key "k2".`)
	require.Contains(t, out.String(), `Read only (never seal): "k1".`)
	require.Zero(t, f.calls)

	out.Reset()
	_, err = reencryptCommand(context.Background(), &fakeRun{rep: report()}, "platform", true, false, "", &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), `Values will be sealed with key "k2".`)
}

// 3. SIGTERM and SIGINT end the run through the context, so it is written as interrupted and not left running.
func TestSignalContext_EndsOnSIGTERM(t *testing.T) {
	ctx, stop := signalContext(context.Background())
	defer stop()
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context was not cancelled by SIGTERM")
	}
}

// 7. --expect-key: a keyring whose sealing key is not the expected one is refused before anything runs, also in a dry run;
// the right key goes through. The help says scripts must pass it.
func TestReencryptCommand_ExpectKey(t *testing.T) {
	for _, dry := range []bool{false, true} {
		f := &fakeRun{rep: report()}
		var out bytes.Buffer
		_, err := reencryptCommand(context.Background(), f, "platform", dry, true, "k1", &out)
		require.ErrorIs(t, err, ErrUnexpectedKey)
		require.Contains(t, err.Error(), `expected "k1", found "k2"`)
		require.Zero(t, f.calls, "nothing runs when the sealing key is not the expected one")
	}
	f := &fakeRun{rep: report()}
	_, err := reencryptCommand(context.Background(), f, "platform", false, true, "k2", &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, 1, f.calls)
	require.Contains(t, keyrotation.Help, "--expect-key")
	require.Contains(t, keyrotation.Help, "always pass it")
	require.Contains(t, keyrotation.Help, "direct database connection")
	require.Contains(t, keyrotation.Help, "never changed")
}

// 6. after the first signal the handler is released, so a second one ends the process at once. A child process runs
// signalContext with SIGTERM: the first signal cancels the context and the child keeps working; the second kills it.
func TestSignalContext_SecondSignalKills(t *testing.T) {
	if os.Getenv("KEYS_SIGNAL_HELPER") == "1" {
		ctx, stop := signalContext(context.Background(), syscall.SIGTERM)
		defer stop()
		fmt.Println("ready")
		<-ctx.Done()
		fmt.Println("cancelled")
		time.Sleep(30 * time.Second) // a long cleanup: only the second signal can end it
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSignalContext_SecondSignalKills")
	cmd.Env = append(os.Environ(), "KEYS_SIGNAL_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	reader := bufio.NewReader(stdout)
	waitLine := func(want string) {
		for {
			line, readErr := reader.ReadString('\n')
			require.NoError(t, readErr)
			if strings.TrimSpace(line) == want {
				return
			}
		}
	}
	waitLine("ready")
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	waitLine("cancelled")
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		require.Error(t, waitErr, "the second signal must kill the process")
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the second signal did not end the process")
	}
}
