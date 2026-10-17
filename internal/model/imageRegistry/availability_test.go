package imageRegistryModel

import "testing"

// A transient reason may clear by itself (the registry answers again, an admin configures it); a
// definitive one needs a change to the task or its credentials.
func TestReasonTransient(t *testing.T) {
	for reason, want := range map[Reason]bool{
		ReasonUnreachable: true, ReasonRateLimited: true, ReasonNotConfigured: true,
		ReasonNotFound: false, ReasonUnauthorized: false, ReasonNeedsEventSecret: false,
		ReasonInvalidReference: false, ReasonCredentialConflict: false,
	} {
		if got := reason.Transient(); got != want {
			t.Errorf("%s transient = %v, want %v", reason, got, want)
		}
	}
}
