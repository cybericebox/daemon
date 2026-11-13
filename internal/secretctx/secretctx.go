// Package secretctx builds the associated data (the "context") that every stored secret is sealed with.
//
// A ciphertext opens only with the context it was sealed under, so a value copied to another row, another purpose or
// another variable does not open there. The context of a value is part of what the stored data means: changing a string
// here makes every value sealed under the old string unreadable. The builders therefore reproduce, byte for byte, the
// strings the code used before they were gathered here (testdata/context_fixtures.json holds ciphertexts sealed by that
// older code, and a test opens them), and a change of a context is a data migration, never an edit.
//
// This is the only place a context is written. tools/checksecretctx fails the build when another package builds one, and
// pkg/secret refuses an empty context.
package secretctx

import (
	"fmt"

	"github.com/gofrs/uuid"
)

// The contexts of the two registry tokens of the settings, which are one row and so have no id to bind to.
const (
	RegistryPush = "image-registry:push"
	RegistryPull = "image-registry:pull"
)

// SMTPPassword binds the password of a mail provider to its row.
func SMTPPassword(id uuid.UUID) []byte { return []byte(fmt.Sprintf("mail.smtp:%s", id)) }

// AgentSecret binds a secret of an infrastructure agent ("key": the mTLS client key, "access": the lab-link signing
// key) to the agent.
func AgentSecret(id uuid.UUID, field string) []byte {
	return []byte("infra.agent:" + id.String() + ":" + field)
}

// EventRegistry binds the registry token of an event to its row.
func EventRegistry(id uuid.UUID) []byte { return []byte("event-registry:" + id.String()) }

// VPNConfig binds a stored VPN client config to its user, scope and scope reference (empty for a test config).
func VPNConfig(userID uuid.UUID, scope string, scopeRef uuid.NullUUID) []byte {
	ref := ""
	if scopeRef.Valid {
		ref = scopeRef.UUID.String()
	}
	return []byte(fmt.Sprintf("vpn-config:%s:%v:%s", userID, scope, ref))
}

// ExerciseEnv binds a secret env var of an exercise to its variant, device and name: a value copied to another variable
// does not open there. The device and env names are free text of the author; the env name is limited to
// [A-Za-z_][A-Za-z0-9_]* and a device name may not hold ':', so the parts cannot run into one another.
func ExerciseEnv(variantID uuid.UUID, device, name string) []byte {
	return []byte("exercise-env:" + variantID.String() + ":" + device + ":" + name)
}

// NotifyPayload binds a queued notification payload to its dispatch.
func NotifyPayload(dispatchID uuid.UUID) []byte {
	return []byte("notify-payload:" + dispatchID.String())
}

// AuthDelivery binds a queued sign-in mail command to its delivery.
func AuthDelivery(id uuid.UUID) []byte { return []byte("auth-delivery:" + id.String()) }

// ArchiveImportPassword binds the sealed password of an archive import job to the job.
func ArchiveImportPassword(jobID uuid.UUID) []byte {
	return []byte("exercise-import:password:" + jobID.String())
}

// ArchiveExportPassword binds the sealed password of an archive export job to the job.
func ArchiveExportPassword(jobID uuid.UUID) []byte {
	return []byte("exercise-export:password:" + jobID.String())
}

// SessionTicket is the context of the session cookie. A cookie is opened before anyone is signed in, so there is no row to
// bind it to: the context only keeps a ticket from opening as any other value.
func SessionTicket() []byte { return []byte("cybericebox/session-ticket/v1") }
