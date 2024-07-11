package appError

import "net/http"

type (
	Code struct {
		informCode int
		message    string
		httpCode   int
	}
)

// NewCode creates a new Code instance with default values.
// Default values are:
// httpCode = http.StatusInternalServerError,
// message = "Internal server error",
// informCode = platformCodeInternalError
func NewCode() Code {
	return Code{
		httpCode:   http.StatusInternalServerError,
		message:    "Internal server error",
		informCode: platformCodeInternalError,
	}
}

func (c Code) WithInformCode(informCode int) Code {
	c.informCode = informCode
	return c
}

func (c Code) WithMessage(message string) Code {
	c.message = message
	return c
}

func (c Code) WithHTTPCode(httpCode int) Code {
	c.httpCode = httpCode
	return c
}

func (c Code) GetInformCode() int {
	return c.informCode
}

func (c Code) GetMessage() string {
	return c.message
}

func (c Code) GetHTTPCode() int {
	return c.httpCode
}

func (c Code) IsInternalError() bool {
	return c.informCode == platformCodeInternalError
}

func (c Code) IsSuccess() bool {
	return c.informCode == platformCodeSuccess
}

// Standard inform codes
const (
	platformCodeInternalError = iota + 0
	platformCodeSuccess
	platformCodeInvalidInput
	platformCodeNotFound
	platformCodeUnauthorized
	platformCodeForbidden
	platformCodeAlreadyExists
)

var (
	CodeInternalError = NewCode()
	// CodeSuccess has http.StatusOK as default http code
	CodeSuccess = NewCode().WithInformCode(platformCodeSuccess).WithMessage("Success").WithHTTPCode(http.StatusOK)
	// CodeInvalidInput has http.StatusBadRequest as default http code
	CodeInvalidInput = NewCode().WithInformCode(platformCodeInvalidInput).WithMessage("Invalid input").WithHTTPCode(http.StatusBadRequest)
	// CodeNotFound has http.StatusNotFound as default http code
	CodeNotFound = NewCode().WithInformCode(platformCodeNotFound).WithMessage("Not found").WithHTTPCode(http.StatusNotFound)
	// CodeUnauthorized has http.StatusUnauthorized as default http code
	CodeUnauthorized = NewCode().WithInformCode(platformCodeUnauthorized).WithMessage("Unauthorized").WithHTTPCode(http.StatusUnauthorized)
	// CodeForbidden has http.StatusForbidden as default http code
	CodeForbidden = NewCode().WithInformCode(platformCodeForbidden).WithMessage("Forbidden").WithHTTPCode(http.StatusForbidden)
	// CodeAlreadyExists has http.StatusConflict as default http code
	CodeAlreadyExists = NewCode().WithInformCode(platformCodeAlreadyExists).WithMessage("Already exists").WithHTTPCode(http.StatusConflict)
)
