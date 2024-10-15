package appError

import "net/http"

type (
	code struct {
		httpCode   int
		informCode int // for Not Found, Already Exists, etc.
		objectCode int // for dto
		detailCode int // for specific error

		message string
		details map[string]interface{}
	}
	Code interface {
		WithMessage(message string) Code
		WithDetails(details map[string]interface{}) Code
		WithInformCode(informCode int) Code
		WithObjectCode(objectCode int) Code
		WithDetailCode(detailCode int) Code
		WithHTTPCode(httpCode int) Code
		Code() int
		ObjectCode() int
		DetailCode() int
		InformCode() int
		HTTPCode() int
		Message() string
		Details() map[string]interface{}
		Is(code Code) bool
		IsInternal() bool
		IsSuccess() bool
		As(code Code) bool
	}
)

// NewCode creates a new Code instance with default values.
// Default values are:
// httpCode = http.StatusInternalServerError,
// message = "Internal server error",
// informCode = platformCodeInternal
func newCode() Code {
	return &code{
		httpCode:   http.StatusInternalServerError,
		message:    "Internal server error",
		informCode: platformCodeInternal,
	}
}

func (c code) WithMessage(message string) Code {
	c.message = message
	return c
}

func (c code) WithDetails(details map[string]interface{}) Code {
	c.details = details
	return c
}

func (c code) WithInformCode(informCode int) Code {
	c.informCode = informCode
	return c
}

func (c code) WithObjectCode(objectCode int) Code {
	c.objectCode = objectCode
	return c
}

func (c code) WithDetailCode(detailCode int) Code {
	c.detailCode = detailCode
	return c
}

func (c code) WithHTTPCode(httpCode int) Code {
	c.httpCode = httpCode
	return c
}

func (c code) Code() int {
	return c.informCode*10000 + c.objectCode*100 + c.detailCode
}

func (c code) ObjectCode() int {
	return c.objectCode
}

func (c code) DetailCode() int {
	return c.detailCode
}

func (c code) InformCode() int {
	return c.informCode
}

func (c code) HTTPCode() int {
	return c.httpCode
}

func (c code) Message() string {
	return c.message
}

func (c code) Details() map[string]interface{} {
	if c.details == nil {
		return make(map[string]interface{})
	}
	return c.details
}

func (c code) Is(code Code) bool {
	return c.Code() == code.Code()
}

func (c code) IsInternal() bool {
	return c.informCode == platformCodeInternal
}

func (c code) IsSuccess() bool {
	return c.informCode == platformCodeSuccess
}

func (c code) As(code Code) bool {
	if code.DetailCode() != 0 {
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
	platformCodeInternal = iota + 0
	platformCodeSuccess
	platformCodeInvalidData
	platformCodeObjectNotFound
	platformCodeObjectExists
	platformCodeUnauthenticated
	platformCodeForbidden
)

// Code constants for categories
var (
	codeInternal = newCode()
	// CodeSuccess has http.StatusOK as default http code
	codeSuccess = newCode().WithInformCode(platformCodeSuccess).WithMessage("Success").WithHTTPCode(http.StatusOK)
	// CodeInvalidData has http.StatusBadRequest as default http code
	codeInvalidData = newCode().WithInformCode(platformCodeInvalidData).WithMessage("Invalid data").WithHTTPCode(http.StatusBadRequest)
	// CodeObjectNotFound has http.StatusNotFound as default http code
	codeObjectNotFound = newCode().WithInformCode(platformCodeObjectNotFound).WithMessage("Object not found").WithHTTPCode(http.StatusNotFound)
	// CodeUnauthenticated has http.StatusUnauthorized as default http code
	codeUnauthenticated = newCode().WithInformCode(platformCodeUnauthenticated).WithMessage("Unauthenticated").WithHTTPCode(http.StatusUnauthorized)
	// CodeForbidden has http.StatusForbidden as default http code
	codeForbidden = newCode().WithInformCode(platformCodeForbidden).WithMessage("Forbidden").WithHTTPCode(http.StatusForbidden)
	// CodeObjectAlreadyExists has http.StatusConflict as default http code
	codeObjectExists = newCode().WithInformCode(platformCodeObjectExists).WithMessage("Object already exists").WithHTTPCode(http.StatusConflict)
)
