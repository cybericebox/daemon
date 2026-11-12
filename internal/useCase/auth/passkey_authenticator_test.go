package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// WebAuthn authenticator data flags.
const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

// b64 is the unpadded URL-safe base64 WebAuthn uses on the wire.
func b64() *base64.Encoding { return base64.RawURLEncoding }

// softAuthenticator is a software platform authenticator: an ES256 key pair that answers registration and
// sign-in ceremonies the way a browser relays them (the JSON form of PublicKeyCredential).
type softAuthenticator struct {
	t          *testing.T
	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	aaguid     []byte
	origin     string
	rpID       string
	counter    uint32
	// uv and up are the flags the authenticator reports; tests flip them to simulate a bad one.
	uv, up         bool
	backupEligible bool
}

func newSoftAuthenticator(t *testing.T, origin, rpID string) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	credID := make([]byte, 32)
	if _, err = rand.Read(credID); err != nil {
		t.Fatal(err)
	}
	return &softAuthenticator{
		t: t, key: key, credID: credID, aaguid: make([]byte, 16), origin: origin, rpID: rpID,
		uv: true, up: true, backupEligible: true,
	}
}

func (a *softAuthenticator) flags(attested bool) byte {
	var f byte
	if a.up {
		f |= flagUP
	}
	if a.uv {
		f |= flagUV
	}
	if a.backupEligible {
		f |= flagBE | flagBS
	}
	if attested {
		f |= flagAT
	}
	return f
}

func (a *softAuthenticator) authData(attested bool) []byte {
	rpHash := sha256.Sum256([]byte(a.rpID))
	out := append([]byte{}, rpHash[:]...)
	out = append(out, a.flags(attested))
	out = binary.BigEndian.AppendUint32(out, a.counter)
	if !attested {
		return out
	}
	point, err := a.key.PublicKey.Bytes() // uncompressed: 0x04 || X || Y
	if err != nil {
		a.t.Fatal(err)
	}
	x, y := point[1:33], point[33:]
	cose, err := webauthncbor.Marshal(map[int64]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	if err != nil {
		a.t.Fatal(err)
	}
	out = append(out, a.aaguid...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(a.credID)))
	out = append(out, a.credID...)
	return append(out, cose...)
}

func (a *softAuthenticator) clientData(typ, challenge string) []byte {
	raw, err := json.Marshal(
		map[string]any{
			"type":        typ,
			"challenge":   challenge,
			"origin":      a.origin,
			"crossOrigin": false,
		},
	)
	if err != nil {
		a.t.Fatal(err)
	}
	return raw
}

// challengeOf reads the challenge out of the options the server returned.
func challengeOf(t *testing.T, options []byte) string {
	t.Helper()
	var parsed struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.PublicKey.Challenge == "" {
		t.Fatalf("no challenge in options: %s", options)
	}
	return parsed.PublicKey.Challenge
}

// create answers a registration ceremony with an attestation of format "none".
func (a *softAuthenticator) create(options []byte) []byte {
	clientData := a.clientData("webauthn.create", challengeOf(a.t, options))
	attObj, err := webauthncbor.Marshal(
		map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(true)},
	)
	if err != nil {
		a.t.Fatal(err)
	}
	return a.marshal(map[string]any{
		"id":    b64().EncodeToString(a.credID),
		"rawId": b64().EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64().EncodeToString(clientData),
			"attestationObject": b64().EncodeToString(attObj),
			"transports":        []string{"internal", "hybrid"},
		},
	})
}

// get answers a sign-in ceremony; the counter advances first, as a real authenticator's does.
func (a *softAuthenticator) get(options []byte) []byte {
	return a.getWith(options, a.userHandle)
}

func (a *softAuthenticator) getWith(options, userHandle []byte) []byte {
	clientData := a.clientData("webauthn.get", challengeOf(a.t, options))
	authData := a.authData(false)
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	return a.marshal(map[string]any{
		"id":    b64().EncodeToString(a.credID),
		"rawId": b64().EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64().EncodeToString(clientData),
			"authenticatorData": b64().EncodeToString(authData),
			"signature":         b64().EncodeToString(sig),
			"userHandle":        b64().EncodeToString(userHandle),
		},
	})
}

func (a *softAuthenticator) marshal(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		a.t.Fatal(err)
	}
	return raw
}
