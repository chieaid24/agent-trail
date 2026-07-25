// Package github implements the GitHub App integration: webhook intake with
// signature validation and delivery dedup, installation and repository sync,
// and the /agent-trail run command flow. Spec:
// docs/architecture/github-app.md.
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ValidSignature reports whether header is the correct X-Hub-Signature-256
// value ("sha256=<hex hmac>") for body under secret. Comparison is
// constant time.
func ValidSignature(secret, body []byte, header string) bool {
	if len(secret) == 0 {
		return false
	}
	hexDigest, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexDigest)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
