package appError

import "errors"

type (
	Error struct {
		code  Code
		error error
	}

	IError interface {
		Error() string
		Unwrap() error
		Code() Code
	}
)

func (e Error) Error() string {
	return e.error.Error()
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
	return e
}

func (e Error) WithError(err error) Error {
	e.error = err
	return e
}
