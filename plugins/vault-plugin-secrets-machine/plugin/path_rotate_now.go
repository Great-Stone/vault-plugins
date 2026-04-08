package plugin

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/robfig/cron/v3"
)

// pathStaticRoleRotate provides an explicit, synchronous rotation trigger.
// This is useful for validation/debugging because scheduler failures only appear in logs.
func pathStaticRoleRotate(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "static-roles/" + framework.GenericNameRegex("name") + "/rotate",
		Fields: map[string]*framework.FieldSchema{
			"name": {Type: framework.TypeString, Description: "Static role name"},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathStaticRoleRotate},
		},
		HelpSynopsis:    "Rotate a static role immediately",
		HelpDescription: "Triggers an immediate password rotation for the given static role and persists the new password + timestamps.",
	}
}

func (b *backend) pathStaticRoleRotate(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	roleName := strings.TrimSpace(data.Get("name").(string))
	if roleName == "" {
		return logical.ErrorResponse("name is required"), nil
	}

	r, err := getStaticRole(ctx, req.Storage, roleName)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return logical.ErrorResponse("unknown static role %q", roleName), nil
	}

	defaults, _ := loadMachineConfig(ctx, req.Storage)

	var prof *machineConfigProfile
	if strings.TrimSpace(r.ConfigName) != "" {
		prof, err = getConfigProfile(ctx, req.Storage, strings.TrimSpace(r.ConfigName))
		if err != nil {
			return nil, err
		}
		if prof == nil {
			return logical.ErrorResponse("unknown config_name %q", r.ConfigName), nil
		}
	}
	rEff := r.roleEffective(prof)

	if strings.TrimSpace(rEff.RotationCron) == "" {
		return logical.ErrorResponse("rotation_cron is required"), nil
	}
	if strings.TrimSpace(rEff.Host) == "" || strings.TrimSpace(rEff.Username) == "" {
		return logical.ErrorResponse("role is incomplete (missing host/username)"), nil
	}
	if strings.TrimSpace(rEff.AdminUsername) == "" {
		return logical.ErrorResponse("role is incomplete (missing admin_username via config/%q)", r.ConfigName), nil
	}
	if strings.TrimSpace(rEff.AdminPassword) == "" && strings.TrimSpace(rEff.AdminPrivateKey) == "" {
		return logical.ErrorResponse("role is incomplete (missing admin_password/admin_private_key via config/%q)", r.ConfigName), nil
	}

	osLower := normalizeOS(rEff.OS)
	if osLower != "linux" && osLower != "windows" {
		return logical.ErrorResponse("unsupported os %q", rEff.OS), nil
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(strings.TrimSpace(rEff.RotationCron))
	if err != nil {
		return logical.ErrorResponse("invalid rotation_cron: %v", err), nil
	}

	now := time.Now().UTC()
	newPw, err := randomPassword(24)
	if err != nil {
		return nil, err
	}

	switch osLower {
	case "linux":
		port := rEff.effectivePort(defaults, osLower)
		if prof != nil {
			port = prof.effectivePort(defaults, osLower)
			if rEff.Port > 0 {
				port = rEff.Port
			}
		}
		if err := rotateLinuxPassword(rEff.Host, port, rEff.AdminUsername, rEff.AdminPassword, rEff.AdminPrivateKey, rEff.AdminPrivateKeyPassphrase, rEff.SudoPassword, rEff.Username, newPw); err != nil {
			return logical.ErrorResponse("rotation failed: %v", err), nil
		}
	case "windows":
		port := rEff.effectivePort(defaults, osLower)
		https := defaults != nil && defaults.WinRMUseHTTPS
		insecure := (defaults != nil && defaults.WinRMSkipTLSVerify) || rEff.WinRMInsecure
		if prof != nil {
			if prof.Port > 0 {
				port = prof.Port
			}
			https = prof.WinRMUseHTTPS
			insecure = prof.WinRMSkipTLSVerify || rEff.WinRMInsecure
		}
		auth := "basic"
		if strings.TrimSpace(rEff.WinRMAuth) != "" {
			auth = normalizeOS(rEff.WinRMAuth)
		} else if prof != nil {
			auth = prof.effectiveWinRMAuth(defaults)
		} else if defaults != nil {
			auth = defaults.effectiveWinRMAuth()
		}
		if err := rotateWindowsPassword(ctx, auth, rEff.Host, port, https, insecure, rEff.AdminUsername, rEff.AdminPassword, rEff.Username, newPw); err != nil {
			return logical.ErrorResponse("rotation failed: %v", err), nil
		}
	}

	r.Password = newPw
	r.LastRotatedAt = now.Format(time.RFC3339)
	r.NextRotationAt = sched.Next(now).Format(time.RFC3339)
	if err := putStaticRole(ctx, req.Storage, roleName, r); err != nil {
		return nil, err
	}

	return &logical.Response{
		Data: map[string]interface{}{
			"role":             roleName,
			"last_rotated_at":  r.LastRotatedAt,
			"next_rotation_at": r.NextRotationAt,
		},
	}, nil
}

