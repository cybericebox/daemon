// Package keyRotationRepo is the database side of the re-encryption of stored secrets: the sources the job walks (one
// per table column), the lock that lets one run of a family go at a time, and the progress table. It writes ciphertext
// only through a compare-and-swap on the value it read, so a concurrent edit always wins.
package keyRotationRepo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/pkg/secret"
)

// MinPoolConns is the number of connections a run needs: the family lock, a swap transaction and a progress write.
const MinPoolConns = 3

// ErrPoolTooSmall: the connection pool cannot hold a run.
var ErrPoolTooSmall = errors.New("connection pool too small")

// The key families, the same names as in the keys status report.
const (
	FamilyPlatform = "platform"
	FamilyVPN      = "vpn"
	FamilyExercise = "exercise"
)

// Contexts build the authenticated context each sealed value was bound to. They are the same builders the code uses
// when it seals, handed in so this package does not depend on the use cases.
type Contexts struct {
	SMTP          func(id uuid.UUID) []byte
	AgentKey      func(id uuid.UUID) []byte
	AgentAccess   func(id uuid.UUID) []byte
	RegistryPush  []byte
	RegistryPull  []byte
	EventRegistry func(id uuid.UUID) []byte
	VPN           func(userID uuid.UUID, scope string, scopeRef uuid.NullUUID) []byte
	ExerciseEnv   func(variantID uuid.UUID, device, env string) []byte
}

type Repository struct {
	pool *pgxpool.Pool
	ctxs Contexts
	// LockTimeout bounds how long a swap waits for the exercise row, which an application writer may hold; a timeout is
	// a conflict for that row (the next run retries it), never a wait without end. Five seconds when zero.
	LockTimeout time.Duration
	// AfterLock is a test seam: it runs inside the exercise swap while its locks are held, before anything is written.
	AfterLock func()
}

func New(pool *pgxpool.Pool, ctxs Contexts) *Repository { return &Repository{pool: pool, ctxs: ctxs} }

// Sources lists the sources of a family; an unknown family has none.
func (r *Repository) Sources(family string) []secret.Source {
	var out []secret.Source
	for _, s := range r.allSources() {
		if s.Family() == family {
			out = append(out, s)
		}
	}
	return out
}

// Families lists the families that have sources.
func (r *Repository) Families() []string { return []string{FamilyPlatform, FamilyVPN, FamilyExercise} }

const afterID = `COALESCE(NULLIF($1, ''), '00000000-0000-0000-0000-000000000000')::uuid`

