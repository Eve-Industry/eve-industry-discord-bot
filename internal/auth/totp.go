package auth

import (
	"bytes"
	"crypto/subtle"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Issuer is the name authenticator apps show above the code.
const Issuer = "EVE Industry Bot"

const (
	totpPeriod = 30
	totpSkew   = 1 // steps either side of now, for clock drift
	qrSize     = 256
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// TOTPSetup is a new authenticator secret for someone to scan.
type TOTPSetup struct {
	Secret string // base32; store it encrypted
	URI    string // otpauth:// link, for apps that take a link
	QRCode []byte // PNG of URI
}

// NewTOTP creates an authenticator secret for account, using the defaults
// every authenticator app supports: SHA-1, 6 digits, 30 seconds.
func NewTOTP(account string) (TOTPSetup, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: account,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
		SecretSize:  20,
	})
	if err != nil {
		return TOTPSetup{}, err
	}
	img, err := key.Image(qrSize, qrSize)
	if err != nil {
		return TOTPSetup{}, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return TOTPSetup{}, err
	}
	return TOTPSetup{Secret: key.Secret(), URI: key.URL(), QRCode: buf.Bytes()}, nil
}

// MatchTOTP checks code against secret around now. It returns the 30-second
// step the code belongs to, so the caller can refuse a step that was already
// used.
func MatchTOTP(secret, code string, now time.Time) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for s := cur - totpSkew; s <= cur+totpSkew; s++ {
		want, err := totp.GenerateCodeCustom(secret, time.Unix(s*totpPeriod, 0), totpOpts)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}
