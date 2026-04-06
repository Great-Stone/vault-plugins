package plugin

import (
	"context"
	"testing"

	"github.com/hashicorp/vault/sdk/logical"
)

func TestRoleWriteRead_StaticBundle(t *testing.T) {
	storage := new(logical.InmemStorage)
	cfg := logical.TestBackendConfig()
	cfg.StorageView = storage

	be, err := Factory(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	// config
	_, err = be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "config",
		Storage:   storage,
		Data: map[string]interface{}{
			"bootstrap_servers": "kafka:9093",
			"security_protocol": "SASL_PLAINTEXT",
			"sasl_mechanism":    "SCRAM-SHA-256",
			"admin_username":    "admin",
			"admin_password":    "admin-secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// role
	_, err = be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "static-roles/static",
		Storage:   storage,
		Data: map[string]interface{}{
			"name":           "static",
			"auth_type":      "plain",
			"static_username": "u",
			"static_password": "p",
			"ttl":            3600,
			"max_ttl":        7200,
			"static_props":   map[string]string{"client.id": "test"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.ReadOperation,
		Path:      "static-creds/static",
		Storage:   storage,
		Data:      map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.IsError() {
		t.Fatalf("unexpected error response: %#v", resp)
	}
	if got := resp.Data["mode"]; got != "static" {
		t.Fatalf("mode got %v", got)
	}
	if got := resp.Data["username"]; got != "u" {
		t.Fatalf("username got %v", got)
	}
}

