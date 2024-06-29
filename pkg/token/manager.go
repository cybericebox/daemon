package token

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gofrs/uuid"
	"github.com/golang-jwt/jwt"
	"time"
)

const (
	tokenTypeAccess int8 = iota
	tokenTypeRefresh
)

var InvalidJWTToken = errors.New("invalid JWT Token")

type Manager struct {
	signingKey string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewTokenManager(signingKey string, accessTTL, refreshTTL time.Duration) (*Manager, error) {
	if signingKey == "" {
		return nil, errors.New("empty signing key")
	}

	if accessTTL <= 0 || refreshTTL <= 0 {
		return nil, errors.New("TTL must be greater than 0")
	}

	return &Manager{signingKey: signingKey, accessTTL: accessTTL, refreshTTL: refreshTTL}, nil
}

func (m *Manager) NewAccessToken(subject interface{}, ttl ...time.Duration) (string, error) {
	// If no TTL is provided, use the default access TTL
	accessTTL := m.accessTTL

	// If a TTL is provided, use that instead
	if len(ttl) == 1 {
		accessTTL = ttl[0]
	}
	return m.newToken(subject, accessTTL, tokenTypeAccess)
}

func (m *Manager) NewRefreshToken(subject interface{}, ttl ...time.Duration) (string, error) {
	// If no TTL is provided, use the default refresh TTL
	refreshTTL := m.refreshTTL

	// If a TTL is provided, use that instead
	if len(ttl) == 1 {
		refreshTTL = ttl[0]
	}

	return m.newToken(subject, refreshTTL, tokenTypeRefresh)
}

func (m *Manager) NewBase64Token(subject interface{}, ttl time.Duration) (string, error) {
	strToken, err := m.newToken(subject, ttl)
	if err != nil {
		return "", err
	}

	bs64Token := base64.StdEncoding.EncodeToString([]byte(strToken))

	return bs64Token, nil
}

func (m *Manager) ParseAccessToken(Token string) (interface{}, error) {
	// Parse the token
	token, err := m.parseToken(Token)
	if err != nil {
		return "", err
	}

	// Check if the token is valid
	claims, ok := token.Claims.(jwt.MapClaims)
	// If the token is not valid, return an error
	if !ok || int8(claims["token"].(float64)) != tokenTypeAccess {
		return "", InvalidJWTToken
	}

	return claims["sub"], nil
}

func (m *Manager) ParseRefreshToken(Token string) (interface{}, error) {
	// Parse the token
	token, err := m.parseToken(Token)
	if err != nil {
		return "", err
	}

	// Check if the token is valid
	claims, ok := token.Claims.(jwt.MapClaims)
	// If the token is not valid, return an error
	if !ok || int8(claims["token"].(float64)) != tokenTypeRefresh {
		return "", InvalidJWTToken
	}

	return claims["sub"], nil
}

func (m *Manager) ParseBase64Token(base64Token string) (interface{}, error) {
	// Decode the base64 token
	Token, err := base64.StdEncoding.DecodeString(base64Token)
	if err != nil {
		return nil, err
	}

	// Parse the token
	token, err := m.parseToken(string(Token))
	if err != nil {
		return "", err
	}

	// Check if the token is valid
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", InvalidJWTToken
	}

	return claims["sub"], nil
}

func (m *Manager) GetAccessTokenTTL() time.Duration {
	return m.accessTTL
}

func (m *Manager) GetRefreshTokenTTL() time.Duration {
	return m.refreshTTL
}

func (m *Manager) newToken(subject interface{}, tokenTTL time.Duration, tokenType ...int8) (string, error) {
	tokenClaims := jwt.MapClaims{}
	tokenClaims["exp"] = time.Now().Add(tokenTTL).Unix()
	tokenClaims["jti"] = uuid.Must(uuid.NewV4())
	tokenClaims["sub"] = subject
	// If a token type is provided, add it to the token
	if len(tokenType) > 0 {
		tokenClaims["token"] = tokenType[0]
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenClaims)

	return token.SignedString([]byte(m.signingKey))
}

func (m *Manager) parseToken(Token string) (*jwt.Token, error) {
	return jwt.Parse(Token, func(token *jwt.Token) (i interface{}, err error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		return []byte(m.signingKey), nil
	})
}
