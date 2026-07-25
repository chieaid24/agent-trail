package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	secret := []byte("webhook-secret")
	body := []byte(`{"action":"created"}`)

	cases := []struct {
		name   string
		secret []byte
		header string
		want   bool
	}{
		{"valid", secret, sign(secret, body), true},
		{"wrong secret", secret, sign([]byte("other"), body), false},
		{"tampered body", secret, sign(secret, []byte(`{"action":"x"}`)), false},
		{"missing header", secret, "", false},
		{"wrong scheme", secret, "sha1=deadbeef", false},
		{"not hex", secret, "sha256=zzzz", false},
		{"empty secret", nil, sign(nil, body), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidSignature(tc.secret, body, tc.header); got != tc.want {
				t.Fatalf("ValidSignature() = %v, want %v", got, tc.want)
			}
		})
	}
}
