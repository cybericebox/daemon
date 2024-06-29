package password

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidHashPassword         = errors.New("invalid hashed password")
	ErrorInvalidPasswordComplexity = errors.New("invalid password complexity")
)

type (
	Manager struct {
		cost                     int
		passwordComplexityConfig PasswordComplexityConfig
	}

	PasswordComplexityConfig struct {
		MinLength            int
		MaxLength            int
		MinCapitalLetters    int
		MinSmallLetters      int
		MinDigits            int
		MinSpecialCharacters int
	}

	Dependencies struct {
		Cost               int
		PasswordComplexity PasswordComplexityConfig
	}
)

func NewHashManager(deps Dependencies) *Manager {
	return &Manager{cost: deps.Cost, passwordComplexityConfig: deps.PasswordComplexity}
}

func (m *Manager) Hash(plaintextPassword string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(plaintextPassword), m.cost)
	if err != nil {
		return "", err
	}

	return string(hashedPassword), nil
}

func (m *Manager) Matches(plaintextPassword, hashedPassword string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(plaintextPassword))
	if err != nil {
		switch {
		case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
			return false, nil
		case strings.Contains(err.Error(), "bcrypt:"):
			return false, errors.Join(ErrInvalidHashPassword, err)
		default:
			return false, err
		}
	}

	return true, nil
}

func (m *Manager) CheckPasswordComplexity(password string) error {
	if len(password) < m.passwordComplexityConfig.MinLength {
		return ErrorInvalidPasswordComplexity
	}

	if len(password) > m.passwordComplexityConfig.MaxLength {
		return ErrorInvalidPasswordComplexity
	}

	if len(strings.FieldsFunc(password, func(r rune) bool {
		return r >= '0' && r <= '9'
	})) < m.passwordComplexityConfig.MinDigits {
		return ErrorInvalidPasswordComplexity
	}

	if len(strings.FieldsFunc(password, func(r rune) bool {
		return r >= 'A' && r <= 'Z'
	})) < m.passwordComplexityConfig.MinCapitalLetters {
		return ErrorInvalidPasswordComplexity
	}

	if len(strings.FieldsFunc(password, func(r rune) bool {
		return r >= 'a' && r <= 'z'
	})) < m.passwordComplexityConfig.MinSmallLetters {
		return ErrorInvalidPasswordComplexity
	}

	if len(strings.FieldsFunc(password, func(r rune) bool {
		return r >= 33 && r <= 47
	})) < m.passwordComplexityConfig.MinSpecialCharacters {
		return ErrorInvalidPasswordComplexity
	}

	return nil
}
