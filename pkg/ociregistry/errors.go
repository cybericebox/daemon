package ociregistry

import (
	"errors"
	"net/http"
)

// Sentinel errors of the client. They are wrapped with %w; no message ever carries a credential.
var (
	// ErrUnauthorized: the registry refused the credentials or the scope (401/403).
	ErrUnauthorized = errors.New("registry refused the credentials")
	// ErrNotFound: the repository, blob or manifest does not exist (404).
	ErrNotFound = errors.New("registry object not found")
	// ErrUnreachable: no answer, a transport failure or a server error (5xx).
	ErrUnreachable = errors.New("registry unreachable")
	// ErrRateLimited: the registry throttles requests (429).
	ErrRateLimited = errors.New("registry rate limit reached")
	// ErrInvalidReference: the host or repository is not a valid registry reference (refused before any
	// request; retrying cannot help).
	ErrInvalidReference = errors.New("invalid registry reference")
	// ErrBlobTooLarge: a blob read whole (GetBlob) is larger than the caller allows.
	ErrBlobTooLarge = errors.New("registry blob too large")
)

// classifyStatus maps an HTTP status to a sentinel; nil for success statuses.
func classifyStatus(code int) error {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return ErrUnauthorized
	case code == http.StatusNotFound:
		return ErrNotFound
	case code == http.StatusTooManyRequests:
		return ErrRateLimited
	case code >= 500:
		return ErrUnreachable
	}
	return nil
}
