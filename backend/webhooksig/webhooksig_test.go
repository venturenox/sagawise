package webhooksig

import (
	"errors"
	"testing"
	"time"
)

// Shared test vector. The Node and Python SDK tests assert the same
// signature for the same inputs, so the three implementations cannot
// drift apart unnoticed.
const (
	vecSecret = "whsec_test_0123456789"
	vecTS     = int64(1757000000)
	vecBody   = `{"order_id":42,"workflow_instance_id":"abc"}`
	vecSig    = "v1=ae24e8081e830be2781f7fdb0f89712f9ab9ba0519cf53d737f558bd3b6de8da"
)

func TestSignVector(t *testing.T) {
	got := Sign([]byte(vecSecret), vecTS, []byte(vecBody))
	if got != vecSig {
		t.Fatalf("Sign = %s\nwant   %s\n(update the SDK tests' vector together with this one)", got, vecSig)
	}
}

func TestVerify(t *testing.T) {
	secret := []byte(vecSecret)
	now := time.Unix(vecTS+30, 0)
	sig := Sign(secret, vecTS, []byte(vecBody))

	check := func(name, ts, sigHdr, body string, at time.Time, want error) {
		t.Helper()
		err := Verify(secret, ts, sigHdr, []byte(body), at, 0)
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
	}
	tsStr := "1757000000"
	check("valid", tsStr, sig, vecBody, now, nil)
	check("rotation: second value matches", tsStr, "v1=00,"+sig, vecBody, now, nil)
	check("missing signature", tsStr, "", vecBody, now, ErrMissing)
	check("missing timestamp", "", sig, vecBody, now, ErrMissing)
	check("malformed timestamp", "soon", sig, vecBody, now, ErrMalformed)
	check("malformed hex", tsStr, "v1=zz", vecBody, now, ErrMalformed)
	check("no v1 value", tsStr, "v0=abcd", vecBody, now, ErrMismatch)
	check("body changed", tsStr, sig, vecBody+" ", now, ErrMismatch)
	check("timestamp changed", "1757000001", sig, vecBody, now, ErrMismatch)
	check("wrong secret", tsStr, Sign([]byte("other"), vecTS, []byte(vecBody)), vecBody, now, ErrMismatch)
	check("replayed 6 minutes later", tsStr, sig, vecBody, time.Unix(vecTS+6*60, 0), ErrExpired)
	check("from 6 minutes in the future", tsStr, sig, vecBody, time.Unix(vecTS-6*60, 0), ErrExpired)
	check("at the tolerance edge", tsStr, sig, vecBody, time.Unix(vecTS+5*60, 0), nil)

	if err := Verify(secret, tsStr, sig, []byte(vecBody), time.Unix(vecTS+90, 0), time.Minute); !errors.Is(err, ErrExpired) {
		t.Errorf("custom tolerance: err = %v, want ErrExpired", err)
	}
}
