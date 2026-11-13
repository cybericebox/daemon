package secret_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cybericebox/daemon/pkg/secret"
)

var (
	keyOld = strings.Repeat("11", 32)
	keyNew = strings.Repeat("22", 32)
)

func ring(t *testing.T, spec string) *secret.Cipher {
	t.Helper()
	c, err := secret.New(spec)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// oldRing seals with k1 only, bothRing seals with k2 and still opens k1.
func oldRing(t *testing.T) *secret.Cipher  { return ring(t, "k1:"+keyOld) }
func bothRing(t *testing.T) *secret.Cipher { return ring(t, "k2:"+keyNew+",k1:"+keyOld) }

func seal(t *testing.T, c *secret.Cipher, plain, ctx string) string {
	t.Helper()
	ct, err := c.EncryptWithContext([]byte(plain), []byte(ctx))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return ct
}

// fakeSource is a store of rows with hooks at the points a real database would race.
type fakeSource struct {
	mu         sync.Mutex
	order      []string
	rows       map[string][]string
	ctxs       map[string][]string
	beforeSwap func(id string)
	swapErr    map[string]error
	swaps      int
	afterSwap  func(n int)
	rowCalls   int
	refs       []string
}

func newFake() *fakeSource {
	return &fakeSource{rows: map[string][]string{}, ctxs: map[string][]string{}, swapErr: map[string]error{}}
}

func (f *fakeSource) add(id, ctx string, values ...string) {
	f.order = append(f.order, id)
	f.rows[id] = values
	for range values {
		f.ctxs[id] = append(f.ctxs[id], ctx)
	}
}

func (f *fakeSource) Family() string { return "fam" }
func (f *fakeSource) Name() string   { return "fake.col" }

func (f *fakeSource) Rows(_ context.Context, after string, limit int) ([]secret.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowCalls++
	var out []secret.Row
	for _, id := range f.order {
		if id <= after {
			continue
		}
		row := secret.Row{ID: id}
		for i, v := range f.rows[id] {
			it := secret.Item{Ciphertext: v, Context: []byte(f.ctxs[id][i])}
			if i < len(f.refs) {
				it.Ref = f.refs[i]
			}
			row.Items = append(row.Items, it)
		}
		out = append(out, row)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeSource) Swap(_ context.Context, id string, oldV, newV []string) (bool, error) {
	if f.beforeSwap != nil {
		f.beforeSwap(id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.swapErr[id]; err != nil {
		return false, err
	}
	cur := f.rows[id]
	for i := range cur {
		if cur[i] != oldV[i] {
			return false, nil
		}
	}
	f.rows[id] = newV
	f.swaps++
	if f.afterSwap != nil {
		f.afterSwap(f.swaps)
	}
	return true, nil
}

func (f *fakeSource) get(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.rows[id]...)
}

func keyOf(v string) string { id, _ := secret.KeyID(v); return id }

func run(t *testing.T, r *secret.Reencryptor, src secret.Source, opts secret.Options) (secret.Result, error) {
	t.Helper()
	return r.Run(context.Background(), src, opts)
}

// 1. one version: nothing to do.
func TestReencrypt_OneVersion_NoWrites(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "ctx-a", seal(t, c, "pa", "ctx-a"))
	f.add("b", "ctx-b", seal(t, c, "pb", "ctx-b"))
	before := f.get("a")[0]
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f.swaps != 0 || res.Total != 2 || res.AlreadyCurrent != 2 || res.Done != 0 || f.get("a")[0] != before {
		t.Fatalf("no writes expected: swaps=%d %+v", f.swaps, res)
	}
}

// 2. two versions: only old rows move; every row opens afterwards; rows on the new key stay byte for byte.
func TestReencrypt_TwoVersions_OnlyOldRowsMove(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "ctx-a", seal(t, oldRing(t), "pa", "ctx-a"))
	f.add("b", "ctx-b", seal(t, c, "pb", "ctx-b"))
	f.add("c", "ctx-c", seal(t, oldRing(t), "pc", "ctx-c"))
	keep := f.get("b")[0]
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != 2 || res.AlreadyCurrent != 1 || res.Failed != 0 || res.KeyID != "k2" {
		t.Fatalf("unexpected result %+v", res)
	}
	for id, want := range map[string]string{"a": "pa", "b": "pb", "c": "pc"} {
		got, err := c.DecryptWithContext(f.get(id)[0], []byte("ctx-"+id))
		if err != nil || string(got) != want {
			t.Fatalf("row %s: %q %v", id, got, err)
		}
		if keyOf(f.get(id)[0]) != "k2" {
			t.Fatalf("row %s not on k2", id)
		}
	}
	if f.get("b")[0] != keep {
		t.Fatal("a row already on the current key was rewritten")
	}
}

// 3. rows already on the new key: a second run writes nothing and the counters do not grow.
func TestReencrypt_SecondRunIsIdempotent(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "ctx-a", seal(t, oldRing(t), "pa", "ctx-a"))
	r := &secret.Reencryptor{Ring: c}
	if _, err := run(t, r, f, secret.Options{}); err != nil {
		t.Fatal(err)
	}
	swaps := f.swaps
	res, err := run(t, r, f, secret.Options{})
	if err != nil || f.swaps != swaps || res.Done != 0 || res.AlreadyCurrent != 1 {
		t.Fatalf("second run must be a no-op: %+v %v swaps=%d", res, err, f.swaps)
	}
}

