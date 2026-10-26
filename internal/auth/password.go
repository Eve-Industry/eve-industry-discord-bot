// Package auth holds the building blocks of dashboard sign-in: password
// hashing, authenticator codes, recovery codes, tokens and secret encryption.
package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id settings recommended by the OWASP Password Storage Cheat Sheet.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// Password length limits. The upper one only stops absurd inputs.
const (
	MinPasswordLen = 12
	MaxPasswordLen = 128
)

// ErrWeakPassword is returned for passwords the policy rejects. Its message
// is safe to show.
var ErrWeakPassword = errors.New("weak password")

// commonPasswords lists leaked passwords of at least 12 characters, from the
// NCSC's 100k most-used list as published in SecLists, lower-cased.
//
//go:embed common-passwords.txt
var commonPasswordsText string

var commonPasswords = sync.OnceValue(func() map[string]bool {
	m := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(commonPasswordsText))
	for sc.Scan() {
		m[sc.Text()] = true
	}
	return m
})

// CheckPasswordPolicy reports why pw can't be used, or nil. Long passphrases
// are encouraged; there are no rules about character classes.
func CheckPasswordPolicy(pw string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLen:
		return fmt.Errorf("%w: use at least %d characters", ErrWeakPassword, MinPasswordLen)
	case n > MaxPasswordLen:
		return fmt.Errorf("%w: use at most %d characters", ErrWeakPassword, MaxPasswordLen)
	case commonPasswords()[strings.ToLower(pw)]:
		return fmt.Errorf("%w: this password appears in leaked password lists", ErrWeakPassword)
	}
	return nil
}

// HashPassword returns an Argon2id hash of pw in the usual PHC string form.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// dummyHash is checked when there is no real hash, so a missing account
// takes as long to reject as a wrong password.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword(NewToken())
	return h
})

// CheckPassword reports whether pw matches hash. An empty hash never
// matches but costs the same time as a real check.
func CheckPassword(hash, pw string) bool {
	if hash == "" {
		checkHash(dummyHash(), pw)
		return false
	}
	return checkHash(hash, pw)
}

func checkHash(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
