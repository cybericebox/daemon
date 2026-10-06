package auth

import "github.com/cybericebox/daemon/pkg/password"

// PasswordPolicy returns the active password complexity rules (static, from config).
func (u *AuthUseCase) PasswordPolicy() PasswordPolicy {
	c := u.password.Complexity()
	return PasswordPolicy{
		MinLength:            c.MinLength,
		MaxLength:            c.MaxLength,
		MinCapitalLetters:    c.MinCapitalLetters,
		MinSmallLetters:      c.MinSmallLetters,
		MinDigits:            c.MinDigits,
		MinSpecialCharacters: c.MinSpecialCharacters,
		SpecialCharacters:    password.SpecialCharacters,
	}
}
