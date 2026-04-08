package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/robfig/cron/v3"
)

func pathStaticRoles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "static-roles/" + framework.GenericNameRegex("name"),
		ExistenceCheck: func(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
			name := data.Get("name").(string)
			if name == "" {
				return false, nil
			}
			e, err := req.Storage.Get(ctx, staticRoleStoragePrefix+name)
			return e != nil, err
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {Type: framework.TypeString, Description: "Static role name"},
			"config_name": {
				Type:        framework.TypeString,
				Description: "Optional reference to config/<name>. When set, host/transport/admin settings are inherited from that config profile.",
			},
			"os": {
				Type:        framework.TypeString,
				Description: "Target OS: linux (SSH) or windows (WinRM). Required when creating a role.",
			},
			"host": {
				Type:        framework.TypeString,
				Description: "Host address (DNS name or IP) reachable from the Vault plugin process. Required when creating a role.",
			},
			"port": {
				Type:        framework.TypeInt,
				Description: "Optional SSH or WinRM port; 0 uses defaults from config or built-ins (22 / 5985).",
			},
			"username": {
				Type:        framework.TypeString,
				Description: "Local account whose password is stored and rotated. Required when creating a role.",
			},
			"password": {
				Type:        framework.TypeString,
				Description: "Current password for the managed account (optional until first successful rotation).",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"admin_username": {
				Type:        framework.TypeString,
				Description: "Privileged account for SSH or WinRM (must be able to change the target user's password). Required when creating a role.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"admin_password": {
				Type:        framework.TypeString,
				Description: "Password for admin_username. Required when creating a role; omit on update to keep the stored value.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"admin_private_key": {
				Type:        framework.TypeString,
				Description: "Optional PEM private key for SSH authentication (Linux). If set, it is preferred over admin_password for SSH auth.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"admin_private_key_passphrase": {
				Type:        framework.TypeString,
				Description: "Optional passphrase for admin_private_key.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"sudo_password": {
				Type:        framework.TypeString,
				Description: "Optional sudo password for Linux when passwordless sudo is not available.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"winrm_insecure": {
				Type:        framework.TypeBool,
				Description: "When true, skip TLS verification for this role's WinRM connection.",
			},
			"winrm_auth": {
				Type:        framework.TypeString,
				Description: "Optional WinRM auth override: basic or ntlm.",
			},
			"rotation_cron": {
				Type:        framework.TypeString,
				Description: "Cron schedule in UTC for password rotation (e.g. '0 */6 * * *'). Required when creating a role.",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathStaticRoleRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathStaticRoleWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathStaticRoleWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathStaticRoleDelete},
		},
		HelpSynopsis:    "Manage static machine credential roles",
		HelpDescription: "Static roles map one Vault name to one host and local username, similar to database static roles.",
	}
}

func pathStaticRoleList(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "static-roles/?",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{Callback: b.pathStaticRoleList},
		},
		HelpSynopsis: "List static role names",
	}
}

