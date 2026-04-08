package plugin

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathStaticCreds(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "static-creds/" + framework.GenericNameRegex("role"),
		Fields: map[string]*framework.FieldSchema{
			"role": {
				Type:        framework.TypeString,
				Description: "Static role name",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathStaticCredsRead,
			},
		},
		HelpSynopsis:    "Read current machine credentials for a static role",
		HelpDescription: "Returns host, username, and the password last written by Vault (after successful rotation). Lease TTL is for bookkeeping only.",
	}
}

func (b *backend) pathStaticCredsRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	roleName := strings.TrimSpace(data.Get("role").(string))
	if roleName == "" {
		return logical.ErrorResponse("role is required"), nil
	}

	defaults, err := loadMachineConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}

	role, err := getStaticRole(ctx, req.Storage, roleName)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return logical.ErrorResponse("unknown role %q", roleName), nil
	}

	// Merge with config/<name> if present.
	var prof *machineConfigProfile
	if strings.TrimSpace(role.ConfigName) != "" {
		prof, _ = getConfigProfile(ctx, req.Storage, strings.TrimSpace(role.ConfigName))
	}
	roleEff := role.roleEffective(prof)

	osLower := normalizeOS(roleEff.OS)
	port := roleEff.effectivePort(defaults, osLower)
	if prof != nil {
		port = prof.effectivePort(defaults, osLower)
		if roleEff.Port > 0 {
			port = roleEff.Port
		}
	}

	leaseTTL := time.Duration(defaultRoleTTL) * time.Second
	now := time.Now().UTC()

	out := map[string]interface{}{
		"config_name":      role.ConfigName,
		"os":               roleEff.OS,
		"host":             roleEff.Host,
		"port":             port,
		"username":         roleEff.Username,
		"password":         roleEff.Password,
		"last_rotated_at":  roleEff.LastRotatedAt,
		"next_rotation_at": roleEff.NextRotationAt,
		"rotation_ttl":     rotationRemainingSeconds(roleEff.NextRotationAt, now),
	}

	resp := b.Secret(machineStaticSecretType).Response(out, map[string]interface{}{
		"mode":     "static",
		"role":     roleName,
		"username": roleEff.Username,
		"host":     roleEff.Host,
	})
	resp.Secret.TTL = leaseTTL
	resp.Secret.MaxTTL = time.Duration(defaultRoleMaxTTL) * time.Second
	return resp, nil
}

func rotationRemainingSeconds(nextRotationAt string, now time.Time) int {
	tStr := strings.TrimSpace(nextRotationAt)
	if tStr == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, tStr)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, tStr)
	}
	if err != nil {
		return 0
	}
	d := t.Sub(now)
	if d <= 0 {
		return 0
	}
	return int(d / time.Second)
}
