package plugin

import (
	"context"
	"fmt"

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
			"app_id": {
				Type:        framework.TypeInt64,
				Description: "GitHub App ID.",
				Required:    true,
			},
			"private_key": {
				Type:        framework.TypeString,
				Description: "PEM-encoded RSA private key for the GitHub App.",
				Required:    true,
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"base_url": {
				Type:        framework.TypeString,
				Description: "GitHub API base URL (GitHub.com default: https://api.github.com). For GitHub Enterprise Server use e.g. https://HOST/api/v3",
				Default:     defaultGitHubAPIBase,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathConfigRead,
				Summary:    "Read configuration (sensitive fields omitted).",
			},
			logical.CreateOperation: &framework.PathOperation{
				Callback: b.pathConfigWrite,
				Summary:    "Configure the secrets engine",
			},
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.pathConfigWrite,
			},
			logical.DeleteOperation: &framework.PathOperation{
				Callback: b.pathConfigDelete,
				Summary:    "Delete configuration",
			},
		},
		HelpSynopsis:    "Configure GitHub App credentials",
		HelpDescription: "Sets app_id, private_key, and optional base_url for the GitHub REST API.",
	}
}

func (b *backend) pathConfigRead(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	entry, err := req.Storage.Get(ctx, "config")
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	var cfg githubConfig
	if err := entry.DecodeJSON(&cfg); err != nil {
		return nil, err
	}
	data := map[string]interface{}{
		"app_id":   cfg.AppID,
		"base_url": cfg.apiBase(),
	}
	return &logical.Response{Data: data}, nil
}

func (b *backend) pathConfigWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	cfg := githubConfig{
		AppID:      int64(data.Get("app_id").(int64)),
		PrivateKey: data.Get("private_key").(string),
	}
	if v, ok := data.GetOk("base_url"); ok {
		cfg.BaseURL = v.(string)
	}
	if cfg.AppID <= 0 {
		return logical.ErrorResponse("app_id must be set to a positive integer"), nil
	}
	if cfg.PrivateKey == "" {
		return logical.ErrorResponse("private_key is required"), nil
	}
	if _, err := parseRSAPrivateKey(cfg.PrivateKey); err != nil {
		return logical.ErrorResponse(fmt.Sprintf("invalid private_key: %v", err)), nil
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

func (b *backend) loadConfig(ctx context.Context, s logical.Storage) (*githubConfig, error) {
	entry, err := s.Get(ctx, "config")
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, fmt.Errorf("configuration is required: write to config/ first")
	}
	var cfg githubConfig
	if err := entry.DecodeJSON(&cfg); err != nil {
		return nil, err
	}
	if cfg.AppID <= 0 || cfg.PrivateKey == "" {
		return nil, fmt.Errorf("configuration is incomplete")
	}
	return &cfg, nil
}
