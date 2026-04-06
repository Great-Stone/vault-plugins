package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathCreds(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "creds/" + framework.GenericNameRegex("role"),
		Fields: map[string]*framework.FieldSchema{
			"role": {
				Type:        framework.TypeString,
				Description: "Role name",
			},
			"ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Optional TTL override (must not exceed role max_ttl).",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathCredsRead,
			},
		},
		HelpSynopsis:    "Issue a GitHub App installation access token",
		HelpDescription: "Reads a short-lived installation token for the given role.",
	}
}

func (b *backend) pathCredsRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	roleName := data.Get("role").(string)
	if roleName == "" {
		return logical.ErrorResponse("role is required"), nil
	}

	cfg, err := b.loadConfig(ctx, req.Storage)
	if err != nil {
		return logical.ErrorResponse("%s", err.Error()), nil
	}

	role, err := b.getRole(ctx, req.Storage, roleName)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return logical.ErrorResponse("unknown role %q", roleName), nil
	}

	ttlSec := role.TTL
	if raw, ok := data.GetOk("ttl"); ok {
		ttlSec = raw.(int)
	}
	if ttlSec <= 0 {
		ttlSec = defaultRoleTTL
	}
	if ttlSec > role.MaxTTL {
		return logical.ErrorResponse("ttl exceeds role max_ttl (%d)", role.MaxTTL), nil
	}

	itReq := installationTokenRequest{
		Repositories:  append([]string(nil), role.Repositories...),
		RepositoryIDs: append([]int64(nil), role.RepositoryIDs...),
		Permissions:   role.Permissions,
	}

	client := b.getHTTPClient()
	tok, err := cfg.createInstallationToken(ctx, client, role.InstallationID, itReq)
	if err != nil {
		return nil, err
	}

	expiresAt, err := time.Parse(time.RFC3339, tok.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("parse expires_at: %w", err)
	}

	leaseTTL := time.Duration(ttlSec) * time.Second
	githubTTL := time.Until(expiresAt)
	if githubTTL > 0 && githubTTL < leaseTTL {
		leaseTTL = githubTTL
	}
	if leaseTTL <= 0 {
		return logical.ErrorResponse("github returned token already expired at %s", tok.ExpiresAt), nil
	}

	resp := b.Secret(installationTokenSecretType).Response(
		map[string]interface{}{
			"token":           tok.Token,
			"expires_at":      tok.ExpiresAt,
			"installation_id": role.InstallationID,
			"permissions":     tok.Permissions,
		},
		map[string]interface{}{
			"installation_id": role.InstallationID,
			"role":            roleName,
			// Stored for lease Revoke → GitHub DELETE /installation/token (not returned in API output).
			"token": tok.Token,
		},
	)
	resp.Secret.TTL = leaseTTL
	resp.Secret.MaxTTL = time.Duration(role.MaxTTL) * time.Second

	return resp, nil
}
