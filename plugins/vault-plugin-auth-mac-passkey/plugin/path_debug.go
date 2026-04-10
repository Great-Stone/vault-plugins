package plugin

import (
	"context"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathDebugUser(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "debug/user/" + framework.GenericNameRegex("user_handle"),
		Fields: map[string]*framework.FieldSchema{
			"user_handle": {Type: framework.TypeString, Required: true},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{Callback: b.handleDebugUserRead},
		},
		HelpSynopsis:    "Debug helper to show registered credential IDs for a user.",
		HelpDescription: "Returns the stored credential IDs for (rp_id, user_handle). Intended for local testing.",
	}
}

func (b *backend) handleDebugUserRead(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	cfg, err := loadConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if err := requireConfigured(cfg); err != nil {
		return logical.ErrorResponse(err.Error()), nil
	}

	userHandle := d.Get("user_handle").(string)
	idx, err := loadUserIndex(ctx, req.Storage, cfg.RPID, userHandle)
	if err != nil {
		return nil, err
	}
	return &logical.Response{Data: map[string]any{
		"rp_id":          cfg.RPID,
		"user_handle":    userHandle,
		"credential_ids": idx.CredentialIDs,
		"count":          len(idx.CredentialIDs),
	}}, nil
}
