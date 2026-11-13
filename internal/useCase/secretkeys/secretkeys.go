// Package secretkeys reads, never writes, the stored ciphertexts of the platform's keyed secrets and reports which key
// sealed each of them, how many are in the older form without a key id, and which cannot be opened with any loaded
// key. It is the operator's answer to "may this key be removed" and the start-up guard against a lost key, which is
// otherwise silent (a registry token reads as not set, an agent is skipped).
package secretkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	vpnUseCase "github.com/cybericebox/daemon/internal/useCase/vpn"
	"github.com/cybericebox/daemon/pkg/secret"
)

// Family is one key of the platform and the data sealed with it.
type Family string

const (
	FamilyPlatform Family = "platform" // PLATFORM_SECRETS_KEY
	FamilyVPN      Family = "vpn"      // VPN_SECRETS_KEY
	FamilyExercise Family = "exercise" // EXERCISE_SECRETS_KEY
)

// Store reads the stored ciphertexts; the repository implements it.
type Store interface {
	SMTPPasswords(ctx context.Context, visit func(id uuid.UUID, ciphertext string) error) error
	AgentKeys(ctx context.Context, visit func(id uuid.UUID, clientKey, accessKey string) error) error
	RegistrySettings(ctx context.Context, visit func(push, pull string) error) error
	EventRegistryTokens(ctx context.Context, visit func(id uuid.UUID, ciphertext string) error) error
	VPNConfigs(ctx context.Context, visit func(userID uuid.UUID, scope string, scopeRef uuid.NullUUID, ciphertext string) error) error
	ExerciseVersions(ctx context.Context, visit func(versionID uuid.UUID, variants []byte) error) error
}

// Ciphers are the loaded keyrings; a nil one means the family has no key configured.
type Ciphers struct {
	Platform, VPN, Exercise *secret.Cipher
}

func (c Ciphers) of(f Family) *secret.Cipher {
	switch f {
	case FamilyPlatform:
		return c.Platform
	case FamilyVPN:
		return c.VPN
	default:
		return c.Exercise
	}
}

// Problem kinds.
const (
	// KeyNotLoaded: the value names a key id that the keyring does not hold.
	KeyNotLoaded = "key_not_loaded"
	// NoKeyOpens: an older value without a key id that no loaded key opens.
	NoKeyOpens = "no_key_opens"
	// CannotOpen: the key is loaded but the value does not open with it and its row context (damaged value, or a
	// context that changed). Only found by a full check.
	CannotOpen = "cannot_open"
	// NoFamilyKey: the family has no key configured at all.
	NoFamilyKey = "family_key_not_configured"
)

// maxProblemRefs bounds the rows named per column in a report.
const maxProblemRefs = 20

// Problem is a value that cannot be opened.
type Problem struct {
	Kind  string
	KeyID string // the id the value names (KeyNotLoaded, CannotOpen)
	Ref   string // the row, enough to find it; never any part of the value
}

// Column is what one table column holds.
type Column struct {
	Family Family
	Name   string // table.column, or table.column@path for a value inside a document
	// Total counts the stored values (empty ones are not counted).
	Total int
	// ByKey counts the values by the key id they name.
	ByKey map[string]int
	// Legacy counts the values of the older form that names no key.
	Legacy int
	// Problems counts the values that cannot be opened, ProblemRefs names the first ones.
	Problems    int
	ProblemRefs []Problem
	// ByReason counts the problems per reason class (the classes the re-encryption job reports).
	ByReason map[secret.Reason]int
}

// RotationState is a row of the re-encryption progress table: counts only.
type RotationState struct {
	Family, Source, KeyID, State string
	Total, Done, Failed          int
	Conflicts                    int
	FailedByReason               map[string]int
	UpdatedAt                    time.Time
	ProcessGone                  bool // a running row whose process is gone: shown as interrupted
	LockUnknown                  bool // read from a replica: a running row cannot be told from a dead one
}

// Report is the state of every keyed secret the scan covers.
type Report struct {
	// Opened is true when every value was opened (a full check), false for the cheap check by key id only.
	Opened bool
	// Keys are the key ids loaded per family, the sealing key first; nil for a family without a key.
	Keys    map[Family][]string
	Columns []Column
	// Rotation is the state of the re-encryption runs; filled by the caller that has the progress table.
	Rotation []RotationState
}

// Problems counts the values that cannot be opened, over all columns.
func (r Report) Problems() int {
	n := 0
	for _, c := range r.Columns {
		n += c.Problems
	}
	return n
}

// StrictProblems is the count a script should act on: the values that cannot be opened and, when strict, also every
// re-encryption run that is not finished or that left failed rows. A key may be retired only at zero.
func (r Report) StrictProblems(strict bool) int {
	n := r.Problems()
	if strict {
		for _, s := range r.Rotation {
			if s.State != "finished" || s.Failed > 0 {
				n++
			}
		}
	}
	return n
}

// UseCase scans the stored secrets.
type UseCase struct {
	store   Store
	ciphers Ciphers
}

func New(store Store, ciphers Ciphers) *UseCase { return &UseCase{store: store, ciphers: ciphers} }

// NotScanned names the short-lived places that also hold ciphertext and are not covered: they expire by themselves.
var NotScanned = []string{
	"temporal_codes.data (queued notifications and auth deliveries, expire by their TTL)",
	"river_job.args (sealed archive passwords, removed when River prunes the job)",
	"session cookies (expire with the session)",
}

