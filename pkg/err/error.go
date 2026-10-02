package err

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

type (
	appError struct {
		context map[string]any
		// public is the part of the context that may reach the client (see WithPublicContext).
		public       map[string]any
		statusCode   StatusCode
		filePosition string

		wrappedError error
	}

	Error interface {
		Error() string
		Unwrap() error
		StatusCode() StatusCode
		Is(err error) bool
		Equal(err error) bool
		UnwrapNotInternalError() Error
		// PublicContext is the context the error marked as safe for the client (WithPublicContext); never the rest.
		PublicContext() map[string]any
	}

	ErrorCreator interface {
		WithInformCode(informCode int) ErrorCreator
		WithObjectCode(objectCode int) ErrorCreator
		WithDetailCode(detailCode int) ErrorCreator
		WithMessage(message string) ErrorCreator
		WithMessageF(format string, a ...any) ErrorCreator
		WithDetail(key string, value any) ErrorCreator
		WithDetails(details map[string]any) ErrorCreator
		WithError(err error) ErrorCreator
		WithWrappedError(errCreator ErrorCreator) ErrorCreator
		WithContext(key string, value any) ErrorCreator
		WithPublicContext(key string, value any) ErrorCreator
		WithStatusCode(statusCode StatusCode) ErrorCreator
		WithHTTPCode(httpCode int) ErrorCreator
		Err(skip ...int) Error
	}
)

func NewError() ErrorCreator {
	return &appError{
		statusCode: NewStatusCode(),
	}
}

func (e appError) Error() string {
	errorString := fmt.Sprintf("(%s) #%d '%s'", e.filePosition, e.statusCode.FullCode(), e.statusCode.Message())

	if e.context != nil {
		var fromContext []string
		for key, value := range e.context {
			fromContext = append(fromContext, fmt.Sprintf("%s:=%v", strings.ToUpper(key), value))
		}
		if len(fromContext) > 0 {
			errorString = fmt.Sprintf("%s {%s}", errorString, strings.Join(fromContext, "; "))
		}
	}
	if e.wrappedError != nil {
		errorString = fmt.Sprintf("%s [%s]", errorString, e.wrappedError.Error())
	}

	return errorString
}

func (e appError) Unwrap() error {
	return e.wrappedError
}

func (e appError) StatusCode() StatusCode {
	return e.statusCode
}

func (e appError) Is(err error) bool {
	var e2 appError
	ok := errors.As(err, &e2)
	if !ok {
		return false
	}
	return e.statusCode.As(e2.statusCode)
}

func (e appError) Equal(err error) bool {
	var e2 appError
	ok := errors.As(err, &e2)
	if !ok {
		return false
	}
	return e.statusCode.Is(e2.statusCode)
}

// WithInformCode sets the inform code of the error
func (e appError) WithInformCode(informCode int) ErrorCreator {
	e.statusCode = e.statusCode.WithInformCode(informCode)
	return e
}

// WithObjectCode sets the object code of the error
func (e appError) WithObjectCode(objectCode int) ErrorCreator {
	e.statusCode = e.statusCode.WithObjectCode(objectCode)
	return e
}

// WithDetailCode sets the detail code of the error
func (e appError) WithDetailCode(detailCode int) ErrorCreator {
	e.statusCode = e.statusCode.WithDetailCode(detailCode)
	return e
}

// WithMessage sets the message of the error
func (e appError) WithMessage(message string) ErrorCreator {
	e.statusCode = e.statusCode.WithMessage(message)

	return e
}

// WithMessageF sets the message of the error with a formatted string
func (e appError) WithMessageF(format string, a ...any) ErrorCreator {
	e.statusCode = e.statusCode.WithMessageF(format, a...)

	return e
}

// WithStatusCode sets the code of the error
func (e appError) WithStatusCode(statusCode StatusCode) ErrorCreator {
	e.statusCode = statusCode
	return e
}

