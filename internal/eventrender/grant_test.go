package eventrender

import (
	"encoding/base64"
	"encoding/json"
	"github.com/gofrs/uuid"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestGrantPurposeBoundedAndStable(t *testing.T) {
	now := time.Unix(1800000000, 0)
	signer, err := New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	require.NoError(t, err)
	replica, err := New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	require.NoError(t, err)
	require.Equal(t, signer.PublicKey(), replica.PublicKey())
	eventID := uuid.Must(uuid.NewV4())
	userID := uuid.Must(uuid.NewV4())
	sid := uuid.Must(uuid.NewV4())
	challenge := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	token, exp, err := signer.Issue("https://summer.example.test", eventID, userID, sid, true, challenge, now)
	require.NoError(t, err)
	require.Equal(t, 5*time.Minute, MaxLifetime)
	require.Equal(t, now.Add(5*time.Minute).Unix(), exp)
	claims, err := replica.Verify(token, "https://summer.example.test", eventID, now)
	require.NoError(t, err)
	require.True(t, claims.Preview)
	require.Equal(t, challenge, claims.Challenge)
	for _, tc := range []struct {
		name, aud string
		event     uuid.UUID
		at        time.Time
	}{
		{"wrong host", "https://other.example.test", eventID, now},
		{"wrong event", "https://summer.example.test", uuid.Must(uuid.NewV4()), now},
		{"expired", "https://summer.example.test", eventID, now.Add(5 * time.Minute)},
		{"future issued", "https://summer.example.test", eventID, now.Add(-time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) { _, err := signer.Verify(token, tc.aud, tc.event, tc.at); require.Error(t, err) })
	}
	parts := strings.Split(token, ".")
	var payload map[string]any
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, json.Unmarshal(raw, &payload))
	payload["challenge"] = "tampered"
	raw, _ = json.Marshal(payload)
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	_, err = signer.Verify(strings.Join(parts, "."), "https://summer.example.test", eventID, now)
	require.Error(t, err)
	_, _, err = signer.Issue("https://summer.example.test", eventID, userID, sid, false, "bad", now)
	require.Error(t, err)
	_, err = signer.Verify(strings.Repeat("x", 8193), "https://summer.example.test", eventID, now)
	require.Error(t, err)
}

func TestGrantWireAudienceIsExactOriginString(t *testing.T) {
	s, err := New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	require.NoError(t, err)
	token, _, err := s.Issue("https://summer.example.test", uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), true, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", time.Unix(1800000000, 0))
	require.NoError(t, err)
	var wire map[string]any
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &wire))
	require.Equal(t, "https://summer.example.test", wire["aud"])
}

func TestGrantRejectsSignedWrongPurposeAndMalformedClaims(t *testing.T) {
	s, err := New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	require.NoError(t, err)
	now := time.Unix(1800000000, 0)
	id := uuid.Must(uuid.NewV4())
	valid := map[string]any{"iss": Issuer, "aud": "https://summer.example.test", "sub": uuid.Must(uuid.NewV4()).String(), "sid": uuid.Must(uuid.NewV4()).String(), "event_id": id.String(), "scope": Scope, "preview": true, "challenge": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()}
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"issuer", "iss", "session"}, {"scope", "scope", "*"}, {"subject UUID", "sub", "not-a-uuid"}, {"nil session", "sid", uuid.Nil.String()}, {"long lifetime", "exp", now.Add(5*time.Minute + time.Second).Unix()}, {"missing issued", "iat", nil}, {"missing expiry", "exp", nil}, {"invalid challenge", "challenge", "bad"}, {"audience array", "aud", []string{"https://summer.example.test"}}, {"invalid preview", "preview", "true"}, {"zero lifetime", "exp", now.Unix()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			for k, v := range valid {
				payload[k] = v
			}
			payload[tc.key] = tc.value
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims(payload))
			token.Header["kid"] = s.PublicKey().KeyID
			token.Header["typ"] = Type
			signed, err := token.SignedString(s.key)
			require.NoError(t, err)
			_, err = s.Verify(signed, "https://summer.example.test", id, now)
			require.Error(t, err)
		})
	}
	for _, header := range []struct{ key, value string }{{"typ", "JWT"}, {"kid", "wrong"}, {"alg", "HS256"}} {
		t.Run(header.key, func(t *testing.T) {
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims(valid))
			token.Header["kid"] = s.PublicKey().KeyID
			token.Header["typ"] = Type
			token.Header[header.key] = header.value
			signed, err := token.SignedString(s.key)
			require.NoError(t, err)
			_, err = s.Verify(signed, "https://summer.example.test", id, now)
			require.Error(t, err)
		})
	}
}

func TestSessionKeyringCompatibilityAndRotation(t *testing.T) {
	const oldKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	const newKey = "fffefdfcfbfaf9f8f7f6f5f4f3f2f1f0efeeedecebeae9e8e7e6e5e4e3e2e1e0"
	raw, err := New(oldKey)
	require.NoError(t, err)
	named, err := New("current:" + oldKey)
	require.NoError(t, err)
	require.Equal(t, raw.PublicKey(), named.PublicKey())
	rotated, err := New("current:" + newKey + ",previous:" + oldKey)
	require.NoError(t, err)
	primary, err := New(newKey)
	require.NoError(t, err)
	require.Equal(t, primary.PublicKey(), rotated.PublicKey())
	require.NotEqual(t, raw.PublicKey(), rotated.PublicKey())
	now := time.Unix(1800000000, 0)
	eventID := uuid.Must(uuid.NewV4())
	token, _, err := raw.Issue("https://summer.example.test", eventID, uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), true, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", now)
	require.NoError(t, err)
	_, err = rotated.Verify(token, "https://summer.example.test", eventID, now)
	require.NoError(t, err)
	_, err = rotated.Verify(token, "https://summer.example.test", eventID, now.Add(5*time.Minute))
	require.Error(t, err)
	_, err = primary.Verify(token, "https://summer.example.test", eventID, now)
	require.Error(t, err)
	for _, invalid := range []string{"", "current:bad", "a:" + oldKey + ",a:" + newKey, "INVALID:" + oldKey} {
		_, err := New(invalid)
		require.Error(t, err)
	}
}
