// Package secretbox seals small secrets (app keys, tokens) before they are
// stored, and signs short-lived values handed to a browser, each under a key
// derived from one server secret and the purpose it is for.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// Box seals and signs for one purpose.
type Box struct {
	aead cipher.AEAD
	sign []byte
}

// ErrNoSecret is returned when the server has no secret to derive keys from.
var ErrNoSecret = errors.New("no server secret is set (TEAMUP_AUTH_SECRET)")

// New derives a box for `purpose` from `secret`.
func New(secret, purpose string) (*Box, error) {
	if len(secret) < 16 {
		return nil, ErrNoSecret
	}
	derive := func(use string) ([]byte, error) {
		key := make([]byte, 32)
		_, err := io.ReadFull(hkdf.New(sha256.New, []byte(secret), nil, []byte(purpose+" "+use)), key)
		return key, err
	}
	sealKey, err := derive("seal")
	if err != nil {
		return nil, err
	}
	signKey, err := derive("sign")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(sealKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, sign: signKey}, nil
}

// Seal encrypts v, as JSON.
func (b *Box) Seal(v any) ([]byte, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

// Open decrypts what Seal made into v.
func (b *Box) Open(sealed []byte, v any) error {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return errors.New("sealed value is damaged")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return errors.New("sealed value cannot be read with this server secret")
	}
	return json.Unmarshal(plain, v)
}

// Token signs v with an expiry, for a value that goes out to a browser or a
// provider and has to come back unchanged.
func (b *Box) Token(v any, life time.Duration) (string, error) {
	payload, err := json.Marshal(struct {
		V any   `json:"v"`
		E int64 `json:"e"`
	}{v, time.Now().Add(life).Unix()})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(b.mac(body)), nil
}

// ErrBadToken is a token not made here, changed, or expired.
var ErrBadToken = errors.New("that link has expired or was not made here")

// ReadToken checks a token made by Token and reads its value into v.
func (b *Box) ReadToken(token string, v any) error {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return ErrBadToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, b.mac(body)) {
		return ErrBadToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return ErrBadToken
	}
	var wrapped struct {
		V json.RawMessage `json:"v"`
		E int64           `json:"e"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || time.Now().Unix() > wrapped.E {
		return ErrBadToken
	}
	if err := json.Unmarshal(wrapped.V, v); err != nil {
		return fmt.Errorf("%w: %v", ErrBadToken, err)
	}
	return nil
}

func (b *Box) mac(body string) []byte {
	m := hmac.New(sha256.New, b.sign)
	m.Write([]byte(body))
	return m.Sum(nil)
}
