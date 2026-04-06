package plugin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/vault/sdk/logical"
)

func TestIssueInstallationToken_EndToEnd(t *testing.T) {
	storage := new(logical.InmemStorage)
	cfg := logical.TestBackendConfig()
	cfg.StorageView = storage

	be, err := Factory(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	b := be.(*backend)

	privPEM := testRSAPrivateKeyPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/42/access_tokens" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":       "ghs_testtoken",
			"expires_at":  time.Now().UTC().Add(59 * time.Minute).Format(time.RFC3339),
			"permissions": map[string]string{"contents": "read"},
		})
	}))
	defer srv.Close()

	b.httpClient = srv.Client()

	_, err = b.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "config",
		Storage:   storage,
		Data: map[string]interface{}{
			"app_id":      int64(123),
			"private_key": privPEM,
			"base_url":    srv.URL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = b.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "roles/ci",
		Storage:   storage,
		Data: map[string]interface{}{
			"name":            "ci",
			"installation_id": int64(42),
			"ttl":             3600,
			"max_ttl":         7200,
			"permissions":     map[string]string{"contents": "read"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := b.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.ReadOperation,
		Path:      "creds/ci",
		Storage:   storage,
		Data:      map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.IsError() {
		t.Fatalf("error response: %v", resp.Error())
	}
	if got := resp.Data["token"]; got != "ghs_testtoken" {
		t.Fatalf("token: got %v", got)
	}
}

func testRSAPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}
