package auth_test

import (
	"testing"

	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
)

func TestPasswordPolicy_ReflectsPasswordClientComplexity(t *testing.T) {
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Password: password.New(password.Config{
			HashCost: 4,
			Complexity: password.ComplexityConfig{
				MinLength:            10,
				MaxLength:            72,
				MinCapitalLetters:    2,
				MinSmallLetters:      3,
				MinDigits:            4,
				MinSpecialCharacters: 5,
			},
		}),
	})

	got := uc.PasswordPolicy()
	want := auth.PasswordPolicy{
		MinLength:            10,
		MaxLength:            72,
		MinCapitalLetters:    2,
		MinSmallLetters:      3,
		MinDigits:            4,
		MinSpecialCharacters: 5,
		SpecialCharacters:    password.SpecialCharacters,
	}
	if got != want {
		t.Fatalf("PasswordPolicy() = %+v, want %+v", got, want)
	}
}
