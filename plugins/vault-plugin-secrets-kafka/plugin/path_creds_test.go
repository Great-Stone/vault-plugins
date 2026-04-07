package plugin

import (
	"context"
	"testing"
	"time"

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
			"name":            "static",
			"auth_type":       "plain",
			"static_username": "u",
			"static_password": "p",
			"static_props":    map[string]string{"client.id": "test"},
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

func TestStaticRoleWrite_ScramRequiresRotationCron(t *testing.T) {
	storage := new(logical.InmemStorage)
	cfg := logical.TestBackendConfig()
	cfg.StorageView = storage

	be, err := Factory(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, err = be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "config",
		Storage:   storage,
		Data: map[string]interface{}{
			"bootstrap_servers": "kafka:9093",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "static-roles/bad",
		Storage:   storage,
		Data: map[string]interface{}{
			"name":            "bad",
			"auth_type":       "scram",
			"static_username": "u",
			"static_password": "p",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.IsError() {
		t.Fatalf("expected error for scram without rotation_cron, got %#v", resp)
	}
}

func TestRotationRemainingSeconds(t *testing.T) {
	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	future := now.Add(90 * time.Second)
	if got := rotationRemainingSeconds(future.Format(time.RFC3339), now); got != 90 {
		t.Fatalf("future: got %d want 90", got)
	}
	past := now.Add(-10 * time.Second)
	if got := rotationRemainingSeconds(past.Format(time.RFC3339), now); got != 0 {
		t.Fatalf("past: got %d want 0", got)
	}
	if got := rotationRemainingSeconds("", now); got != 0 {
		t.Fatalf("empty: got %d want 0", got)
	}
	nano := now.Add(30 * time.Second).Format(time.RFC3339Nano)
	if got := rotationRemainingSeconds(nano, now); got != 30 {
		t.Fatalf("rfc3339nano: got %d want 30", got)
	}
}

func TestStaticCredsRead_ScramRotationTTL(t *testing.T) {
	storage := new(logical.InmemStorage)
	cfg := logical.TestBackendConfig()
	cfg.StorageView = storage

	be, err := Factory(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, err = be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "config",
		Storage:   storage,
		Data: map[string]interface{}{
			"bootstrap_servers": "localhost:19093",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.CreateOperation,
		Path:      "static-roles/app_scram",
		Storage:   storage,
		Data: map[string]interface{}{
			"name":            "app_scram",
			"auth_type":       "scram",
			"scram_mechanism": "SCRAM-SHA-256",
			"static_username": "u",
			"static_password": "p",
			"rotation_cron":   "0 * * * *",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	r, err := getStaticRole(context.Background(), storage, "app_scram")
	if err != nil || r == nil {
		t.Fatal(err)
	}
	r.NextRotationAt = time.Now().UTC().Add(100 * time.Second).Format(time.RFC3339)
	if err := putStaticRole(context.Background(), storage, "app_scram", r); err != nil {
		t.Fatal(err)
	}

	resp, err := be.HandleRequest(context.Background(), &logical.Request{
		Operation: logical.ReadOperation,
		Path:      "static-creds/app_scram",
		Storage:   storage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.IsError() {
		t.Fatalf("response: %#v", resp)
	}
	raw, ok := resp.Data["rotation_ttl"]
	if !ok {
		t.Fatal("expected rotation_ttl in response data")
	}
	ttl, ok := raw.(int)
	if !ok {
		t.Fatalf("rotation_ttl type got %T", raw)
	}
	if ttl < 90 || ttl > 100 {
		t.Fatalf("rotation_ttl=%d want ~100", ttl)
	}
}
