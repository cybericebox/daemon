package app

import "github.com/cybericebox/daemon/pkg/secret"

// cipherOrNil keeps a missing cipher a nil interface: a nil *secret.Cipher stored in an interface
// would look present.
func cipherOrNil(c *secret.Cipher) interface {
	DecryptWithContext(encoded string, context []byte) ([]byte, error)
} {
	if c == nil {
		return nil
	}
	return c
}
