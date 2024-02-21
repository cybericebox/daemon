package auth

import "errors"

var (
	ErrNoSubdomain = errors.New("no subdomain in context")

	ErrUserAlreadyExists = errors.New("user already exists")
	ErrEmailAlreadyTaken = errors.New("email already taken")

	ErrInvalidEmailOrPassword = errors.New("invalid email or password")

	ErrInvalidTemporalCode       = errors.New("invalid temporal code")
	ErrInvalidPasswordComplexity = errors.New("invalid password complexity") //password.ErrorInvalidPasswordComplexity
)