// brokenSeal returns a value sealed for another plaintext, so the verification must catch it.
type brokenSeal struct{ *secret.Cipher }

func (b brokenSeal) EncryptWithContext(_, ctx []byte) (string, error) {
	return b.Cipher.EncryptWithContext([]byte("other"), ctx)
}

// 4. verify-by-decrypt: a seal that does not round-trip is never written.
func TestReencrypt_VerifyFailureIsNotWritten(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "ctx-a", seal(t, oldRing(t), "pa", "ctx-a"))
	old := f.get("a")[0]
	res, err := run(t, &secret.Reencryptor{Ring: brokenSeal{c}}, f, secret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.ByReason[secret.ReasonVerify] != 1 || f.swaps != 0 || f.get("a")[0] != old {
		t.Fatalf("verify failure must leave the row: %+v", res)
	}
}

// 5. AAD mismatch: the row is failed and untouched, other rows go on.
func TestReencrypt_AADMismatchFailsOnlyThatRow(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "renamed", seal(t, oldRing(t), "pa", "original"))
	f.add("b", "ctx-b", seal(t, oldRing(t), "pb", "ctx-b"))
	old := f.get("a")[0]
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Done != 1 || res.ByReason[secret.ReasonAAD] != 1 || f.get("a")[0] != old {
		t.Fatalf("unexpected %+v", res)
	}
	if len(res.FailedRows) != 1 || res.FailedRows[0] != (secret.Failed{Source: "fake.col", RowID: "a", KeyID: "k1", Reason: secret.ReasonAAD}) {
		t.Fatalf("failed rows: %+v", res.FailedRows)
	}
}

