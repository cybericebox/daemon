package startupdefaults

func init() {
	for _, typ := range []string{"registry.push_token.broken", "registry.pull_token.broken", "registry.event_secret.broken"} {
		inAppLook[typ] = [2]string{"warning", "warning"}
	}
	for _, typ := range []string{"registry.push_token.restored", "registry.pull_token.restored", "registry.event_secret.restored"} {
		inAppLook[typ] = [2]string{"success", "success"}
	}
}
