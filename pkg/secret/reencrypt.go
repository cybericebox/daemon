package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

// Re-encryption moves stored ciphertexts from older keys to the current one, so that an old key can be retired.
//
// The job knows nothing about tables: a Source hands it rows (id plus the ciphertexts of the row with the context
// each was sealed under) and takes a rewritten row back with a compare-and-swap. Rules:
//   - a row is rewritten only when one of its values needs rotation; values already on the current key stay byte for byte;
//   - every new value is opened again with its context and compared to the old plaintext before anything is written;
//   - the write is a compare-and-swap on the old values: a concurrent edit wins and the row is retried by the next run;
//   - a row that cannot be opened is counted failed with a reason class and is never touched;
//   - nothing is deleted, no key is touched; the output never carries a plaintext or a ciphertext.

// Reason is the class of a failed row.
type Reason string

const (
	ReasonUnknownKey Reason = "unknown_key_id"
	ReasonAAD        Reason = "aad_mismatch"
	ReasonVerify     Reason = "verify_failed"
	ReasonCorrupt    Reason = "corrupt"
)

// Reasons lists every reason in a fixed order, for reports.
var Reasons = []Reason{ReasonUnknownKey, ReasonAAD, ReasonVerify, ReasonCorrupt}

var (
	// ErrAlreadyRunning: another run of the same family holds the lock.
	ErrAlreadyRunning = errors.New("secret: re-encryption of this family is already running")
	// ErrNoCurrentKey: the ring has no key to seal with.
	ErrNoCurrentKey = errors.New("secret: no current key in the keyring")
	// ErrStore wraps an error of the Source (database), which stops the run.
	ErrStore = errors.New("secret: store error")
)

// Item is one stored ciphertext of a row with the context it was sealed under.
type Item struct {
	Ciphertext string
	Context    []byte
}

// Row is one unit of rewriting: all items of a row are written together or not at all.
type Row struct {
	ID    string
	Items []Item
	// Unreadable marks a row whose stored document cannot even be parsed to find its values; it is failed as corrupt.
	Unreadable bool
}

// Source gives rows of one persistent store to the job.
type Source interface {
	// Family names the key family (a lock and a report unit), for example "mail".
	Family() string
	// Name names the source inside the family (a table and column), for reports.
	Name() string
	// Rows returns up to limit rows with id greater than after, ordered by id; an empty slice ends the pass.
	Rows(ctx context.Context, after string, limit int) ([]Row, error)
	// Swap replaces the old ciphertexts of the row with the new ones in one write, only if the row still holds the
	// old ones. swapped is false when the row changed meanwhile.
	Swap(ctx context.Context, id string, oldValues, newValues []string) (swapped bool, err error)
}

// Ring is what the job needs from a Cipher.
type Ring interface {
	Sealer
	NeedsRotation(encoded string) bool
	CurrentKeyID() string
	KeyIDs() []string
}

// Locker serialises runs per family. Lock returns ErrAlreadyRunning when the family is taken.
type Locker interface {
	Lock(ctx context.Context, family string) (unlock func(), err error)
}

// Failed is a row that could not be opened or verified.
type Failed struct {
	Source string
	RowID  string
	KeyID  string // the key id named by the failing value, empty when it names none
	Reason Reason
}

// Progress is the state of one run, reported after every batch.
type Progress struct {
	Family    string
	Source    string
	KeyID     string // the current key the run moves rows to
	Total     int    // rows scanned so far
	Done      int    // rows rewritten
	Failed    int
	Conflicts int
	// ByReason counts the failed rows per reason class.
	ByReason  map[Reason]int
	DryRun    bool
	UpdatedAt time.Time
}

// Result is the outcome of a run.
type Result struct {
	Progress
	AlreadyCurrent int
	FailedRows     []Failed
}

// Reencryptor runs the job.
type Reencryptor struct {
	Ring      Ring
	BatchSize int           // rows per batch, 100 when zero
	Pause     time.Duration // between batches
	Locker    Locker        // optional
	// Sleep waits between batches; time-based by default. It returns early with the context error.
	Sleep func(ctx context.Context, d time.Duration) error
	// Report receives the progress after every batch (and once at the end). It must not keep values.
	Report func(Progress)
	Now    func() time.Time
}

// Options of one run.
type Options struct {
	// DryRun opens and checks every row and counts what would be rewritten, writing nothing.
	DryRun bool
}

const defaultBatchSize = 100

