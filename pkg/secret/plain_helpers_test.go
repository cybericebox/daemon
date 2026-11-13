package secret

// Tests seal and open plain strings under one fixed test context; production code has no way to seal without a context.
const testContext = "test-context"

func sealPlain(c *Cipher, plain string) (string, error) {
	return c.EncryptWithContext([]byte(plain), []byte(testContext))
}

func openPlain(c *Cipher, sealed string) (string, error) {
	plain, err := c.DecryptWithContext(sealed, []byte(testContext))
	return string(plain), err
}
