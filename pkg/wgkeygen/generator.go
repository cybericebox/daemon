package wgkeygen

import (
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/curve25519"

)

// keySize defines the size of the key
const keySize = 32

// key is curve25519 key.
// It is used by WireGuard to represent Public and preShared keys.
type key [keySize]byte

// privateKey is curve25519 key.
// It is used by WireGuard to represent private keys.
type privateKey [keySize]byte

type (
	KeyGenerator struct {
	}

	KeyPair struct {
		PublicKey  string
		PrivateKey string
	}
)

func NewKeyGenerator() *KeyGenerator {
	return &KeyGenerator{}
}

// NewKeyPair returns private key, Public key and error
func (g *KeyGenerator) NewKeyPair() (*KeyPair, error) {
	privKey, err := newPrivateKey()
	if err != nil {
		return nil, ErrWgKeyGen.WithError(err).WithMessage("Failed to generate key pair").Err()
	}
	pubKey := privKey.Public()

	return &KeyPair{
		PublicKey:  pubKey.String(),
		PrivateKey: privKey.String(),
	}, nil
}

func (g *KeyGenerator) NewPreSharedKey() (string, error) {
	preShKey, err := newRandomKey()
	if err != nil {
		return "", ErrWgKeyGen.WithError(err).WithMessage("Failed to generate pre-shared key").Err()
	}

	return preShKey.String(), nil
}

// NewPrivateKey generates a new curve25519 secret key.
// It conforms to the format described on https://cr.yp.to/ecdh.html.
func newPrivateKey() (privateKey, error) {
	k, err := newRandomKey()
	if err != nil {
		return privateKey{}, ErrWgKeyGen.WithError(err).WithMessage("Failed to generate private key").Err()
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return (privateKey)(*k), nil
}

// NewPreSharedKey generates a new key
func newRandomKey() (*key, error) {
	var k [keySize]byte
	_, err := rand.Read(k[:])
	if err != nil {
		return nil, ErrWgKeyGen.WithError(err).WithMessage("Failed to generate random key").Err()
	}
	return (*key)(&k), nil
}

// Public computes the Public key matching this curve25519 secret key.
func (k *privateKey) Public() key {
	var p [keySize]byte
	curve25519.ScalarBaseMult(&p, (*[keySize]byte)(k))
	return p
}

// String returns a private key as a string
func (k *privateKey) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// String returns a Public key as a string
func (k *key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}