// 6. the old key is gone from the ring: rows naming it are failed, untouched, the run does not panic.
func TestReencrypt_LostOldKey(t *testing.T) {
	onlyNew := ring(t, "k2:"+keyNew)
	f := newFake()
	f.add("a", "ctx-a", seal(t, oldRing(t), "pa", "ctx-a"))
	f.add("b", "ctx-b", seal(t, ring(t, "k0:"+strings.Repeat("33", 32)), "pb", "ctx-b"))
	f.add("c", "ctx-c", seal(t, onlyNew, "pc", "ctx-c"))
	old := f.get("a")[0]
	res, err := run(t, &secret.Reencryptor{Ring: onlyNew}, f, secret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 2 || res.ByReason[secret.ReasonUnknownKey] != 2 || res.AlreadyCurrent != 1 || f.get("a")[0] != old {
		t.Fatalf("unexpected %+v", res)
	}
}

// 7. legacy value without a key id: moved when an old key can open it, failed when none can.
func TestReencrypt_Legacy(t *testing.T) {
	single := ring(t, keyOld) // sealed as default:<hex>; strip the prefix to get the older form
	full := seal(t, single, "pl", "ctx-l")
	legacy := strings.SplitN(strings.TrimPrefix(full, "v1:"), ":", 2)[1]

	withOld := ring(t, "k2:"+keyNew+",default:"+keyOld)
	f := newFake()
	f.add("l", "ctx-l", legacy)
	res, err := run(t, &secret.Reencryptor{Ring: withOld}, f, secret.Options{})
	if err != nil || res.Done != 1 || keyOf(f.get("l")[0]) != "k2" {
		t.Fatalf("legacy must move: %+v %v", res, err)
	}

	withoutOld := ring(t, "k2:"+keyNew)
	g := newFake()
	g.add("l", "ctx-l", legacy)
	res, err = run(t, &secret.Reencryptor{Ring: withoutOld}, g, secret.Options{})
	if err != nil || res.Failed != 1 || g.get("l")[0] != legacy {
		t.Fatalf("legacy without its key must fail untouched: %+v %v", res, err)
	}
}

// 8. interruption in the middle: written rows stay readable, the rest untouched, the next run finishes the job.
func TestReencrypt_InterruptedThenResumed(t *testing.T) {
	c := bothRing(t)
	build := func() *fakeSource {
		f := newFake()
		for i := 0; i < 6; i++ {
			id := fmt.Sprintf("r%d", i)
			f.add(id, "ctx-"+id, seal(t, oldRing(t), "p"+id, "ctx-"+id))
		}
		return f
	}
	f := build()
	ctx, cancel := context.WithCancel(context.Background())
	f.afterSwap = func(n int) {
		if n == 3 {
			cancel()
		}
	}
	var last secret.Progress
	r := &secret.Reencryptor{Ring: c, BatchSize: 2, Report: func(p secret.Progress) { last = p }}
	res, err := r.Run(ctx, f, secret.Options{})
	if !errors.Is(err, context.Canceled) || res.Done != 3 {
		t.Fatalf("expected cancel after 3 rows: %+v %v", res, err)
	}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("r%d", i)
		if _, err := c.DecryptWithContext(f.get(id)[0], []byte("ctx-"+id)); err != nil {
			t.Fatalf("row %s unreadable after interruption: %v", id, err)
		}
		wantNew := i < 3
		if (keyOf(f.get(id)[0]) == "k2") != wantNew {
			t.Fatalf("row %s on %s", id, keyOf(f.get(id)[0]))
		}
	}
	// the cancel came inside the second batch, so the last report is the one after the first batch: exactly 2
	if last.Done != 2 || last.Total != 2 {
		t.Fatalf("progress should show exactly the first batch, got %+v", last)
	}
	f.afterSwap = nil
	if _, err := run(t, r, f, secret.Options{}); err != nil {
		t.Fatal(err)
	}
	ref := build()
	if _, err := run(t, &secret.Reencryptor{Ring: c}, ref, secret.Options{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("r%d", i)
		if keyOf(f.get(id)[0]) != keyOf(ref.get(id)[0]) {
			t.Fatalf("resumed run differs from an uninterrupted one at %s", id)
		}
	}
}

// 9. a store error on row K stops the run with an error; K keeps its old value and is not counted failed.
func TestReencrypt_StoreErrorStops(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	for _, id := range []string{"a", "b", "c"} {
		f.add(id, "ctx-"+id, seal(t, oldRing(t), "p"+id, "ctx-"+id))
	}
	f.swapErr["b"] = errors.New("connection reset")
	old := f.get("b")[0]
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if !errors.Is(err, secret.ErrStore) || res.Failed != 0 || res.Done != 1 || f.get("b")[0] != old {
		t.Fatalf("unexpected %+v %v", res, err)
	}
	if keyOf(f.get("c")[0]) != "k1" {
		t.Fatal("rows after the error must not be touched")
	}
}

// 10. batches: N+1 rows with batch N are all processed, with a pause between batches.
func TestReencrypt_BatchesAndPause(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("r%d", i)
		f.add(id, "c", seal(t, oldRing(t), "p", "c"))
	}
	var pauses []time.Duration
	r := &secret.Reencryptor{
		Ring: c, BatchSize: 2, Pause: time.Second,
		Sleep: func(_ context.Context, d time.Duration) error { pauses = append(pauses, d); return nil },
	}
	res, err := run(t, r, f, secret.Options{})
	if err != nil || res.Done != 5 || res.Total != 5 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(pauses) != 3 { // between 3 batches with rows, and before the empty closing read
		t.Fatalf("pauses %v", pauses)
	}
}

// 11. no key to seal with: the run refuses to start and reads nothing.
func TestReencrypt_NoCurrentKeyRefuses(t *testing.T) {
	f := newFake()
	f.add("a", "c", "x")
	for _, rg := range []secret.Ring{nil, &secret.Cipher{}} {
		_, err := run(t, &secret.Reencryptor{Ring: rg}, f, secret.Options{})
		if !errors.Is(err, secret.ErrNoCurrentKey) || f.rowCalls != 0 {
			t.Fatalf("expected refusal, got %v (reads=%d)", err, f.rowCalls)
		}
	}
}

// A corrupt stored value is classed corrupt, never rewritten.
func TestReencrypt_CorruptValue(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "c", "v1:k1:!!!not-base64!!!")
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil || res.ByReason[secret.ReasonCorrupt] != 1 || f.swaps != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