// WithHTTPCode overrides transport status without changing the stable
// application error code. This is used for operationally unavailable services
// such as an optional infrastructure agent.
func (e appError) WithHTTPCode(httpCode int) ErrorCreator {
	e.statusCode = e.statusCode.WithHTTPCode(httpCode)
	return e
}

// WithContext sets the context of the error
func (e appError) WithContext(key string, value any) ErrorCreator {
	ctx := make(map[string]any, len(e.context)+1)
	for k, v := range e.context {
		ctx[k] = v
	}
	ctx[key] = value
	e.context = ctx
	return e
}

// WithPublicContext sets a context value that is also sent to the client in the response status (Status.Context), so a
// frontend can act on it (the nearest free window, the frame a device exceeds). It is the only way a context value
// leaves the server: WithContext stays internal, for logs. Use it only for values that are safe to show to whoever
// triggered the error: never an agent name, an address, a secret or any internal detail.
func (e appError) WithPublicContext(key string, value any) ErrorCreator {
	e = e.WithContext(key, value).(appError)
	public := make(map[string]any, len(e.public)+1)
	for k, v := range e.public {
		public[k] = v
	}
	public[key] = value
	e.public = public
	return e
}

// PublicContext returns a copy of the context marked public; nil when there is none.
func (e appError) PublicContext() map[string]any {
	if len(e.public) == 0 {
		return nil
	}
	out := make(map[string]any, len(e.public))
	for k, v := range e.public {
		out[k] = v
	}
	return out
}

// WithDetail sets a detail of the error
func (e appError) WithDetail(key string, value any) ErrorCreator {
	e.statusCode = e.statusCode.WithDetail(key, value)
	return e
}

// WithDetails sets the details of the error
func (e appError) WithDetails(details map[string]any) ErrorCreator {
	e.statusCode = e.statusCode.WithDetails(details)
	return e
}

// WithError wraps an error in the current error
func (e appError) WithError(err error) ErrorCreator {
	e.wrappedError = err
	return e
}

// WithWrappedError wraps a wrapped error in the current error
func (e appError) WithWrappedError(errCreator ErrorCreator) ErrorCreator {
	e.wrappedError = errCreator.Err(1)
	return e
}

// Err returns the error with the stack trace
// The argument skip is the number of stack frames to ascend, with 0 or empty identifying the caller of Err.
func (e appError) Err(skip ...int) Error {
	if len(skip) > 0 && skip[0] >= 0 {
		skip[0]++
	} else {
		skip = append(skip, 1)
	}
	return e.saveStack(skip[0])
}

// UnwrapNotInternalError unwraps the error until it finds an error that is not internal or returns nil if it is not found
func (e appError) UnwrapNotInternalError() Error {
	if e.statusCode.IsInternal() {
		wrapped, ok := e.wrappedError.(interface{ UnwrapNotInternalError() Error })
		if ok {
			return wrapped.UnwrapNotInternalError()
		}
		return e
	}
	return e
}

// The argument skip is the number of stack frames to ascend, with 0 identifying the caller of saveStack.
func (e appError) saveStack(skip int) Error {
	if skip < 0 {
		skip = 0
	}
	skip++
	_, file, line, ok := runtime.Caller(skip)
	if ok {
		currentDir, er := os.Getwd()
		if er != nil {
			return e
		}
		file = strings.TrimPrefix(file, currentDir)
		e.filePosition = fmt.Sprintf("%s:%d", file, line)
	}
	return e
}

var (
	ErrObjectNotFound  = NewError().WithStatusCode(StatusCodeObjectNotFound)
	ErrObjectExists    = NewError().WithStatusCode(StatusCodeObjectExists)
	ErrForbidden       = NewError().WithStatusCode(StatusCodeForbidden)
	ErrUnauthenticated = NewError().WithStatusCode(StatusCodeUnauthenticated)
	ErrInvalidData     = NewError().WithStatusCode(StatusCodeInvalidData)
	ErrInternal        = NewError()
	ErrConflict        = NewError().WithStatusCode(StatusCodeConflict)
)
