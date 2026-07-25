package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

func testKeyPEM(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return pem.EncodeToMemory(block), key
}

func newTestClient(t *testing.T, baseURL string) (*Client, *rsa.PrivateKey) {
	t.Helper()
	pemBytes, key := testKeyPEM(t)
	c, err := NewClient("12345", pemBytes, baseURL, observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return c, key
}

func TestAppJWTSignedAndClaimed(t *testing.T) {
	c, key := newTestClient(t, "http://unused")
	jwt, err := c.appJWT()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}

	claimBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimBytes, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != "12345" {
		t.Fatalf("iss = %q", claims.Iss)
	}
	if claims.Exp <= claims.Iat || claims.Exp-claims.Iat > 11*60 {
		t.Fatalf("implausible claim window: iat=%d exp=%d", claims.Iat, claims.Exp)
	}
}

func TestInstallationTokenCachedUntilExpiry(t *testing.T) {
	var mints atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42/access_tokens" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing bearer JWT")
		}
		mints.Add(1)
		fmt.Fprintf(w, `{"token":"tok-%d","expires_at":%q}`,
			mints.Load(), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	ctx := context.Background()

	first, err := c.InstallationToken(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.InstallationToken(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || mints.Load() != 1 {
		t.Fatalf("token not cached: %q vs %q, mints=%d", first, second, mints.Load())
	}

	// Within the refresh margin the token is re-minted.
	c.now = func() time.Time { return time.Now().Add(56 * time.Minute) }
	if _, err := c.InstallationToken(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if mints.Load() != 2 {
		t.Fatalf("expiring token not refreshed: mints=%d", mints.Load())
	}
}

func TestListInstallationRepositoriesPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") {
			fmt.Fprintf(w, `{"token":"tok","expires_at":%q}`,
				time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			return
		}
		page := r.URL.Query().Get("page")
		if page == "1" {
			// A full page (100 entries) forces a second request.
			var b strings.Builder
			b.WriteString(`{"repositories":[`)
			for i := range 100 {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"id":%d,"name":"r%d","full_name":"o/r%d","owner":{"login":"o"}}`, i+1, i+1, i+1)
			}
			b.WriteString(`]}`)
			fmt.Fprint(w, b.String())
			return
		}
		fmt.Fprint(w, `{"repositories":[{"id":101,"name":"last","full_name":"o/last","owner":{"login":"o"}}]}`)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	repos, err := c.ListInstallationRepositories(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 101 {
		t.Fatalf("got %d repositories, want 101", len(repos))
	}
	if repos[100].FullName != "o/last" {
		t.Fatalf("last repo = %+v", repos[100])
	}
}

func TestAPIErrorSurfacesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") {
			fmt.Fprintf(w, `{"token":"tok","expires_at":%q}`,
				time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			return
		}
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	_, err := c.CollaboratorPermission(context.Background(), 7, "o", "r", "user")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want *APIError 404", err)
	}
}

func TestParsePrivateKeyRejectsGarbage(t *testing.T) {
	if _, err := NewClient("1", []byte("not a pem"), "", observability.NewRegistry()); err == nil {
		t.Fatal("garbage key accepted")
	}
}
