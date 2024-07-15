package appError

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

type (
	Error struct {
		code         Code
		message      string
		error        error
		filePosition string
	}

	IError interface {
		Error() string
		Unwrap() error
		Code() Code
		UnwrapNotInternalError() Error
	}
)

func (e Error) Error() string {
	return fmt.Sprintf("{%s} %s: [%s]", e.filePosition, e.code.GetMessage(), e.error)
}

func (e Error) Unwrap() error {
	return e.error
}

func (e Error) Code() Code {
	return e.code
}

func NewError() Error {
	return Error{
		code: NewCode(),
	}
}

func (e Error) WithCode(code Code) Error {
	e.code = code
	if e.error == nil {
		e.error = errors.New(code.GetMessage())
	}
	_, file, line, ok := runtime.Caller(1)
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

func (e Error) WithMessage(message string) Error {
	e.message = message
	e.code = e.code.WithMessage(message)

	_, file, line, ok := runtime.Caller(1)
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

func (e Error) WithError(err error) Error {
	e.error = err
	_, file, line, ok := runtime.Caller(1)
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

func (e Error) UnwrapNotInternalError() Error {
	if e.code.IsInternalError() {
		wrapped, ok := e.error.(interface{ UnwrapNotInternalError() Error })
		if ok {
			return wrapped.UnwrapNotInternalError()
		}
		return e
	}
	return e
}