// Check scans every source. With open true each value is also opened with its row context (a full check, what `keys
// status` does); with false only the key id a value names is compared with the keyring (cheap, what the start-up guard
// does).
func (u *UseCase) Check(ctx context.Context, open bool) (Report, error) {
	rep := Report{Opened: open, Keys: map[Family][]string{}}
	for _, f := range []Family{FamilyPlatform, FamilyVPN, FamilyExercise} {
		if c := u.ciphers.of(f); c != nil {
			rep.Keys[f] = c.KeyIDs()
		}
	}
	cols := map[string]*Column{}
	column := func(f Family, name string) *Column {
		if c, ok := cols[name]; ok {
			return c
		}
		c := &Column{Family: f, Name: name, ByKey: map[string]int{}}
		cols[name] = c
		return c
	}
	record := func(f Family, name, ref, ciphertext string, context []byte) {
		if ciphertext == "" {
			return
		}
		col := column(f, name)
		col.Total++
		id, legacy := secret.KeyID(ciphertext)
		shown := secret.SafeKeyID(id)
		ref = secret.Printable(ref, 160)
		if legacy {
			col.Legacy++
		} else {
			col.ByKey[shown]++
		}
		var problem *Problem
		reason := secret.ReasonAAD
		switch cipher := u.ciphers.of(f); {
		case cipher == nil:
			problem = &Problem{Kind: NoFamilyKey, Ref: ref}
		case !legacy && !cipher.HasKey(id):
			problem = &Problem{Kind: KeyNotLoaded, KeyID: shown, Ref: ref}
			reason = secret.ReasonUnknownKey
		case open:
			if _, err := cipher.DecryptWithContext(ciphertext, context); err != nil {
				reason = secret.ReasonOf(err)
				if legacy {
					problem = &Problem{Kind: NoKeyOpens, Ref: ref}
				} else {
					problem = &Problem{Kind: CannotOpen, KeyID: shown, Ref: ref}
				}
			}
		}
		if problem != nil {
			col.Problems++
			if col.ByReason == nil {
				col.ByReason = map[secret.Reason]int{}
			}
			col.ByReason[reason]++
			if len(col.ProblemRefs) < maxProblemRefs {
				col.ProblemRefs = append(col.ProblemRefs, *problem)
			}
		}
	}

	if err := u.store.SMTPPasswords(ctx, func(id uuid.UUID, ct string) error {
		record(FamilyPlatform, "mail_smtp_configs.password_ciphertext", id.String(), ct, mailUseCase.PasswordContext(id))
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("read the SMTP passwords: %w", err)
	}
	if err := u.store.AgentKeys(ctx, func(id uuid.UUID, clientKey, accessKey string) error {
		record(FamilyPlatform, "infrastructure_agents.client_key_ciphertext", id.String(), clientKey, infraModel.AgentSecretContext(id, "key"))
		record(FamilyPlatform, "infrastructure_agents.access_private_key_ciphertext", id.String(), accessKey, infraModel.AgentSecretContext(id, "access"))
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("read the agent keys: %w", err)
	}
	if err := u.store.RegistrySettings(ctx, func(push, pull string) error {
		record(FamilyPlatform, "image_registry_settings.push_token_ciphertext", "settings", push, []byte(imageRegistryUseCase.PushContext))
		record(FamilyPlatform, "image_registry_settings.pull_token_ciphertext", "settings", pull, []byte(imageRegistryUseCase.PullContext))
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("read the registry settings: %w", err)
	}
	if err := u.store.EventRegistryTokens(ctx, func(id uuid.UUID, ct string) error {
		record(FamilyPlatform, "event_registry_secrets.token_ciphertext", id.String(), ct, []byte(imageRegistryUseCase.EventSecretContext(id)))
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("read the event registry tokens: %w", err)
	}
	if err := u.store.VPNConfigs(ctx, func(userID uuid.UUID, scope string, ref uuid.NullUUID, ct string) error {
		record(FamilyVPN, "user_vpn_configs.config", fmt.Sprintf("user %s, scope %s", userID, scope), ct,
			vpnUseCase.ConfigContext(userID, vpnModel.Scope(scope), ref))
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("read the VPN configs: %w", err)
	}
	if err := u.store.ExerciseVersions(ctx, func(versionID uuid.UUID, raw []byte) error {
		var variants []exerciseModel.Variant
		if err := json.Unmarshal(raw, &variants); err != nil {
			return fmt.Errorf("the variants document cannot be read: %w", err)
		}
		for _, variant := range variants {
			for _, device := range variant.Topology.Devices {
				for _, env := range device.Bootstrap.Env {
					if !env.Secret {
						continue
					}
					ref := fmt.Sprintf("version %s, variant %s, device %s, env %s", versionID, variant.ID, device.Name, env.Name)
					record(FamilyExercise, "exercise_versions.variants@topology.devices[].bootstrap.env[]", ref, env.Value,
						exerciseUseCase.EnvSecretContext(variant.ID, device.Name, env.Name))
				}
			}
		}
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("read the exercise secrets: %w", err)
	}

	for _, c := range cols {
		rep.Columns = append(rep.Columns, *c)
	}
	sort.Slice(rep.Columns, func(i, j int) bool { return rep.Columns[i].Name < rep.Columns[j].Name })
	return rep, nil
}
