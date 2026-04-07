package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathConfig(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "config",
		ExistenceCheck: func(ctx context.Context, req *logical.Request, _ *framework.FieldData) (bool, error) {
			e, err := req.Storage.Get(ctx, "config")
			return e != nil, err
		},
		Fields: map[string]*framework.FieldSchema{
			"bootstrap_servers": {
				Type:        framework.TypeString,
				Description: "Kafka bootstrap servers for client connections (e.g. kafka:9093).",
				Required:    true,
			},
			"admin_bootstrap_servers": {
				Type:        framework.TypeString,
				Description: "Optional bootstrap servers for admin operations (defaults to bootstrap_servers).",
			},
			"security_protocol": {
				Type:        framework.TypeString,
				Description: "Optional security protocol (PLAINTEXT, SASL_PLAINTEXT, SASL_SSL, SSL).",
			},
			"sasl_mechanism": {
				Type:        framework.TypeString,
				Description: "Optional SASL mechanism (e.g. SCRAM-SHA-256).",
			},
			"admin_username": {
				Type:        framework.TypeString,
				Description: "Optional admin username for authenticated clusters.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"admin_password": {
				Type:        framework.TypeString,
				Description: "Optional admin password for authenticated clusters.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{Callback: b.pathConfigRead},
			logical.CreateOperation: &framework.PathOperation{
				Callback: b.pathConfigWrite,
			},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathConfigWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathConfigDelete},
		},
		HelpSynopsis:    "Configure Kafka connection settings",
		HelpDescription: "Sets bootstrap servers and optional admin authentication settings.",
	}
}

func (b *backend) pathConfigRead(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	cfg, err := b.loadConfig(ctx, req.Storage)
	if err != nil {
		// Return nil on not configured to match common Vault pattern.
		if strings.Contains(err.Error(), "configuration is required") {
			return nil, nil
		}
		return nil, err
	}
	out := map[string]interface{}{
		"bootstrap_servers":       cfg.BootstrapServers,
		"admin_bootstrap_servers": cfg.AdminBootstrapServers,
		"security_protocol":       cfg.SecurityProtocol,
		"sasl_mechanism":          cfg.SaslMechanism,
		// omit admin_username/password
	}
	return &logical.Response{Data: out}, nil
}

func (b *backend) pathConfigWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	cfg := kafkaConfig{
		BootstrapServers:      strings.TrimSpace(data.Get("bootstrap_servers").(string)),
		AdminBootstrapServers: strings.TrimSpace(data.Get("admin_bootstrap_servers").(string)),
		SecurityProtocol:      strings.TrimSpace(data.Get("security_protocol").(string)),
		SaslMechanism:         strings.TrimSpace(data.Get("sasl_mechanism").(string)),
		AdminUsername:         strings.TrimSpace(data.Get("admin_username").(string)),
		AdminPassword:         data.Get("admin_password").(string),
	}
	if cfg.BootstrapServers == "" {
		return logical.ErrorResponse("bootstrap_servers is required"), nil
	}
	if (cfg.AdminUsername != "" || cfg.AdminPassword != "") && (cfg.AdminUsername == "" || cfg.AdminPassword == "") {
		return logical.ErrorResponse("admin_username and admin_password must be set together"), nil
	}
	entry, err := logical.StorageEntryJSON("config", cfg)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, entry); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathConfigDelete(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	if err := req.Storage.Delete(ctx, "config"); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) loadConfig(ctx context.Context, s logical.Storage) (*kafkaConfig, error) {
	e, err := s.Get(ctx, "config")
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("configuration is required: write to config/ first")
	}
	var cfg kafkaConfig
	if err := e.DecodeJSON(&cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.BootstrapServers) == "" {
		return nil, fmt.Errorf("configuration is incomplete")
	}
	return &cfg, nil
}
