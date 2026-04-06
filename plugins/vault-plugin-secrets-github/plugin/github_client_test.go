package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestNormalizeRepoNamesForGitHubInstallation(t *testing.T) {
	got := normalizeRepoNamesForGitHubInstallation([]string{"example-org/example-repo", "  ", "plain"})
	want := []string{"example-repo", "plain"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestRevokeInstallationAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/installation/token" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := revokeInstallationAccessToken(context.Background(), srv.Client(), srv.URL, "test-token"); err != nil {
		t.Fatal(err)
	}
}

func TestParseRSAPrivateKey_PKCS1(t *testing.T) {
	pem := testRSAPrivateKeyPEM(t)
	if _, err := parseRSAPrivateKey(pem); err != nil {
		t.Fatal(err)
	}
}
