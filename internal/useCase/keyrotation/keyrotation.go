// Package keyrotation moves the stored ciphertexts of a key family to the current key of its keyring, so an older key
// can be retired. It never deletes a key and never rewrites a value it cannot open; see pkg/secret (Reencryptor) for the
// rules of one source and `keys check` for the gate that says an old key may go.
package keyrotation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/keyRotationRepo"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	vpnUseCase "github.com/cybericebox/daemon/internal/useCase/vpn"
	"github.com/cybericebox/daemon/pkg/secret"
)

// ErrUnknownFamily: the family names no key of the platform.
var ErrUnknownFamily = errors.New("unknown key family")

// ErrFamilyKeyMissing: the family has no key configured, so nothing can be sealed.
var ErrFamilyKeyMissing = errors.New("the key family has no key configured")

// Contexts are the real context builders of every sealed value, the ones the code seals with.
func Contexts() keyRotationRepo.Contexts {
	return keyRotationRepo.Contexts{
		SMTP:          mailUseCase.PasswordContext,
		AgentKey:      func(id uuid.UUID) []byte { return infraModel.AgentSecretContext(id, "key") },
		AgentAccess:   func(id uuid.UUID) []byte { return infraModel.AgentSecretContext(id, "access") },
		RegistryPush:  []byte(imageRegistryUseCase.PushContext),
		RegistryPull:  []byte(imageRegistryUseCase.PullContext),
		EventRegistry: func(id uuid.UUID) []byte { return []byte(imageRegistryUseCase.EventSecretContext(id)) },
		VPN: func(userID uuid.UUID, scope string, ref uuid.NullUUID) []byte {
			return vpnUseCase.ConfigContext(userID, vpnModel.Scope(scope), ref)
		},
		ExerciseEnv: exerciseUseCase.EnvSecretContext,
	}
}

// Store is the database side; keyRotationRepo.Repository implements it.
type Store interface {
	Sources(family string) []secret.Source
	Lock(ctx context.Context, family string) (func(), error)
	SaveProgress(ctx context.Context, p secret.Progress, state string, startedAt time.Time) error
}

// Rings are the loaded keyrings by family; a nil one means no key is configured.
type Rings map[string]*secret.Cipher

// Options of one run.
type Options struct {
	DryRun bool
	// BatchSize and Pause tune the load; zero means the defaults of the job.
	BatchSize int
	Pause     time.Duration
}

// UseCase runs the re-encryption.
type UseCase struct {
	store Store
	rings Rings
	now   func() time.Time
}

func New(store Store, rings Rings) *UseCase {
	return &UseCase{store: store, rings: rings, now: time.Now}
}

// Keys tells which key of the family's keyring will seal and which only read, so an operator sees a wrong ring order
// (the old key first) before anything is written.
func (u *UseCase) Keys(family string) (sealing string, readOnly []string, err error) {
	if len(u.store.Sources(family)) == 0 {
		return "", nil, fmt.Errorf("%w: %q", ErrUnknownFamily, family)
	}
	ring := u.rings[family]
	if ring == nil {
		return "", nil, fmt.Errorf("%w: %q", ErrFamilyKeyMissing, family)
	}
	ids := ring.KeyIDs()
	return ids[0], ids[1:], nil
}

// Report is the outcome of a run over a family.
type Report struct {
	Family  string
	KeyID   string
	DryRun  bool
	Sources []secret.Result
	// ProgressNotRecorded is true when a write of the progress table failed during the run: the rows were handled, but
	// the table is stale and must not be trusted for this run.
	ProgressNotRecorded bool
	warned              map[string]bool
}

// Failed counts the rows that could not be opened or verified, over all sources.
func (r Report) Failed() int {
	n := 0
	for _, s := range r.Sources {
		n += s.Failed
	}
	return n
}

// Run re-encrypts every source of the family, one source after the other, under one lock for the whole family. A
// store error or a cancel stops the run with the partial report; rows already written stay written and readable. Rows
// that cannot be opened are listed in the report and left as they are.
func (u *UseCase) Run(ctx context.Context, family string, opts Options) (Report, error) {
	rep := Report{Family: family, DryRun: opts.DryRun}
	sources := u.store.Sources(family)
	if len(sources) == 0 {
		return rep, fmt.Errorf("%w: %q", ErrUnknownFamily, family)
	}
	ring := u.rings[family]
	if ring == nil {
		return rep, fmt.Errorf("%w: %q", ErrFamilyKeyMissing, family)
	}
	rep.KeyID = ring.CurrentKeyID()
	unlock, err := u.store.Lock(ctx, family)
	if err != nil {
		return rep, err
	}
	defer unlock()

	for _, src := range sources {
		started := u.now()
		r := &secret.Reencryptor{
			Ring: ring, BatchSize: opts.BatchSize, Pause: opts.Pause,
			Report: func(p secret.Progress) {
				if opts.DryRun {
					return // a dry run leaves no trace in the progress table
				}
				u.save(ctx, &rep, p, keyRotationRepo.StateRunning, started)
			},
			Now: u.now,
		}
		if !opts.DryRun {
			u.save(ctx, &rep, secret.Progress{Family: family, Source: src.Name(), KeyID: rep.KeyID, UpdatedAt: started},
				keyRotationRepo.StateRunning, started)
		}
		res, runErr := r.Run(ctx, src, secret.Options{DryRun: opts.DryRun})
		rep.Sources = append(rep.Sources, res)
		if !opts.DryRun {
			state := keyRotationRepo.StateFinished
			switch {
			case runErr == nil && (res.Failed > 0 || res.Conflicts > 0):
				state = keyRotationRepo.StateFinishedWithFailures
			case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
				state = keyRotationRepo.StateInterrupted
			case runErr != nil:
				state = keyRotationRepo.StateFailed
			}
			res.UpdatedAt = u.now()
			// The final state is written even when the run's context is already cancelled.
			u.save(context.WithoutCancel(ctx), &rep, res.Progress, state, started)
		}
		if runErr != nil {
			return rep, runErr
		}
	}
	return rep, nil
}

// save writes the progress row. A failed write must not stop the job (the rows are what matters), but it is never
// silent: one warning per source with no value in it, and the report says the table was not recorded.
func (u *UseCase) save(ctx context.Context, rep *Report, p secret.Progress, state string, started time.Time) {
	err := u.store.SaveProgress(ctx, p, state, started)
	if err == nil {
		return
	}
	if rep.warned == nil {
		rep.warned = map[string]bool{}
	}
	if !rep.warned[p.Source] {
		rep.warned[p.Source] = true
		log.Warn().Str("family", p.Family).Str("source", p.Source).Str("error_class", errorClass(err)).
			Msg("Key re-encryption: the progress table could not be written; the status table is stale for this run")
	}
	rep.ProgressNotRecorded = true
}

// errorClass names an error without any text that could carry a value.
func errorClass(err error) string {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.As(err, &pg):
		return "postgres_" + pg.Code
	default:
		return "store_error"
	}
}
