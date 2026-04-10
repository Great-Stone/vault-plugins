package plugin

import (
	"context"
	"strings"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathConfig(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "config",
		Fields: map[string]*framework.FieldSchema{
			"rp_id": {
				Type:        framework.TypeString,
				Description: "WebAuthn Relying Party ID (e.g. vault.example.com). Omitted or empty defaults to localhost for the bundled macOS helper.",
				Required:    false,
			},
			"allowed_origins": {
				Type:        framework.TypeCommaStringSlice,
				Description: "Allowed browser origins for WebAuthn clientDataJSON verification. Omitted or empty defaults to http://localhost:8765 and http://127.0.0.1:8765 when rp_id is localhost or 127.0.0.1; otherwise required.",
				Required:    false,
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
	eff, err := effectivePasskeyConfig(cfg)
	if err != nil {
		return logical.ErrorResponse("%s", err.Error()), nil
	}
	return &logical.Response{Data: map[string]any{
		"rp_id":                     eff.RPID,
		"allowed_origins":           eff.AllowedOrigins,
		"allowed_user_handle_regex": eff.AllowedUserHandleRegex,
		"challenge_ttl":             eff.ChallengeTTL,
		"rp_id_uses_default":        strings.TrimSpace(cfg.RPID) == "",
		"allowed_origins_use_localhost_defaults": len(cleanOriginList(cfg.AllowedOrigins)) == 0 &&
			isLocalhostRPID(eff.RPID),
	}}, nil
}

func (b *backend) handleConfigWrite(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	existing, err := loadConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}

	var rpID string
	if v, ok := d.GetOk("rp_id"); ok {
		rpID = strings.TrimSpace(v.(string))
	} else {
		rpID = strings.TrimSpace(existing.RPID)
	}

	var origins []string
	if v, ok := d.GetOk("allowed_origins"); ok {
		origins = cleanOriginList(v.([]string))
	} else {
		origins = append([]string(nil), existing.AllowedOrigins...)
	}

	var regex string
	if v, ok := d.GetOk("allowed_user_handle_regex"); ok {
		regex = v.(string)
	} else {
		regex = existing.AllowedUserHandleRegex
	}

	var ttl string
	if v, ok := d.GetOk("challenge_ttl"); ok {
		ttl = strings.TrimSpace(v.(string))
	} else {
		ttl = existing.ChallengeTTL
	}

	cfg := &configEntry{
		RPID:                   rpID,
		AllowedOrigins:         origins,
		AllowedUserHandleRegex: regex,
		ChallengeTTL:           ttl,
	}
	eff, err := effectivePasskeyConfig(cfg)
	if err != nil {
		return logical.ErrorResponse("%s", err.Error()), nil
	}
	if err := saveConfig(ctx, req.Storage, eff); err != nil {
		return nil, err
	}
	return &logical.Response{Data: map[string]any{
		"configured": true,
		"rp_id":      eff.RPID,
		"origins":    eff.AllowedOrigins,
		"ttl":        eff.challengeTTL().String(),
	}}, nil
}
