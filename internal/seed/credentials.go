package seed

import (
	"bufio"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/cybericebox/daemon/pkg/password"
)

// DefaultCredentialsPath is where the seeded accounts and their password go. The file is
// gitignored; the password is never printed or logged.
const DefaultCredentialsPath = ".seed-credentials"

const passwordKey = "password:"

// credentialLine is one account of the credentials file.
type credentialLine struct {
	Email string
	Role  string
	Team  string
}

// readPassword returns the shared password of a previous run, so a re-run keeps it. Empty when
// there is no usable file.
func readPassword(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), passwordKey); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// writeCredentials rewrites the file (mode 0600) with the shared password and every account.
func writeCredentials(path, header, plain string, lines []credentialLine) error {
	var out strings.Builder
	out.WriteString("# Cyber ICE Box seed accounts. Dev only, gitignored, rewritten by every `make seed`.\n")
	for line := range strings.SplitSeq(header, "\n") {
		out.WriteString("# " + line + "\n")
	}
	out.WriteString(passwordKey + " " + plain + "\n")
	out.WriteString("# email\trole\tteam\n")
	for _, line := range lines {
		out.WriteString(line.Email + "\t" + line.Role + "\t" + line.Team + "\n")
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func removeCredentials(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove credentials: %w", err)
	}
	return nil
}

// generatePassword builds a password that passes the platform policy: the required count of
// every character class, padded to a comfortable length.
func generatePassword(policy password.ComplexityConfig) (string, error) {
	const (
		capitals = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		smalls   = "abcdefghijkmnpqrstuvwxyz"
		digits   = "23456789"
	)
	length := max(policy.MinLength, 16)
	if policy.MaxLength > 0 {
		length = min(length, policy.MaxLength)
	}
	var chars []byte
	add := func(alphabet string, count int) error {
		for range count {
			index, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				return err
			}
			chars = append(chars, alphabet[index.Int64()])
		}
		return nil
	}
	for _, class := range []struct {
		alphabet string
		count    int
	}{
		{capitals, max(policy.MinCapitalLetters, 1)},
		{smalls, max(policy.MinSmallLetters, 1)},
		{digits, max(policy.MinDigits, 1)},
		{"!#$%&*+-.", policy.MinSpecialCharacters},
	} {
		if err := add(class.alphabet, class.count); err != nil {
			return "", err
		}
	}
	if len(chars) < length {
		if err := add(smalls+capitals+digits, length-len(chars)); err != nil {
			return "", err
		}
	}
	for i := len(chars) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		chars[i], chars[j.Int64()] = chars[j.Int64()], chars[i]
	}
	return string(chars), nil
}