func (r *Repository) allSources() []secret.Source {
	c := r.ctxs
	p := r.pool
	single := func(family, name, query, swap string, scan func(pgx.Rows) (secret.Row, error)) secret.Source {
		return &column{pool: p, family: family, name: name, query: query, swap: swap, scan: scan}
	}
	idCt := func(ctxOf func(uuid.UUID) []byte) func(pgx.Rows) (secret.Row, error) {
		return func(rows pgx.Rows) (secret.Row, error) {
			var id uuid.UUID
			var ct string
			if err := rows.Scan(&id, &ct); err != nil {
				return secret.Row{}, err
			}
			return secret.Row{ID: id.String(), Items: []secret.Item{{Ciphertext: ct, Context: ctxOf(id)}}}, nil
		}
	}
	singleton := func(ctx []byte) func(pgx.Rows) (secret.Row, error) {
		return func(rows pgx.Rows) (secret.Row, error) {
			var ct string
			if err := rows.Scan(&ct); err != nil {
				return secret.Row{}, err
			}
			return secret.Row{ID: "settings", Items: []secret.Item{{Ciphertext: ct, Context: ctx}}}, nil
		}
	}
	return []secret.Source{
		single(FamilyPlatform, "mail_smtp_configs.password_ciphertext",
			`SELECT id, password_ciphertext FROM mail_smtp_configs WHERE password_ciphertext <> '' AND id > `+afterID+` ORDER BY id LIMIT $2`,
			`UPDATE mail_smtp_configs SET password_ciphertext = $3 WHERE id = $1::uuid AND password_ciphertext = $2`,
			idCt(c.SMTP)),
		single(FamilyPlatform, "infrastructure_agents.client_key_ciphertext",
			`SELECT id, client_key_ciphertext FROM infrastructure_agents WHERE client_key_ciphertext <> '' AND id > `+afterID+` ORDER BY id LIMIT $2`,
			`UPDATE infrastructure_agents SET client_key_ciphertext = $3 WHERE id = $1::uuid AND client_key_ciphertext = $2`,
			idCt(c.AgentKey)),
		single(FamilyPlatform, "infrastructure_agents.access_private_key_ciphertext",
			`SELECT id, access_private_key_ciphertext FROM infrastructure_agents WHERE access_private_key_ciphertext <> '' AND id > `+afterID+` ORDER BY id LIMIT $2`,
			`UPDATE infrastructure_agents SET access_private_key_ciphertext = $3 WHERE id = $1::uuid AND access_private_key_ciphertext = $2`,
			idCt(c.AgentAccess)),
		// The settings are one row without a uuid: it is returned only for the first page.
		single(FamilyPlatform, "image_registry_settings.push_token_ciphertext",
			`SELECT push_token_ciphertext FROM image_registry_settings WHERE push_token_ciphertext <> '' AND $1::text = '' LIMIT $2`,
			`UPDATE image_registry_settings SET push_token_ciphertext = $3 WHERE $1::text = 'settings' AND push_token_ciphertext = $2`,
			singleton(c.RegistryPush)),
		single(FamilyPlatform, "image_registry_settings.pull_token_ciphertext",
			`SELECT pull_token_ciphertext FROM image_registry_settings WHERE pull_token_ciphertext <> '' AND $1::text = '' LIMIT $2`,
			`UPDATE image_registry_settings SET pull_token_ciphertext = $3 WHERE $1::text = 'settings' AND pull_token_ciphertext = $2`,
			singleton(c.RegistryPull)),
		single(FamilyPlatform, "event_registry_secrets.token_ciphertext",
			`SELECT id, token_ciphertext FROM event_registry_secrets WHERE token_ciphertext <> '' AND id > `+afterID+` ORDER BY id LIMIT $2`,
			`UPDATE event_registry_secrets SET token_ciphertext = $3 WHERE id = $1::uuid AND token_ciphertext = $2`,
			idCt(c.EventRegistry)),
		single(FamilyVPN, "user_vpn_configs.config",
			`SELECT id, user_id, scope, scope_ref, config FROM user_vpn_configs WHERE config <> '' AND id > `+afterID+` ORDER BY id LIMIT $2`,
			`UPDATE user_vpn_configs SET config = $3 WHERE id = $1::uuid AND config = $2`,
			func(rows pgx.Rows) (secret.Row, error) {
				var id, userID uuid.UUID
				var scope, ct string
				var ref uuid.NullUUID
				if err := rows.Scan(&id, &userID, &scope, &ref, &ct); err != nil {
					return secret.Row{}, err
				}
				return secret.Row{ID: id.String(), Items: []secret.Item{{Ciphertext: ct, Context: c.VPN(userID, scope, ref)}}}, nil
			}),
		&exerciseVersions{pool: p, env: c.ExerciseEnv, afterLock: r.AfterLock, lockTimeout: r.LockTimeout},
	}
}

// column is a source of one text column, one row per stored value.
type column struct {
	pool         *pgxpool.Pool
	family, name string
	query, swap  string
	scan         func(pgx.Rows) (secret.Row, error)
}

func (s *column) Family() string { return s.family }
func (s *column) Name() string   { return s.name }

