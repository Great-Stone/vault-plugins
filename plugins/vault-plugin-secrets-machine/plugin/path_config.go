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
		ExistenceCheck: func(ctx context.Context, req *logical.Request, _ *framework.FieldData) (bool, error) {
			e, err := req.Storage.Get(ctx, "config")
			return e != nil, err
		},
		Fields: map[string]*framework.FieldSchema{
			"default_ssh_port": {
				Type:        framework.TypeInt,
				Description: "Default SSH port when a static role omits port (default 22).",
			},
			"default_winrm_port": {
				Type:        framework.TypeInt,
				Description: "Default WinRM port when a static role omits port (default 5985).",
			},
			"winrm_use_https": {
				Type:        framework.TypeBool,
				Description: "When true, WinRM uses HTTPS (typically port 5986 unless overridden).",
			},
			"winrm_skip_tls_verify": {
				Type:        framework.TypeBool,
				Description: "When true, TLS certificate verification is skipped for WinRM (lab use only).",
			},
			"winrm_auth": {
				Type:        framework.TypeString,
				Description: "WinRM auth scheme: basic (default), negotiate, or ntlm.",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathConfigRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathConfigWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathConfigWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathConfigDelete},
		},
		HelpSynopsis:    "Optional defaults for SSH/WinRM connections",
		HelpDescription: "Optional engine-level defaults. Per-host connection details live on each static role.",
	}
}

func (b *backend) pathConfigRead(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	cfg, err := loadMachineConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	out := map[string]interface{}{
		"default_ssh_port":      cfg.DefaultSSHPort,
		"default_winrm_port":    cfg.DefaultWinRMPort,
		"winrm_use_https":       cfg.WinRMUseHTTPS,
		"winrm_skip_tls_verify": cfg.WinRMSkipTLSVerify,
		"winrm_auth":            cfg.WinRMAuth,
	}
	return &logical.Response{Data: out}, nil
}

func (b *backend) pathConfigWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	cfg := machineConfig{}
	if v, ok := data.GetOk("default_ssh_port"); ok {
		cfg.DefaultSSHPort = v.(int)
	}
	if v, ok := data.GetOk("default_winrm_port"); ok {
		cfg.DefaultWinRMPort = v.(int)
	}
	if v, ok := data.GetOk("winrm_use_https"); ok {
		cfg.WinRMUseHTTPS = v.(bool)
	}
	if v, ok := data.GetOk("winrm_skip_tls_verify"); ok {
		cfg.WinRMSkipTLSVerify = v.(bool)
	}
	if v, ok := data.GetOk("winrm_auth"); ok {
		cfg.WinRMAuth = strings.TrimSpace(v.(string))
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

// loadMachineConfig returns nil when no config document exists (not an error).
func loadMachineConfig(ctx context.Context, s logical.Storage) (*machineConfig, error) {
	e, err := s.Get(ctx, "config")
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var cfg machineConfig
	if err := e.DecodeJSON(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func normalizeOS(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
