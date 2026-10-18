package inboxModel

// The registry credential check: a platform token or an organizer secret stopped working (a request to the people
// who can fix it) and, later, works again (a note). They exist only as inbox messages. Each credential class has
// its own pair of types, because a template holds only variable substitutions: the copy cannot branch.
const (
	TypeRegistryPushBroken     = "registry.push_token.broken"
	TypeRegistryPushRestored   = "registry.push_token.restored"
	TypeRegistryPullBroken     = "registry.pull_token.broken"
	TypeRegistryPullRestored   = "registry.pull_token.restored"
	TypeRegistrySecretBroken   = "registry.event_secret.broken"
	TypeRegistrySecretRestored = "registry.event_secret.restored"
)

// Credential classes of the registry check (the alert key of a platform token, the key prefix of a secret).
const (
	RegistryKindPush   = "platform_push"
	RegistryKindPull   = "platform_pull"
	RegistryKindSecret = "event_secret"
)

func init() {
	for _, typ := range []string{TypeRegistryPushBroken, TypeRegistryPullBroken, TypeRegistrySecretBroken} {
		categoryRules[typ] = map[RecipientRole]Category{RoleAdmin: CategoryRequests, RoleManager: CategoryRequests, "": CategoryPersonal}
		// The request closes by itself when the credential works again; a recipient may also dismiss it by hand.
		manuallyResolvable[typ] = true
	}
	for _, typ := range []string{TypeRegistryPushRestored, TypeRegistryPullRestored, TypeRegistrySecretRestored} {
		categoryRules[typ] = map[RecipientRole]Category{"": CategoryPersonal}
	}
}

// RegistryCredentialTypes returns the notification types of a credential class.
func RegistryCredentialTypes(kind string) (broken, restored string) {
	switch kind {
	case RegistryKindPush:
		return TypeRegistryPushBroken, TypeRegistryPushRestored
	case RegistryKindPull:
		return TypeRegistryPullBroken, TypeRegistryPullRestored
	}
	return TypeRegistrySecretBroken, TypeRegistrySecretRestored
}

// RegistryCredentialRef ties every recipient's copy of the alert about one credential together; key names the
// credential ("platform_push", "platform_pull", "event_secret:<id>").
func RegistryCredentialRef(key string) string { return "registry_credential:" + key }
