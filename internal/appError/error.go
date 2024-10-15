package appError

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

type (
	appError struct {
		context      map[string]interface{}
		code         Code
		filePosition string

		wrappedError error
	}

	Error interface {
		Error() string
		Unwrap() error
		Code() Code
		Is(err error) bool
		UnwrapNotInternalError() Error
	}

	ErrorCreator interface {
		WithInformCode(informCode int) ErrorCreator
		WithObjectCode(objectCode int) ErrorCreator
		WithDetailCode(detailCode int) ErrorCreator
		WithMessage(message string) ErrorCreator
		WithError(err error) ErrorCreator
		WithContext(key string, value interface{}) ErrorCreator
		WithCode(code Code) ErrorCreator
		Cause() Error
		Err() Error
	}
)

func newError() ErrorCreator {
	return &appError{
		code: newCode(),
	}
}

func (e appError) Error() string {
	errorString := fmt.Sprintf("(%s) #%d '%s'", e.filePosition, e.code.Code(), e.code.Message())

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

func (e appError) Code() Code {
	return e.code
}

func (e appError) Is(err error) bool {
	var e2 appError
	ok := errors.As(err, &e2)
	if !ok {
		return false
	}
	return e.code.Is(e2.code)
}

func (e appError) WithInformCode(informCode int) ErrorCreator {
	e.code = e.code.WithInformCode(informCode)
	return e
}

func (e appError) WithObjectCode(objectCode int) ErrorCreator {
	e.code = e.code.WithObjectCode(objectCode)
	return e
}

func (e appError) WithDetailCode(detailCode int) ErrorCreator {
	e.code = e.code.WithDetailCode(detailCode)
	return e
}

func (e appError) WithMessage(message string) ErrorCreator {
	e.code = e.code.WithMessage(message)

	return e
}

func (e appError) WithCode(code Code) ErrorCreator {
	e.code = code
	return e
}

func (e appError) WithContext(key string, value interface{}) ErrorCreator {
	if e.context == nil {
		e.context = make(map[string]interface{})
	}
	e.context[key] = value
	return e
}

func (e appError) WithError(err error) ErrorCreator {
	e.wrappedError = err
	return e
}

func (e appError) Cause() Error {
	return e.saveStack()
}

func (e appError) Err() Error {
	return e
}

func (e appError) UnwrapNotInternalError() Error {
	if e.code.IsInternal() {
		wrapped, ok := e.wrappedError.(interface{ UnwrapNotInternalError() Error })
		if ok {
			return wrapped.UnwrapNotInternalError()
		}
		return e
	}
	return e
}

func (e appError) saveStack() Error {
	_, file, line, ok := runtime.Caller(2)
	if ok {
		currentDir, er := os.Getwd()
		if er != nil {
			return e
		}
		file = file[len(currentDir):]
		e.filePosition = fmt.Sprintf("%s:%d", file, line)
	}
	return e
}

var (
	ErrObjectNotFound = newError().WithCode(codeObjectNotFound)
	ErrObjectExists   = newError().WithCode(codeObjectExists)
	ErrForbidden      = newError().WithCode(codeForbidden)
	ErrInvalidData    = newError().WithCode(codeInvalidData)
	ErrInternal       = newError()
	Success           = newError().WithCode(codeSuccess)
)
