package tools

type CError struct {
	Code    int
	Message string
	error
}

func (e CError) Error() string {
	return e.Message
}

func (e CError) StatusCode() int {
	if e.Code == 0 {
		return 500
	}
	return e.Code
}

func NewError(message string, code ...int) *CError {
	c := 500
	if len(code) > 0 {
		c = code[0]
	}
	return &CError{
		Code:    c,
		Message: message,
	}
}
