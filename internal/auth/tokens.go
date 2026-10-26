package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// NewToken returns 32 random bytes, URL-safe, for session cookies, invite
// links and CSRF tokens.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the form of a token kept in the database, so a copy of
// the database can't be used to sign in.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const recoveryAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

var recoveryEncoding = base32.NewEncoding(recoveryAlphabet).WithPadding(base32.NoPadding)

// NewRecoveryCodes returns n one-time codes like "k3j9d-2mf7q".
func NewRecoveryCodes(n int) []string {
	codes := make([]string, n)
	for i := range codes {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		s := recoveryEncoding.EncodeToString(b)[:10]
		codes[i] = s[:5] + "-" + s[5:]
	}
	return codes
}

// HashRecoveryCode returns the stored form of code, ignoring case, spaces
// and dashes so a code typed loosely still matches.
func HashRecoveryCode(code string) string {
	code = strings.ToLower(code)
	code = strings.NewReplacer("-", "", " ", "").Replace(code)
	return HashToken(code)
}

// Box encrypts small secrets, such as authenticator secrets, at rest.
type Box struct {
	aead cipher.AEAD
}

// ParseKey decodes a base64 key of 32 bytes, as made by
// `openssl rand -base64 32`.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := enc.DecodeString(s); err == nil {
			if len(key) != 32 {
				return nil, fmt.Errorf("key is %d bytes, want 32", len(key))
			}
			return key, nil
		}
	}
	return nil, errors.New("key is not base64")
}

// NewBox returns a Box using AES-256-GCM with key.
func NewBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain. The result starts with its random nonce.
func (b *Box) Seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return b.aead.Seal(nonce, nonce, plain, nil)
}

// Open decrypts what Seal returned.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed secret too short")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
