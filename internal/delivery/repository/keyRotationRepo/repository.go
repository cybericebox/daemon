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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/pkg/secret"
)

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
		&exerciseVersions{pool: p, env: c.ExerciseEnv},
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
	walkSecretEnvs(doc, func(variantID uuid.UUID, device, name string, env map[string]any) {
		if v, _ := env["value"].(string); v != "" {
			items = append(items, secret.Item{Ciphertext: v, Context: s.env(variantID, device, name)})
		}
	})
	return items
}

// Swap rewrites the secret values of one version and, in the same transaction, every identical value of the other
// versions of the same exercise (the draft copied from the published version, checkpoints): the published version
// and its draft are compared by raw jsonb equality, so equal documents must stay equal, also across an interruption.
// "Identical" means the same ciphertext under the same context; a value of another row that was not readable under its
// own context is never touched.
func (s *exerciseVersions) Swap(ctx context.Context, id string, oldValues, newValues []string) (bool, error) {
	if len(oldValues) != len(newValues) {
		return false, errors.New("old and new values differ in number")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var exerciseID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT exercise_id FROM exercise_versions WHERE id = $1::uuid`, id).Scan(&exerciseID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	// The same lock order as a save of the exercise: the exercise first, then its versions by id.
	if _, err = tx.Exec(ctx, `SELECT 1 FROM exercises WHERE id = $1 FOR UPDATE`, exerciseID); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT id, variants FROM exercise_versions WHERE exercise_id = $1 ORDER BY id FOR UPDATE`, exerciseID)
	if err != nil {
		return false, err
	}
	type version struct {
		id  uuid.UUID
		raw []byte
	}
	var versions []version
	for rows.Next() {
		var v version
		if err = rows.Scan(&v.id, &v.raw); err != nil {
			rows.Close()
			return false, err
		}
		versions = append(versions, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return false, err
	}

	// The version being swapped: compare every value with what the job read (compare-and-swap), and record each
	// (context, old value) -> new value.
	type docOf struct {
		v   version
		doc []any
	}
	var target *docOf
	for _, v := range versions {
		if v.id.String() != id {
			continue
		}
		doc, perr := parseVariants(v.raw)
		if perr != nil {
			return false, nil
		}
		target = &docOf{v: v, doc: doc}
	}
	if target == nil {
		return false, nil
	}
	replace := map[string]string{}
	i, same := 0, true
	walkSecretEnvs(target.doc, func(variantID uuid.UUID, device, name string, env map[string]any) {
		v, _ := env["value"].(string)
		if v == "" {
			return
		}
		if i >= len(oldValues) {
			same = false
			return
		}
		key := string(s.env(variantID, device, name)) + "\x00" + oldValues[i]
		switch {
		case v == oldValues[i]:
			replace[key] = newValues[i]
			env["value"] = newValues[i]
		case s.wasWritten(key, v):
			// already rewritten with its sibling earlier in this run
		default:
			same = false
		}
		i++
	})
	if !same || i != len(oldValues) {
		return false, nil
	}
	if len(replace) > 0 {
		if err = s.write(ctx, tx, target.v.id, target.v.raw, target.doc); err != nil {
			return false, err
		}
	}

	for _, v := range versions {
		if v.id == target.v.id {
			continue
		}
		doc, perr := parseVariants(v.raw)
		if perr != nil {
			continue
		}
		changed := false
		walkSecretEnvs(doc, func(variantID uuid.UUID, device, name string, env map[string]any) {
			cur, _ := env["value"].(string)
			if next, ok := replace[string(s.env(variantID, device, name))+"\x00"+cur]; ok && cur != "" {
				env["value"] = next
				changed = true
			}
		})
		if changed {
			if err = s.write(ctx, tx, v.id, v.raw, doc); err != nil {
				return false, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.written == nil {
		s.written = map[string]string{}
	}
	for k, v := range replace {
		s.written[k] = v
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
	for _, v := range doc {
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
					visit(vid, str(device["name"]), str(env["name"]), env)
				}
			}
		}
	}
}

func str(v any) string { s, _ := v.(string); return s }

// Lock takes the advisory lock of a family on its own connection, so one run per family goes at a time across
// every daemon process. It returns ErrAlreadyRunning when the family is taken.
func (r *Repository) Lock(ctx context.Context, family string) (func(), error) {
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
	StateRunning     = "running"
	StateFinished    = "finished"
	StateInterrupted = "interrupted"
	StateFailed      = "failed"
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
(family, source, key_id, state, total, done, failed, conflicts, failed_by_reason, started_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11)
ON CONFLICT (family, source) DO UPDATE SET key_id = EXCLUDED.key_id, state = EXCLUDED.state, total = EXCLUDED.total,
 done = EXCLUDED.done, failed = EXCLUDED.failed, conflicts = EXCLUDED.conflicts, failed_by_reason = EXCLUDED.failed_by_reason,
 started_at = EXCLUDED.started_at, updated_at = EXCLUDED.updated_at`,
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
}

// Progress reads the progress table, ordered by family and source.
func (r *Repository) Progress(ctx context.Context) ([]StoredProgress, error) {
	rows, err := r.pool.Query(ctx, `SELECT family, source, key_id, state, total, done, failed, conflicts, failed_by_reason, started_at, updated_at
FROM key_rotation_progress ORDER BY family, source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredProgress
	for rows.Next() {
		var sp StoredProgress
		var reasons []byte
		if err := rows.Scan(&sp.Family, &sp.Source, &sp.KeyID, &sp.State, &sp.Total, &sp.Done, &sp.Failed, &sp.Conflicts, &reasons, &sp.StartedAt, &sp.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(reasons, &sp.FailedByReason); err != nil {
			return nil, fmt.Errorf("progress of %s: %w", sp.Source, err)
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}
