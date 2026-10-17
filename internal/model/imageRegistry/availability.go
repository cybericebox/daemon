package imageRegistryModel

// Status is the result of an image availability check.
type Status string

const (
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable"
)

// Reason explains an unavailable status. It never carries a token.
type Reason string

const (
	ReasonNotConfigured    Reason = "not_configured"
	ReasonNotFound         Reason = "not_found"
	ReasonUnauthorized     Reason = "unauthorized"
	ReasonUnreachable      Reason = "unreachable"
	ReasonRateLimited      Reason = "rate_limited"
	ReasonNeedsEventSecret Reason = "needs_event_secret"
	ReasonInvalidReference Reason = "invalid_reference"
	// ReasonCredentialConflict: the lab would need two different credentials for
	// one registry host, or credentials for more hosts than the agent takes
	// (MaxDeployCredentials); the deploy is refused before it starts.
	ReasonCredentialConflict Reason = "credential_conflict"
	// ReasonDigestMismatch: an uploaded blob's bytes do not hash to its digest (image upload).
	ReasonDigestMismatch Reason = "digest_mismatch"
	// ReasonUploadMissing: the chunked upload a blob push reads is gone (expired or dropped); upload it again.
	ReasonUploadMissing Reason = "upload_missing"
)

// Transient is true for a reason that may clear without any change to the
// task or its credentials: the registry answers again, or an admin configures
// the platform registry.
func (r Reason) Transient() bool {
	switch r {
	case ReasonUnreachable, ReasonRateLimited, ReasonNotConfigured:
		return true
	default:
		return false
	}
}

// Availability is the check result for one image reference.
type Availability struct {
	Image  string
	Status Status
	Reason Reason
}

// MaxDeployCredentials is the most pull credentials (distinct registry hosts)
// the laboratory agent accepts with one lab deploy.
const MaxDeployCredentials = 8

// CredentialHost is the key a pull credential is sent under: the lowercased
// host, with every Docker Hub spelling as index.docker.io (the agent takes one
// credential per bare host). Image references are never rewritten with it.
func CredentialHost(host string) string {
	if h := normalizeHost(host); h != dockerHubHost {
		return h
	}
	return "index.docker.io"
}
