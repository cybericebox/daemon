package err

import (
	"fmt"
	"net/http"
)

type (
	code struct {
		httpCode   int
		informCode int // for Not Found, Already Exists, etc.
		objectCode int // for dto
		detailCode int // for specific error

		message string
		details map[string]any
	}
	StatusCode interface {
		WithMessage(message string) StatusCode
		WithMessageF(format string, a ...any) StatusCode
		WithDetail(key string, value any) StatusCode
		WithDetails(details map[string]any) StatusCode
		WithInformCode(informCode int) StatusCode
		WithObjectCode(objectCode int) StatusCode
		WithDetailCode(detailCode int) StatusCode
		WithHTTPCode(httpCode int) StatusCode
		FullCode() int
		InformCode() int
		ObjectCode() int
		DetailCode() int
		HTTPCode() int
		Message() string
		Details() map[string]any
		Is(code StatusCode) bool
		IsInternal() bool
		IsSuccess() bool
		As(code StatusCode) bool
	}
)

// NewStatusCode creates a new StatusCode instance with default values.
// Default values are:
// message = "Internal server error",
// informCode = platformCodeInternal
func NewStatusCode() StatusCode {
	return &code{
		httpCode:   http.StatusInternalServerError,
		message:    "Internal server error",
		informCode: InformCodeInternal,
	}
}

func (c code) WithMessage(message string) StatusCode {
	c.message = message
	return c
}

func (c code) WithMessageF(format string, a ...any) StatusCode {
	c.message = fmt.Sprintf(format, a...)
	return c
}

func (c code) WithDetail(key string, value any) StatusCode {
	if c.details == nil {
		c.details = make(map[string]any)
	}
	c.details[key] = value
	return c
}

func (c code) WithDetails(details map[string]any) StatusCode {
	if c.details == nil {
		c.details = make(map[string]any)
	}

	for k, v := range details {
		c.details[k] = v
	}
	return c
}

func (c code) WithInformCode(informCode int) StatusCode {
	c.informCode = informCode
	return c
}

func (c code) WithObjectCode(objectCode int) StatusCode {
	c.objectCode = objectCode
	return c
}

func (c code) WithDetailCode(detailCode int) StatusCode {
	c.detailCode = detailCode
	return c
}

func (c code) WithHTTPCode(httpCode int) StatusCode {
	c.httpCode = httpCode
	return c
}

// FullCode returns the full code as a combination of informCode, objectCode and detailCode.
// The full code is calculated as: informCode*10000 + objectCode*100 + detailCode
func (c code) FullCode() int {
	return c.informCode*10000 + c.objectCode*100 + c.detailCode
}

func (c code) InformCode() int {
	return c.informCode
}

func (c code) ObjectCode() int {
	return c.objectCode
}

func (c code) DetailCode() int {
	return c.detailCode
}

func (c code) HTTPCode() int {
	return c.httpCode
}

func (c code) Message() string {
	return c.message
}

func (c code) Details() map[string]any {
	if c.details == nil {
		return make(map[string]any)
	}
	return c.details
}

func (c code) Is(code StatusCode) bool {
	return c.FullCode() == code.FullCode()
}

func (c code) IsInternal() bool {
	return c.informCode == InformCodeInternal
}

func (c code) IsSuccess() bool {
	return c.informCode == InformCodeSuccess
}

// As reports whether c matches the target code at the target's most specific
// level. A detail-level target is scoped to its object code when it has one:
// comparing detailCode alone would conflate unrelated errors from different
// objects that happen to share a detail code. A detail-only target (no
// objectCode) keeps matching by detailCode alone for callers that never set
// object codes.
func (c code) As(code StatusCode) bool {
	if code.DetailCode() != 0 {
		if code.ObjectCode() != 0 && c.ObjectCode() != code.ObjectCode() {
			return false
		}
		return c.DetailCode() == code.DetailCode()
	}

	if code.ObjectCode() != 0 {
		return c.ObjectCode() == code.ObjectCode()
	}

	if code.InformCode() != 0 {
		return c.InformCode() == code.InformCode()
	}

	return false
}

// Standard inform codes
const (
	InformCodeInternal = iota + 0
	InformCodeSuccess
	InformCodeInvalidData
	InformCodeObjectNotFound
	InformCodeObjectExists
	InformCodeUnauthenticated
	InformCodeForbidden
	InformCodeConflict
)

// StatusCode constants for categories
var (
	// StatusCodeSuccess has http.StatusOK as default http code
	StatusCodeSuccess = NewStatusCode().WithInformCode(InformCodeSuccess).WithMessage("Success").WithHTTPCode(http.StatusOK)
	// StatusCodeInvalidData has http.StatusBadRequest as default http code
	StatusCodeInvalidData = NewStatusCode().WithInformCode(InformCodeInvalidData).WithMessage("Invalid data").WithHTTPCode(http.StatusBadRequest)
	// StatusCodeObjectNotFound has http.StatusNotFound as default http code
	StatusCodeObjectNotFound = NewStatusCode().WithInformCode(InformCodeObjectNotFound).WithMessage("Object not found").WithHTTPCode(http.StatusNotFound)
	// StatusCodeUnauthenticated has http.StatusUnauthorized as default http code
	StatusCodeUnauthenticated = NewStatusCode().WithInformCode(InformCodeUnauthenticated).WithMessage("Unauthenticated").WithHTTPCode(http.StatusUnauthorized)
	// StatusCodeForbidden has http.StatusForbidden as default http code
	StatusCodeForbidden = NewStatusCode().WithInformCode(InformCodeForbidden).WithMessage("Forbidden").WithHTTPCode(http.StatusForbidden)
	// StatusCodeObjectExists has http.StatusConflict as default http code
	StatusCodeObjectExists = NewStatusCode().WithInformCode(InformCodeObjectExists).WithMessage("Object already exists").WithHTTPCode(http.StatusConflict)
	// StatusCodeConflict has http.StatusConflict as default http code
	StatusCodeConflict = NewStatusCode().WithInformCode(InformCodeConflict).WithMessage("Conflict").WithHTTPCode(http.StatusConflict)
)

// DetailRetryAfterSeconds is the status detail that carries a throttling
// delay; the HTTP layer sends it as the Retry-After header.
const DetailRetryAfterSeconds = "retryAfterSeconds"
