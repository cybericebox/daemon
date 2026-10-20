package ociregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
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
	// ErrSourceCorrupt: the archive's own bytes are broken as its container reads them (a zip entry longer or
	// shorter than declared, a failed checksum or authentication code). The reader of the source wraps such failures
	// with it, so they are told as the author's archive, not as a platform failure.
	ErrSourceCorrupt = errors.New("image archive is corrupt")
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

const (
	// maxErrorBody bounds how much of a failing response is read for the registry's own error.
	maxErrorBody = 2 << 10
	// maxErrorDetail bounds the registry text carried in an error.
	maxErrorDetail = 200
)

// statusFailure builds the error of a failed registry response: the sentinel of its status plus, when the body is
// an OCI error document ({"errors":[{"code","message"}]}), the registry's code and message. The body is read up
// to 2 KiB and cleaned (no control characters, bounded), so a hostile registry cannot flood a log. The caller
// still drains and closes the response.
func statusFailure(op string, resp *http.Response) error {
	sentinel := classifyStatus(resp.StatusCode)
	if sentinel == nil {
		sentinel = ErrUnreachable
	}
	if detail := registryDetail(resp.Body); detail != "" {
		return fmt.Errorf("%s: %w (status %d: %s)", op, sentinel, resp.StatusCode, detail)
	}
	return fmt.Errorf("%s: %w (status %d)", op, sentinel, resp.StatusCode)
}

// registryDetail is "CODE: message" of the first usable entry of an OCI error document, "" otherwise.
func registryDetail(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBody))
	if err != nil && len(raw) == 0 {
		return ""
	}
	var doc struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	for _, e := range doc.Errors {
		code, msg := cleanText(e.Code), cleanText(e.Message)
		switch {
		case code != "" && msg != "":
			return clip(code + ": " + msg)
		case code != "":
			return clip(code)
		case msg != "":
			return clip(msg)
		}
	}
	return ""
}

func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string) string {
	if r := []rune(s); len(r) > maxErrorDetail {
		return string(r[:maxErrorDetail]) + "…"
	}
	return s
}
