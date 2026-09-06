// Package webhooksig signs and verifies Sagawise failure webhooks so a
// receiver can tell a real compensation request from a forged one
// (contract W6, threat model T4).
//
// Scheme. With a shared secret configured, every delivery carries
//
//	X-Sagawise-Timestamp: <unix seconds>
//	X-Sagawise-Signature: v1=<hex HMAC-SHA256(secret, "<timestamp>.<body>")>
//
// The timestamp is part of the signed input, so a captured delivery cannot
// be replayed once it is older than the receiver's tolerance. The body is
// signed byte for byte, so receivers must verify the raw request body
// before any JSON parsing re-serialises it. The same scheme is implemented
// in the Node and Python SDKs (verify_signature); the three share the test
// vector in this package's tests.
package webhooksig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Header names.
const (
	HeaderTimestamp = "X-Sagawise-Timestamp"
	HeaderSignature = "X-Sagawise-Signature"
)

// DefaultTolerance is how far a delivery's timestamp may be from the
// receiver's clock before Verify rejects it as a replay.
const DefaultTolerance = 5 * time.Minute

var (
	ErrMissing   = errors.New("webhook signature: header missing")
	ErrMalformed = errors.New("webhook signature: header malformed")
	ErrExpired   = errors.New("webhook signature: timestamp outside tolerance")
	ErrMismatch  = errors.New("webhook signature: mismatch")
)

// Sign returns the value of the signature header for a body sent at ts.
func Sign(secret []byte, ts int64, body []byte) string {
	return "v1=" + hex.EncodeToString(mac(secret, ts, body))
}

func mac(secret []byte, ts int64, body []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(strconv.FormatInt(ts, 10)))
	h.Write([]byte{'.'})
	h.Write(body)
	return h.Sum(nil)
}

// Verify checks a received delivery: tsHeader and sigHeader are the two
// header values as received, body is the raw request body, now is the
// receiver's clock and tolerance the accepted clock skew (0 means
// DefaultTolerance). It returns nil only when the signature is valid and
// the timestamp is within tolerance.
func Verify(secret []byte, tsHeader, sigHeader string, body []byte, now time.Time, tolerance time.Duration) error {
	if tsHeader == "" || sigHeader == "" {
		return ErrMissing
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(tsHeader), 10, 64)
	if err != nil {
		return ErrMalformed
	}
	if tolerance <= 0 {
		tolerance = DefaultTolerance
	}
	if d := now.Unix() - ts; d > int64(tolerance/time.Second) || -d > int64(tolerance/time.Second) {
		return ErrExpired
	}
	// Several v1= values may be present (a secret rotation sends both);
	// any one that matches is enough.
	matched := false
	for _, part := range strings.Split(sigHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "v1=") {
			continue
		}
		got, err := hex.DecodeString(part[3:])
		if err != nil {
			return ErrMalformed
		}
		if hmac.Equal(got, mac(secret, ts, body)) {
			matched = true
		}
	}
	if !matched {
		return ErrMismatch
	}
	return nil
}
