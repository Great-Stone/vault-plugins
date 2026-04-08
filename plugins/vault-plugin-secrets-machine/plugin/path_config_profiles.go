package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/robfig/cron/v3"
)

func pathConfigProfiles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "config/" + framework.GenericNameRegex("name"),
		ExistenceCheck: func(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
			name := strings.TrimSpace(data.Get("name").(string))
			if name == "" {
				return false, nil
			}
			e, err := req.Storage.Get(ctx, configStoragePrefix+name)
			return e != nil, err
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {Type: framework.TypeString, Description: "Config profile name"},

			"os": {
				Type:        framework.TypeString,
				Description: "Target OS/transport selector: linux (SSH) or windows (WinRM).",
				Required:    true,
			},
			"host": {
				Type:        framework.TypeString,
				Description: "Host address (DNS name or IP) reachable from the Vault plugin process.",
				Required:    true,
			},
			"port": {
				Type:        framework.TypeInt,
				Description: "Optional SSH or WinRM port; 0 uses defaults from config/ (legacy defaults) or built-ins.",
			},

			"admin_username": {
				Type:        framework.TypeString,
				Description: "Privileged account used to change passwords on the target host.",
				Required:    true,
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"admin_password": {
				Type:        framework.TypeString,
				Description: "Password for admin_username (SSH/WinRM). For Linux, admin_private_key can be used instead.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"admin_private_key": {
				Type:        framework.TypeString,
				Description: "Optional PEM private key for SSH auth (Linux). Preferred over admin_password when set.",
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

			// Windows/WinRM knobs (also usable on linux but ignored)
			"winrm_use_https": {
				Type:        framework.TypeBool,
				Description: "Use HTTPS for WinRM.",
			},
			"winrm_skip_tls_verify": {
				Type:        framework.TypeBool,
				Description: "Skip TLS verification for WinRM (lab only).",
			},
			"winrm_auth": {
				Type:        framework.TypeString,
				Description: "WinRM auth scheme: basic (default) or ntlm.",
			},

			// Root/admin credential rotation (optional)
			"root_rotation_cron": {
				Type:        framework.TypeString,
				Description: "Optional cron (UTC) to rotate the privileged credential itself (admin_username password).",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathConfigProfileRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathConfigProfileWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathConfigProfileWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathConfigProfileDelete},
		},
		HelpSynopsis:    "Manage machine connection profiles",
		HelpDescription: "Database-like config profiles: one profile stores host + transport + privileged credential used by static roles.",
	}
}

func pathConfigProfileList(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "config/?",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{Callback: b.pathConfigProfileList},
		},
		HelpSynopsis: "List config profile names",
	}
}

func (b *backend) pathConfigProfileWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if name == "" {
		return logical.ErrorResponse("name is required"), nil
	}

	osLower := normalizeOS(data.Get("os").(string))
	if osLower != "linux" && osLower != "windows" {
		return logical.ErrorResponse("os must be linux or windows"), nil
	}
	host := strings.TrimSpace(data.Get("host").(string))
	if host == "" {
		return logical.ErrorResponse("host is required"), nil
	}
	adminUser := strings.TrimSpace(data.Get("admin_username").(string))
	if adminUser == "" {
		return logical.ErrorResponse("admin_username is required"), nil
	}

	existing, err := getConfigProfile(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	var p machineConfigProfile
	if existing != nil {
		p = *existing
	}

	p.OS = osLower
	p.Host = host
	if v, ok := data.GetOk("port"); ok {
		p.Port = v.(int)
	}
	p.AdminUsername = adminUser
	if v, ok := data.GetOk("admin_password"); ok {
		p.AdminPassword = v.(string)
	}
	if v, ok := data.GetOk("admin_private_key"); ok {
		p.AdminPrivateKey = v.(string)
	}
	if v, ok := data.GetOk("admin_private_key_passphrase"); ok {
		p.AdminPrivateKeyPassphrase = v.(string)
	}
	if v, ok := data.GetOk("sudo_password"); ok {
		p.SudoPassword = v.(string)
	}
	if v, ok := data.GetOk("winrm_use_https"); ok {
		p.WinRMUseHTTPS = v.(bool)
	}
	if v, ok := data.GetOk("winrm_skip_tls_verify"); ok {
		p.WinRMSkipTLSVerify = v.(bool)
	}
	if v, ok := data.GetOk("winrm_auth"); ok {
		p.WinRMAuth = strings.TrimSpace(v.(string))
	}
	if v, ok := data.GetOk("root_rotation_cron"); ok {
		p.RootRotationCron = strings.TrimSpace(v.(string))
	}

	if strings.TrimSpace(p.AdminPassword) == "" && strings.TrimSpace(p.AdminPrivateKey) == "" {
		return logical.ErrorResponse("admin_password or admin_private_key is required"), nil
	}

	if strings.TrimSpace(p.RootRotationCron) != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(p.RootRotationCron); err != nil {
			return logical.ErrorResponse("invalid root_rotation_cron: %v", err), nil
		}
		// Root rotation only supported when password auth is available (we rotate the password).
		if strings.TrimSpace(p.AdminPassword) == "" {
			return logical.ErrorResponse("root_rotation_cron requires admin_password (cannot rotate private key)"), nil
		}
	}

	if err := putConfigProfile(ctx, req.Storage, name, &p); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathConfigProfileRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	p, err := getConfigProfile(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	out := map[string]interface{}{
		"os":                     p.OS,
		"host":                   p.Host,
		"port":                   p.Port,
		"admin_username":         p.AdminUsername,
		"winrm_use_https":        p.WinRMUseHTTPS,
		"winrm_skip_tls_verify":  p.WinRMSkipTLSVerify,
		"winrm_auth":             p.WinRMAuth,
		"root_rotation_cron":     p.RootRotationCron,
		"root_last_rotated_at":   p.RootLastRotatedAt,
		"root_next_rotation_at":  p.RootNextRotationAt,
		"root_rotation_ttl":      rotationRemainingSeconds(p.RootNextRotationAt, time.Now().UTC()),
	}
	// Do not return passwords/keys.
	return &logical.Response{Data: out}, nil
}

func (b *backend) pathConfigProfileDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if name == "" {
		return logical.ErrorResponse("name is required"), nil
	}
	if err := req.Storage.Delete(ctx, configStoragePrefix+name); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathConfigProfileList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	list, err := req.Storage.List(ctx, configStoragePrefix)
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(list), nil
}

func getConfigProfile(ctx context.Context, s logical.Storage, name string) (*machineConfigProfile, error) {
	e, err := s.Get(ctx, configStoragePrefix+name)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var p machineConfigProfile
	if err := e.DecodeJSON(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func putConfigProfile(ctx context.Context, s logical.Storage, name string, p *machineConfigProfile) error {
	if p == nil {
		return fmt.Errorf("config profile is nil")
	}
	entry, err := logical.StorageEntryJSON(configStoragePrefix+name, p)
	if err != nil {
		return err
	}
	return s.Put(ctx, entry)
}

