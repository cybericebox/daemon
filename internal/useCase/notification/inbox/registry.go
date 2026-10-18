package inboxUseCase

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// RegistryCredentialAlert is the snapshot of a registry credential the periodic check reports. It names the
// credential (host, repository), never its token.
type RegistryCredentialAlert struct {
	// Key ties the alert to its credential: "platform_push", "platform_pull" or "event_secret:<id>".
	Key string
	// Kind is the credential class: platform_push, platform_pull or event_secret.
	Kind string
	// Label is what the credential is for (registry host and repository, or the secret's host and prefix).
	Label string
	// EventID is set for an organizer secret (nil UUID for the platform tokens), with the event's labels.
	EventID   uuid.UUID
	EventName string
	EventTag  string
	// Reason is the reason code of the refusal.
	Reason string
	At     time.Time
}

func (a RegistryCredentialAlert) vars() map[string]any {
	eventID := ""
	if a.EventID != uuid.Nil {
		eventID = a.EventID.String()
	}
	return map[string]any{
		"label": a.Label, "reason": a.Reason,
		"event_id": eventID, "event_name": a.EventName, "event_tag": a.EventTag,
	}
}

// recipients are the people who can fix the credential: the platform admins for a platform token, the managers
// of the event (owner and moderators) for its secret.
func (r *RequestRouter) registryRecipients(ctx context.Context, a RegistryCredentialAlert) ([]uuid.UUID, inboxModel.RecipientRole, []dispatchModel.NotifyOption, error) {
	if a.EventID == uuid.Nil {
		admins, err := r.queries.ListPlatformAdminUserIDs(ctx)
		if err != nil {
			return nil, "", nil, fmt.Errorf("inbox requests: list platform admins: %w", err)
		}
		return admins, inboxModel.RoleAdmin, nil, nil
	}
	managers, err := r.queries.ListEventWriteManagerUserIDs(ctx, a.EventID)
	if err != nil {
		return nil, "", nil, fmt.Errorf("inbox requests: list event managers: %w", err)
	}
	return managers, inboxModel.RoleManager, []dispatchModel.NotifyOption{dispatchModel.WithEventScope(a.EventID)}, nil
}

// RegistryCredentialBroken asks the people who can fix the credential to do it. The check sends it once, on the
// transition to broken.
func (r *RequestRouter) RegistryCredentialBroken(ctx context.Context, a RegistryCredentialAlert) error {
	to, role, opts, err := r.registryRecipients(ctx, a)
	if err != nil {
		return err
	}
	typ, _ := inboxModel.RegistryCredentialTypes(a.Kind)
	meta := inboxModel.NewMeta(typ, role, inboxModel.RegistryCredentialRef(a.Key)).Raised(a.At)
	return r.fanOut(ctx, to, typ, a.vars(), meta, opts...)
}

// RegistryCredentialRestored closes the request and tells the same people the credential works again. The check
// sends it once, on the transition back to ok.
func (r *RequestRouter) RegistryCredentialRestored(ctx context.Context, a RegistryCredentialAlert) error {
	if err := r.resolve(ctx, inboxModel.RegistryCredentialRef(a.Key), inboxModel.ResolutionResolved, uuid.Nil); err != nil {
		return err
	}
	to, role, opts, err := r.registryRecipients(ctx, a)
	if err != nil {
		return err
	}
	_, typ := inboxModel.RegistryCredentialTypes(a.Kind)
	meta := inboxModel.NewMeta(typ, role, "").Raised(a.At)
	return r.fanOut(ctx, to, typ, a.vars(), meta, opts...)
}

// RegistryCredentialClosed closes the request of a credential that no longer exists (removed or replaced
// settings), with no note.
func (r *RequestRouter) RegistryCredentialClosed(ctx context.Context, key string) error {
	return r.resolve(ctx, inboxModel.RegistryCredentialRef(key), inboxModel.ResolutionResolved, uuid.Nil)
}