// A row with several values moves all of them together or none (one bad value fails the whole row).
func TestReencrypt_MultiValueRowIsAtomic(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("ok", "c", seal(t, oldRing(t), "p1", "c"), seal(t, c, "p2", "c"), seal(t, oldRing(t), "p3", "c"))
	f.add("bad", "c", seal(t, oldRing(t), "p1", "c"), seal(t, oldRing(t), "p2", "other"))
	keepNew := f.get("ok")[1]
	bad := f.get("bad")
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil || res.Done != 1 || res.Failed != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := f.get("ok"); keyOf(got[0]) != "k2" || got[1] != keepNew || keyOf(got[2]) != "k2" {
		t.Fatalf("ok row: %v", got)
	}
	if got := f.get("bad"); got[0] != bad[0] || got[1] != bad[1] {
		t.Fatal("a failed row must be left exactly as it was")
	}
}

// A concurrent edit wins: the swap changes nothing, the row is a conflict, and the edit survives.
func TestReencrypt_ConcurrentEditWins(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "c", seal(t, oldRing(t), "pa", "c"))
	edited := seal(t, c, "edited", "c")
	f.beforeSwap = func(id string) {
		f.mu.Lock()
		f.rows[id] = []string{edited}
		f.mu.Unlock()
	}
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil || res.Conflicts != 1 || res.Done != 0 || res.Failed != 0 || f.get("a")[0] != edited {
		t.Fatalf("%+v %v", res, err)
	}
}

// Dry run counts but writes nothing, and still finds the failing rows.
func TestReencrypt_DryRun(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("a", "c", seal(t, oldRing(t), "pa", "c"))
	f.add("b", "c", seal(t, oldRing(t), "pb", "other"))
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{DryRun: true})
	if err != nil || f.swaps != 0 || res.Done != 1 || res.Failed != 1 || !res.DryRun {
		t.Fatalf("%+v %v", res, err)
	}
}

// One run per family: a second start while the first is inside is refused; another family is independent.
func TestReencrypt_LockPerFamily(t *testing.T) {
	c := bothRing(t)
	lock := secret.NewMemoryLocker()
	inside := make(chan struct{})
	release := make(chan struct{})
	f := newFake()
	f.add("a", "c", seal(t, oldRing(t), "pa", "c"))
	f.beforeSwap = func(string) { close(inside); <-release }
	r := &secret.Reencryptor{Ring: c, Locker: lock}
	done := make(chan error, 1)
	go func() { _, err := r.Run(context.Background(), f, secret.Options{}); done <- err }()
	<-inside
	if _, err := run(t, r, newFake(), secret.Options{}); !errors.Is(err, secret.ErrAlreadyRunning) {
		t.Fatalf("second run of the same family must be refused, got %v", err)
	}
	other := &otherFamily{newFake()}
	if _, err := run(t, r, other, secret.Options{}); err != nil {
		t.Fatalf("another family must run: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type otherFamily struct{ *fakeSource }

func (otherFamily) Family() string { return "other" }

// The result and its errors carry no plaintext and no ciphertext.
func TestReencrypt_ResultHoldsNoValues(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	bad := seal(t, oldRing(t), "plain-bad-value", "original")
	f.add("a", "renamed", bad)
	res, _ := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	out := fmt.Sprintf("%+v", res)
	for _, leak := range []string{"plain-bad-value", bad, strings.TrimPrefix(bad, "v1:k1:")} {
		if strings.Contains(out, leak) {
			t.Fatalf("result leaks a value: %s", out)
		}
	}
}

// 2. a failed value of a multi-value row is named by its place inside the row.
func TestReencrypt_FailedValueIsNamedInsideTheRow(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("row", "c", seal(t, oldRing(t), "p1", "c"), seal(t, oldRing(t), "p2", "other"))
	f.refs = []string{"variant 0, device \"web\", env \"A\"", "variant 1, device \"db\", env \"B\""}
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil || len(res.FailedRows) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := res.FailedRows[0].Detail; got != f.refs[1] {
		t.Fatalf("detail %q", got)
	}
}

// 5. nothing from stored data can reach a terminal as a control sequence: key ids, row ids and refs are cleaned.
func TestReencrypt_StoredTextIsCleanedForDisplay(t *testing.T) {
	c := bothRing(t)
	f := newFake()
	f.add("row\x1b[2J-id", "c", "v1:evil\x1b[31m\nkey:AAAA")
	f.refs = []string{"dev\x1b]0;title\x07"}
	res, err := run(t, &secret.Reencryptor{Ring: c}, f, secret.Options{})
	if err != nil || len(res.FailedRows) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	got := res.FailedRows[0]
	if got.KeyID != "invalid-id" {
		t.Fatalf("key id %q", got.KeyID)
	}
	for _, s := range []string{got.RowID, got.Detail, got.KeyID} {
		for _, r := range s {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("control character in %q", s)
			}
		}
	}
	if long := secret.Printable(strings.Repeat("x", 500), 120); len([]rune(long)) > 121 {
		t.Fatalf("not cut: %d", len(long))
	}
}
