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
		Fields: map[string]*framework.FieldSchema{
			"rp_id": {
				Type:        framework.TypeString,
				Description: "WebAuthn Relying Party ID (usually a DNS name, e.g. vault.example.com).",
				Required:    true,
			},
			"allowed_origins": {
				Type:        framework.TypeCommaStringSlice,
				Description: "Allowed browser origins for WebAuthn clientDataJSON verification (e.g. https://vault.example.com).",
				Required:    true,
			},
			"allowed_user_handle_regex": {
				Type:        framework.TypeString,
				Description: "Optional global regex for user_handle (applies to both register and login).",
				Required:    false,
			},
			"challenge_ttl": {
				Type:        framework.TypeString,
				Description: "Challenge TTL duration (e.g. 2m). Defaults to 2m.",
				Required:    false,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.handleConfigRead,
			},
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.handleConfigWrite,
			},
		},
		HelpSynopsis:    "Configure RPID and allowed origins for passkey auth.",
		HelpDescription: "This configuration is required for WebAuthn verification and challenge issuance.",
	}
}

func (b *backend) handleConfigRead(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	cfg, err := loadConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	return &logical.Response{Data: map[string]any{
		"rp_id":                     cfg.RPID,
		"allowed_origins":           cfg.AllowedOrigins,
		"allowed_user_handle_regex": cfg.AllowedUserHandleRegex,
		"challenge_ttl":             cfg.ChallengeTTL,
	}}, nil
}

func (b *backend) handleConfigWrite(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	rpID := d.Get("rp_id").(string)
	origins := d.Get("allowed_origins").([]string)
	if rpID == "" || len(origins) == 0 {
		return logical.ErrorResponse("rp_id and allowed_origins are required"), nil
	}

	cfg := &configEntry{
		RPID:                   rpID,
		AllowedOrigins:         origins,
		AllowedUserHandleRegex: d.Get("allowed_user_handle_regex").(string),
		ChallengeTTL:           d.Get("challenge_ttl").(string),
	}
	if err := saveConfig(ctx, req.Storage, cfg); err != nil {
		return nil, err
	}
	return &logical.Response{Data: map[string]any{
		"configured": true,
		"rp_id":      cfg.RPID,
		"origins":    cfg.AllowedOrigins,
		"ttl":        cfg.challengeTTL().String(),
	}}, nil
}

func requireConfigured(cfg *configEntry) error {
	if cfg == nil || cfg.RPID == "" || len(cfg.AllowedOrigins) == 0 {
		return fmt.Errorf("passkey auth is not configured: set rp_id and allowed_origins at auth/<mount>/config")
	}
	return nil
}