func (s *column) Rows(ctx context.Context, after string, limit int) ([]secret.Row, error) {
	rows, err := s.pool.Query(ctx, s.query, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secret.Row
	for rows.Next() {
		row, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *column) Swap(ctx context.Context, id string, oldValues, newValues []string) (bool, error) {
	if len(oldValues) != 1 || len(newValues) != 1 {
		return false, errors.New("a column source swaps one value")
	}
	tag, err := s.pool.Exec(ctx, s.swap, id, oldValues[0], newValues[0])
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// exerciseVersions is the source of the secret env values inside exercise_versions.variants (a jsonb document). A
// row is one version; its items are all its secret values in document order, and they are written back together with
// the rest of the document untouched.
type exerciseVersions struct {
	pool *pgxpool.Pool
	env  func(variantID uuid.UUID, device, env string) []byte

	// written remembers, in memory only and never printed, which new value this run gave to (context, old value), so a
	// row that an earlier swap already rewrote together with its sibling is recognised and not counted as a conflict.
	mu      sync.Mutex
	written map[string]string
	partly  map[string]bool // versions that received a shared value from the swap of a sibling

	afterLock   func()        // test seam: runs while the locks are held, before anything is written
	lockTimeout time.Duration // wait for the exercise row; zero means defaultLockTimeout
}

const defaultLockTimeout = 5 * time.Second

// lockConflict tells a failed lock (a timeout, a refused NOWAIT, a deadlock the server resolved against us) from a real
// error: the row is a conflict for this run and the next run tries it again.
func lockConflict(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.Code == "55P03" || pg.Code == "40P01")
}

func (s *exerciseVersions) Family() string { return FamilyExercise }
func (s *exerciseVersions) Name() string {
	return "exercise_versions.variants@topology.devices[].bootstrap.env[]"
}

func (s *exerciseVersions) Rows(ctx context.Context, after string, limit int) ([]secret.Row, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, variants FROM exercise_versions
WHERE id > `+afterID+` AND jsonb_path_exists(variants, '$[*].topology.devices[*].bootstrap.env[*] ? (@.secret == true)')
ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secret.Row
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		doc, err := parseVariants(raw)
		if err != nil {
			// A document that cannot be read is a failed row, not a stop.
			out = append(out, secret.Row{ID: id.String(), Unreadable: true})
			continue
		}
		out = append(out, secret.Row{ID: id.String(), Items: s.items(doc)})
	}
	return out, rows.Err()
}

func (s *exerciseVersions) items(doc []any) []secret.Item {
	var items []secret.Item
	walkSecretEnvsAt(doc, func(variant int, variantID uuid.UUID, device, name string, env map[string]any) {
		if v, _ := env["value"].(string); v != "" {
			items = append(items, secret.Item{Ciphertext: v, Context: s.env(variantID, device, name),
				Ref: fmt.Sprintf("variant %d, device %q, env %q", variant, device, name)})
		}
	})
	return items
}

// Swap rewrites the secret values of one version and, in the same transaction, every identical value of the other
// versions of the same exercise (the draft copied from the published version, checkpoints): the published version
// and its draft are compared by raw jsonb equality, so equal documents must stay equal, also across an interruption.
// "Identical" means the same ciphertext under the same context; a value of another row that was not readable under its
// own context is never touched (a row that fails for another reason can still receive a shared value; it is then
// reported as partly rotated, see PartlyRotated).
//
// Locks: the exercise row and the version rows that hold a secret, FOR NO KEY UPDATE, in the order a save of the
// exercise takes them (exercise first, then versions by id). The pass changes no key column, so foreign-key checks of
// other writers (a pin to an event, a file reference) do not wait. Round trips: BEGIN, one statement that locks the
// exercise and finds it, one that reads the versions with secrets, one UPDATE for the version and one per sibling that
// holds a shared value, COMMIT.
func (s *exerciseVersions) Swap(ctx context.Context, id string, oldValues, newValues []string) (bool, error) {
	return s.swap(ctx, id, oldValues, newValues, nil)
}

// SwapReusing is Swap that also looks, under the same lock, for a ciphertext of the current key with the same context
// in the other versions that opens to the same plaintext as the freshly sealed value, and writes that one instead.
// Equal content then ends with equal bytes also when a copy was made from a stale read while the pass was running.
func (s *exerciseVersions) SwapReusing(ctx context.Context, id string, oldValues, newValues []string,
	same func(itemContext []byte, candidate, fresh string) bool) (bool, error) {
	return s.swap(ctx, id, oldValues, newValues, same)
}

// PartlyRotated tells whether a shared value of this version was moved to the current key by the swap of a sibling.
func (s *exerciseVersions) PartlyRotated(rowID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partly[rowID]
}

type versionRow struct {
	id  uuid.UUID
	raw []byte
	doc []any
}

type slot struct {
	env map[string]any
	ctx []byte
	cur string
}

func (s *exerciseVersions) slots(doc []any) []slot {
	var out []slot
	walkSecretEnvs(doc, func(variantID uuid.UUID, device, name string, env map[string]any) {
		if v, _ := env["value"].(string); v != "" {
			out = append(out, slot{env: env, ctx: s.env(variantID, device, name), cur: v})
		}
	})
	return out
}

func (s *exerciseVersions) swap(ctx context.Context, id string, oldValues, newValues []string,
	same func(itemContext []byte, candidate, fresh string) bool) (bool, error) {
	if len(oldValues) != len(newValues) {
		return false, errors.New("old and new values differ in number")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// The only place the swap waits is the exercise row, while it holds no other lock, with a bound. The version rows are
	// taken with NOWAIT: a version held by an application writer makes this row a conflict at once. So the swap can never
	// be one side of a wait cycle, and never blocks an application writer for longer than its own short transaction.
	timeout := s.lockTimeout
	if timeout <= 0 {
		timeout = defaultLockTimeout
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = '%dms'`, timeout.Milliseconds())); err != nil {
		return false, err
	}
	var exerciseID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT e.id FROM exercises e JOIN exercise_versions v ON v.exercise_id = e.id
WHERE v.id = $1::uuid FOR NO KEY UPDATE OF e`, id).Scan(&exerciseID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || lockConflict(err) {
			return false, nil
		}
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT id, variants FROM exercise_versions WHERE exercise_id = $1
AND jsonb_path_exists(variants, '$[*].topology.devices[*].bootstrap.env[*] ? (@.secret == true)')
ORDER BY id FOR NO KEY UPDATE NOWAIT`, exerciseID)
	if err != nil {
		if lockConflict(err) {
			return false, nil
		}
		return false, err
	}
	var versions []versionRow
	for rows.Next() {
		var v versionRow
		if err = rows.Scan(&v.id, &v.raw); err != nil {
			rows.Close()
			return false, err
		}
		versions = append(versions, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		if lockConflict(err) {
			return false, nil
		}
		return false, err
	}
	if s.afterLock != nil {
		s.afterLock()
	}

	var target *versionRow
	for i := range versions {
		doc, perr := parseVariants(versions[i].raw)
		if perr != nil {
			continue
		}
		versions[i].doc = doc
		if versions[i].id.String() == id {
			target = &versions[i]
		}
	}
	if target == nil {
		return false, nil
	}

	// Compare every value with what the job read (compare-and-swap); a value that an earlier swap of this run already
	// moved together with its sibling is accepted as it is.
	slots := s.slots(target.doc)
	if len(slots) != len(oldValues) {
		return false, nil
	}
	keyOf := func(ctxBytes []byte, old string) string { return string(ctxBytes) + "\x00" + old }
	moved := make([]bool, len(slots))
	for i, sl := range slots {
		switch {
		case sl.cur == oldValues[i]:
			moved[i] = true
		case s.wasWritten(keyOf(sl.ctx, oldValues[i]), sl.cur):
		default:
			return false, nil
		}
	}

	// Reuse: the same content under the same context in another version, already on the current key.
	replace := map[string]string{}
	for i, sl := range slots {
		if !moved[i] {
			continue
		}
		next := newValues[i]
		if same != nil {
			for j := range versions {
				if versions[j].doc == nil || versions[j].id == target.id {
					continue
				}
				found := ""
				for _, other := range s.slots(versions[j].doc) {
					if string(other.ctx) == string(sl.ctx) && other.cur != oldValues[i] && other.cur != next && same(sl.ctx, other.cur, next) {
						found = other.cur
						break
					}
				}
				if found != "" {
					next = found
					break
				}
			}
		}
		replace[keyOf(sl.ctx, oldValues[i])] = next
		sl.env["value"] = next
	}
	if len(replace) > 0 {
		if err = s.write(ctx, tx, target.id, target.raw, target.doc); err != nil {
			return false, err
		}
	}

	touched := map[string]bool{}
	for i := range versions {
		v := &versions[i]
		if v.id == target.id || v.doc == nil {
			continue
		}
		changed := false
		for _, sl := range s.slots(v.doc) {
			if next, ok := replace[keyOf(sl.ctx, sl.cur)]; ok {
				sl.env["value"] = next
				changed = true
			}
		}
		if changed {
			if err = s.write(ctx, tx, v.id, v.raw, v.doc); err != nil {
				return false, err
			}
			touched[v.id.String()] = true
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.written == nil {
		s.written = map[string]string{}
		s.partly = map[string]bool{}
	}
	for k, v := range replace {
		s.written[k] = v
	}
	for k := range touched {
		s.partly[k] = true
	}
	s.mu.Unlock()
	return true, nil
}

func (s *exerciseVersions) wasWritten(key, current string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, ok := s.written[key]
	return ok && next == current
}

// write replaces a version's document if it still holds the bytes that were read; the rows are locked by the caller.
func (s *exerciseVersions) write(ctx context.Context, tx pgx.Tx, versionID uuid.UUID, read []byte, doc []any) error {
	next, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE exercise_versions SET variants = $3::jsonb WHERE id = $1 AND variants = $2::jsonb`,
		versionID, string(read), string(next))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("a locked version row changed")
	}
	return nil
}

func parseVariants(raw []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // numbers stay as written
	var doc []any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// walkSecretEnvs visits every env entry marked secret of every device of every variant, in document order.
func walkSecretEnvs(doc []any, visit func(variantID uuid.UUID, device, name string, env map[string]any)) {
	walkSecretEnvsAt(doc, func(_ int, variantID uuid.UUID, device, name string, env map[string]any) {
		visit(variantID, device, name, env)
	})
}

// walkSecretEnvsAt is walkSecretEnvs with the position of the variant in the document.
func walkSecretEnvsAt(doc []any, visit func(variant int, variantID uuid.UUID, device, name string, env map[string]any)) {
	for vi, v := range doc {
		variant, _ := v.(map[string]any)
		vid, _ := uuid.FromString(str(variant["id"]))
		topology, _ := variant["topology"].(map[string]any)
		devices, _ := topology["devices"].([]any)
		for _, d := range devices {
			device, _ := d.(map[string]any)
			bootstrap, _ := device["bootstrap"].(map[string]any)
			envs, _ := bootstrap["env"].([]any)
			for _, e := range envs {
				env, _ := e.(map[string]any)
				if secret, _ := env["secret"].(bool); secret {
					visit(vi, vid, str(device["name"]), str(env["name"]), env)
				}
			}
		}
	}
}

func str(v any) string { s, _ := v.(string); return s }

// Lock takes the advisory lock of a family on its own connection, so one run per family goes at a time across
// every daemon process. It returns ErrAlreadyRunning when the family is taken.
func (r *Repository) Lock(ctx context.Context, family string) (func(), error) {
	// The run keeps this connection for its whole length and needs at least two more (a swap transaction, the progress
	// write): a smaller pool would wait for itself forever.
	if max := r.pool.Config().MaxConns; max < MinPoolConns {
		return nil, fmt.Errorf("%w: the pool holds %d connection(s), re-encryption needs %d", ErrPoolTooSmall, max, MinPoolConns)
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var got bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('cybericebox.key-reencrypt:' || $1, 0))`, family).Scan(&got); err != nil {
		conn.Release()
		return nil, err
	}
	if !got {
		conn.Release()
		return nil, secret.ErrAlreadyRunning
	}
	return func() {
		// The unlock must run even when the run's context is already cancelled.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, `SELECT pg_advisory_unlock(hashtextextended('cybericebox.key-reencrypt:' || $1, 0))`, family); err != nil {
			conn.Conn().Close(uctx) // closing the session releases the lock
		}
		conn.Release()
	}, nil
}

// State of a source in the progress table.
const (
	StateRunning  = "running"
	StateFinished = "finished"
	// StateFinishedWithFailures: the run reached the end but left rows that cannot be opened, or rows that changed
	// meanwhile and wait for the next run. An old key is not free to go.
	StateFinishedWithFailures = "finished_with_failures"
	StateInterrupted          = "interrupted"
	StateFailed               = "failed"
)

// SaveProgress writes the counts of a source; it holds no value.
func (r *Repository) SaveProgress(ctx context.Context, p secret.Progress, state string, startedAt time.Time) error {
	reasons := map[string]int{}
	for k, v := range p.ByReason {
		reasons[string(k)] = v
	}
	raw, err := json.Marshal(reasons)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO key_rotation_progress
(family, source, key_id, state, total, done, failed, conflicts, failed_by_reason, started_at, updated_at, last_moved_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, CASE WHEN $6 > 0 THEN $11::timestamptz END)
ON CONFLICT (family, source) DO UPDATE SET key_id = EXCLUDED.key_id, state = EXCLUDED.state, total = EXCLUDED.total,
 done = EXCLUDED.done, failed = EXCLUDED.failed, conflicts = EXCLUDED.conflicts, failed_by_reason = EXCLUDED.failed_by_reason,
 started_at = EXCLUDED.started_at, updated_at = EXCLUDED.updated_at,
 last_moved_at = COALESCE(EXCLUDED.last_moved_at, key_rotation_progress.last_moved_at)`,
		p.Family, p.Source, p.KeyID, state, p.Total, p.Done, p.Failed, p.Conflicts, string(raw), startedAt, p.UpdatedAt)
	return err
}

// StoredProgress is a row of the progress table.
type StoredProgress struct {
	Family, Source, KeyID, State string
	Total, Done, Failed          int
	Conflicts                    int
	FailedByReason               map[string]int
	StartedAt, UpdatedAt         time.Time
	// LastMovedAt is when a run last moved a value to the current key; kept by runs that move nothing. Nil: never.
	LastMovedAt *time.Time
	// ProcessGone: the row says running but no process holds the family lock, so the run died without finishing.
	// State is then reported as interrupted.
	ProcessGone bool
	// LockUnknown: the table was read from a replica, which cannot tell a live run from a dead one.
	LockUnknown bool
}

// Progress reads the progress table, ordered by family and source.
func (r *Repository) Progress(ctx context.Context) ([]StoredProgress, error) {
	return r.ProgressIn(ctx, r.pool)
}

// Querier is what reading the progress needs: a pool or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ProgressIn reads the progress table (and the lock state) through q, so a caller can read it in the same snapshot as
// other tables.
func (r *Repository) ProgressIn(ctx context.Context, q Querier) ([]StoredProgress, error) {
	rows, err := q.Query(ctx, `SELECT family, source, key_id, state, total, done, failed, conflicts, failed_by_reason, started_at, updated_at, last_moved_at
FROM key_rotation_progress ORDER BY family, source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredProgress
	for rows.Next() {
		var sp StoredProgress
		var reasons []byte
		if err := rows.Scan(&sp.Family, &sp.Source, &sp.KeyID, &sp.State, &sp.Total, &sp.Done, &sp.Failed, &sp.Conflicts, &reasons, &sp.StartedAt, &sp.UpdatedAt, &sp.LastMovedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(reasons, &sp.FailedByReason); err != nil {
			return nil, fmt.Errorf("progress of %s: %w", sp.Source, err)
		}
		out = append(out, sp)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// A replica sees only its own locks: the run's lock is on the primary, so "no holder" proves nothing there. The
	// rows are then shown as stored, with the lock state unknown.
	var replica bool
	if err = q.QueryRow(ctx, `SELECT pg_is_in_recovery()`).Scan(&replica); err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for i := range out {
		if replica && out[i].State == StateRunning {
			out[i].LockUnknown = true
		}
		if replica {
			continue
		}
		if out[i].State != StateRunning {
			continue
		}
		h, known := held[out[i].Family]
		if !known {
			if h, err = r.lockHeld(ctx, q, out[i].Family); err != nil {
				return nil, err
			}
			held[out[i].Family] = h
		}
		if !h {
			out[i].State, out[i].ProcessGone = StateInterrupted, true
		}
	}
	return out, nil
}

// lockHeld tells whether some session holds the advisory lock of the family, without taking it (taking it for a moment
// would refuse a run that starts at that moment).
func (r *Repository) lockHeld(ctx context.Context, q Querier, family string) (bool, error) {
	var held bool
	// The two-int form of an advisory key is classid (high 32 bits) and objid (low 32 bits) with objsubid 1; the lock is
	// scoped to this database.
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND granted AND objsubid = 1
 AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
 AND ((classid::bigint << 32) | objid::bigint) = hashtextextended('cybericebox.key-reencrypt:' || $1, 0))`, family).Scan(&held)
	return held, err
}
