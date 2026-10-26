package auth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q, want OWASP Argon2id settings", h)
	}
	if !CheckPassword(h, "correct horse battery staple") {
		t.Error("right password rejected")
	}
	if CheckPassword(h, "correct horse battery stapler") {
		t.Error("wrong password accepted")
	}
	if CheckPassword("", "anything at all") {
		t.Error("empty hash accepted a password")
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, pw := range []string{"short", "123456789012", strings.Repeat("a", 129)} {
		if err := CheckPasswordPolicy(pw); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("CheckPasswordPolicy(%q) = %v, want ErrWeakPassword", pw, err)
		}
	}
	if err := CheckPasswordPolicy("tritanium for the capital yard"); err != nil {
		t.Errorf("passphrase rejected: %v", err)
	}
}

func TestTOTP(t *testing.T) {
	setup, err := NewTOTP("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(setup.QRCode, []byte("\x89PNG")) {
		t.Error("QR code is not a PNG")
	}
	if !strings.Contains(setup.URI, "issuer=EVE") {
		t.Errorf("URI %q lacks the issuer", setup.URI)
	}

	now := time.Unix(1_700_000_000, 0)
	code, err := totp.GenerateCode(setup.Secret, now)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := MatchTOTP(setup.Secret, code, now)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("MatchTOTP = %d, %v; want step %d", step, ok, now.Unix()/30)
	}
	if _, ok := MatchTOTP(setup.Secret, code, now.Add(30*time.Second)); !ok {
		t.Error("code from one step ago rejected; drift should be allowed")
	}
	if _, ok := MatchTOTP(setup.Secret, code, now.Add(2*time.Minute)); ok {
		t.Error("code from four steps ago accepted")
	}
	if _, ok := MatchTOTP(setup.Secret, "12345", now); ok {
		t.Error("five-digit code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes := NewRecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' {
			t.Errorf("code %q is not like xxxxx-xxxxx", c)
		}
		seen[c] = true
	}
	if len(seen) != 10 {
		t.Error("recovery codes repeat")
	}
	c := codes[0]
	if HashRecoveryCode(c) != HashRecoveryCode(strings.ToUpper(strings.ReplaceAll(c, "-", " "))) {
		t.Error("loosely typed code doesn't match")
	}
}

func TestBox(t *testing.T) {
	key, err := ParseKey("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed := b.Seal([]byte("JBSWY3DPEHPK3PXP"))
	got, err := b.Open(sealed)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open(sealed); err == nil {
		t.Error("tampered secret decrypted")
	}
	if _, err := ParseKey("dG9vIHNob3J0"); err == nil {
		t.Error("short key accepted")
	}
}