func (r *Reencryptor) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reencryptor) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run moves every row of the source that is not on the current key to the current key. On a context cancel or a
// store error it returns the partial result with the error; the rows already written stay written and readable.
func (r *Reencryptor) Run(ctx context.Context, src Source, opts Options) (Result, error) {
	res := Result{ByReason: map[Reason]int{}}
	res.Family, res.Source, res.DryRun = src.Family(), src.Name(), opts.DryRun
	if r.Ring == nil || len(r.Ring.KeyIDs()) == 0 {
		return res, ErrNoCurrentKey
	}
	res.KeyID = r.Ring.CurrentKeyID()

	if r.Locker != nil {
		unlock, err := r.Locker.Lock(ctx, src.Family())
		if err != nil {
			return res, err
		}
		defer unlock()
	}

	batch := r.BatchSize
	if batch <= 0 {
		batch = defaultBatchSize
	}
	after := ""
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !first && r.Pause > 0 {
			if err := r.sleep(ctx, r.Pause); err != nil {
				return res, err
			}
		}
		first = false
		rows, err := src.Rows(ctx, after, batch)
		if err != nil {
			return res, fmt.Errorf("%w: %s: reading rows: %w", ErrStore, src.Name(), err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			after = row.ID
			if err := r.row(ctx, src, row, opts, &res); err != nil {
				return res, err
			}
		}
		r.report(&res)
	}
	r.report(&res)
	return res, nil
}

func (r *Reencryptor) report(res *Result) {
	res.UpdatedAt = r.now()
	if r.Report != nil {
		p := res.Progress
		p.ByReason = make(map[Reason]int, len(res.ByReason))
		for k, v := range res.ByReason {
			p.ByReason[k] = v
		}
		r.Report(p)
	}
}

func (r *Reencryptor) fail(res *Result, src Source, id, keyID string, reason Reason) {
	res.Failed++
	res.ByReason[reason]++
	res.FailedRows = append(res.FailedRows, Failed{Source: src.Name(), RowID: id, KeyID: keyID, Reason: reason})
}

func (r *Reencryptor) row(ctx context.Context, src Source, row Row, opts Options, res *Result) error {
	res.Total++
	if row.Unreadable {
		r.fail(res, src, row.ID, "", ReasonCorrupt)
		return nil
	}
	oldValues := make([]string, len(row.Items))
	newValues := make([]string, len(row.Items))
	rotate := false
	for i, it := range row.Items {
		oldValues[i], newValues[i] = it.Ciphertext, it.Ciphertext
		if !r.Ring.NeedsRotation(it.Ciphertext) {
			continue
		}
		rotate = true
		keyID, _ := KeyID(it.Ciphertext)
		plain, err := r.Ring.DecryptWithContext(it.Ciphertext, it.Context)
		if err != nil {
			r.fail(res, src, row.ID, keyID, classify(err))
			return nil
		}
		sealed, err := r.Ring.EncryptWithContext(plain, it.Context)
		if err != nil || !r.verified(sealed, it.Context, plain) {
			r.fail(res, src, row.ID, keyID, ReasonVerify)
			return nil
		}
		newValues[i] = sealed
	}
	if !rotate {
		res.AlreadyCurrent++
		return nil
	}
	if opts.DryRun {
		res.Done++
		return nil
	}
	swapped, err := src.Swap(ctx, row.ID, oldValues, newValues)
	if err != nil {
		return fmt.Errorf("%w: %s: writing a row: %w", ErrStore, src.Name(), err)
	}
	if !swapped {
		res.Conflicts++
		return nil
	}
	res.Done++
	return nil
}

// verified opens the new value again and checks it names the current key and holds the same plaintext.
func (r *Reencryptor) verified(sealed string, context, plain []byte) bool {
	if id, legacy := KeyID(sealed); legacy || id != r.Ring.CurrentKeyID() {
		return false
	}
	back, err := r.Ring.DecryptWithContext(sealed, context)
	return err == nil && bytes.Equal(back, plain)
}

func classify(err error) Reason {
	switch {
	case errors.Is(err, ErrUnknownKey):
		return ReasonUnknownKey
	case errors.Is(err, ErrCorrupt):
		return ReasonCorrupt
	default:
		// A wrong context and a wrong key both fail authentication; the value names no way to tell them apart.
		return ReasonAAD
	}
}

// MemoryLocker is a Locker inside one process: it serialises runs per family and is what tests use.
type MemoryLocker struct {
	held chan struct{}
	m    map[string]bool
}

// NewMemoryLocker returns a ready MemoryLocker.
func NewMemoryLocker() *MemoryLocker {
	l := &MemoryLocker{held: make(chan struct{}, 1), m: map[string]bool{}}
	l.held <- struct{}{}
	return l
}

func (l *MemoryLocker) Lock(_ context.Context, family string) (func(), error) {
	<-l.held
	defer func() { l.held <- struct{}{} }()
	if l.m[family] {
		return nil, ErrAlreadyRunning
	}
	l.m[family] = true
	return func() {
		<-l.held
		delete(l.m, family)
		l.held <- struct{}{}
	}, nil
}