func (b *backend) pathStaticRoleWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if name == "" {
		return logical.ErrorResponse("name is required"), nil
	}

	existing, err := getStaticRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}

	var role staticRole
	if existing != nil {
		role = *existing
	}

	if v, ok := data.GetOk("os"); ok {
		role.OS = normalizeOS(v.(string))
	} else if existing == nil {
		// may be inherited from config_name
	}

	if v, ok := data.GetOk("config_name"); ok {
		role.ConfigName = strings.TrimSpace(v.(string))
	}

	if v, ok := data.GetOk("host"); ok {
		role.Host = strings.TrimSpace(v.(string))
	} else if existing == nil {
		// may be inherited from config_name
	}

	if v, ok := data.GetOk("username"); ok {
		role.Username = strings.TrimSpace(v.(string))
	} else if existing == nil {
		return logical.ErrorResponse("username is required"), nil
	}

	if v, ok := data.GetOk("admin_username"); ok {
		role.AdminUsername = strings.TrimSpace(v.(string))
	} else if existing == nil {
		// may be inherited from config_name
	}

	if v, ok := data.GetOk("admin_password"); ok {
		role.AdminPassword = v.(string)
	}
	if v, ok := data.GetOk("admin_private_key"); ok {
		role.AdminPrivateKey = v.(string)
	}
	if v, ok := data.GetOk("admin_private_key_passphrase"); ok {
		role.AdminPrivateKeyPassphrase = v.(string)
	}

	if v, ok := data.GetOk("rotation_cron"); ok {
		role.RotationCron = strings.TrimSpace(v.(string))
	} else if existing == nil {
		return logical.ErrorResponse("rotation_cron is required"), nil
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(role.RotationCron); err != nil {
		return logical.ErrorResponse("invalid rotation_cron: %v", err), nil
	}

	osLower := normalizeOS(role.OS)
	if osLower != "linux" && osLower != "windows" {
		return logical.ErrorResponse("os must be linux or windows"), nil
	}
	role.OS = osLower

	if v, ok := data.GetOk("port"); ok {
		role.Port = v.(int)
	}

	if v, ok := data.GetOk("password"); ok {
		role.Password = v.(string)
	}

	if v, ok := data.GetOk("sudo_password"); ok {
		role.SudoPassword = v.(string)
	}

	if v, ok := data.GetOk("winrm_insecure"); ok {
		role.WinRMInsecure = v.(bool)
	}
	if v, ok := data.GetOk("winrm_auth"); ok {
		role.WinRMAuth = strings.TrimSpace(v.(string))
	}

	if strings.TrimSpace(role.Host) == "" {
		if strings.TrimSpace(role.ConfigName) == "" {
			return logical.ErrorResponse("host is required (or set config_name)"), nil
		}
	}
	if strings.TrimSpace(role.Username) == "" {
		return logical.ErrorResponse("username is required"), nil
	}
	if strings.TrimSpace(role.AdminUsername) == "" {
		if strings.TrimSpace(role.ConfigName) == "" {
			return logical.ErrorResponse("admin_username is required (or set config_name)"), nil
		}
	}
	if strings.TrimSpace(role.AdminPassword) == "" && strings.TrimSpace(role.AdminPrivateKey) == "" {
		if strings.TrimSpace(role.ConfigName) == "" {
			return logical.ErrorResponse("admin_password or admin_private_key is required (or set config_name)"), nil
		}
	}

	if existing == nil {
		role.LastRotatedAt = ""
		role.NextRotationAt = ""
	}

	entry, err := logical.StorageEntryJSON(staticRoleStoragePrefix+name, role)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, entry); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathStaticRoleRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	role, err := getStaticRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, nil
	}
	out := map[string]interface{}{
		"config_name":       role.ConfigName,
		"os":               role.OS,
		"host":             role.Host,
		"port":             role.Port,
		"username":         role.Username,
		"rotation_cron":    role.RotationCron,
		"last_rotated_at":  role.LastRotatedAt,
		"next_rotation_at": role.NextRotationAt,
		"winrm_insecure":   role.WinRMInsecure,
		"winrm_auth":       role.WinRMAuth,
	}
	if role.AdminUsername != "" {
		out["admin_username"] = role.AdminUsername
	}
	return &logical.Response{Data: out}, nil
}

func (b *backend) pathStaticRoleDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if err := req.Storage.Delete(ctx, staticRoleStoragePrefix+name); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathStaticRoleList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	list, err := req.Storage.List(ctx, staticRoleStoragePrefix)
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(list), nil
}

func getStaticRole(ctx context.Context, s logical.Storage, name string) (*staticRole, error) {
	e, err := s.Get(ctx, staticRoleStoragePrefix+name)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var r staticRole
	if err := e.DecodeJSON(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func putStaticRole(ctx context.Context, s logical.Storage, name string, r *staticRole) error {
	if r == nil {
		return fmt.Errorf("static role is nil")
	}
	entry, err := logical.StorageEntryJSON(staticRoleStoragePrefix+name, r)
	if err != nil {
		return err
	}
	return s.Put(ctx, entry)
}
