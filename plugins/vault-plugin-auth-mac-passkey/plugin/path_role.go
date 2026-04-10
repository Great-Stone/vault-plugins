package plugin

import (
	"context"
	"regexp"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathRole(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "role/" + framework.GenericNameRegex("name"),
		Fields: map[string]*framework.FieldSchema{
			"name": {Type: framework.TypeString, Required: true},
			"policies": {
				Type:        framework.TypeCommaStringSlice,
				Description: "Comma-separated list of policies to attach to tokens.",
				Required:    false,
			},
			"ttl": {
				Type:        framework.TypeString,
				Description: "Token TTL (e.g. 1h). Optional.",
				Required:    false,
			},
			"max_ttl": {
				Type:        framework.TypeString,
				Description: "Token max TTL (e.g. 24h). Optional.",
				Required:    false,
			},
			"allowed_user_handle_regex": {
				Type:        framework.TypeString,
				Description: "Regex that userHandle must match to use this role.",
				Required:    false,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.handleRoleRead},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.handleRoleWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.handleRoleDelete},
		},
		HelpSynopsis:    "Manage roles for passkey auth.",
		HelpDescription: "Roles control token policies/TTLs and optional userHandle validation.",
	}
}

func pathRoleList(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "role/?$",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{Callback: b.handleRoleList},
		},
	}
}

func (b *backend) handleRoleRead(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	name := d.Get("name").(string)
	r, err := loadRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return &logical.Response{Data: map[string]any{
		"policies":                  r.Policies,
		"ttl":                       r.TTL,
		"max_ttl":                   r.MaxTTL,
		"allowed_user_handle_regex": r.AllowedUserHandleRegex,
	}}, nil
}

func (b *backend) handleRoleWrite(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	name := d.Get("name").(string)
	r := &roleEntry{
		Policies:               d.Get("policies").([]string),
		TTL:                    d.Get("ttl").(string),
		MaxTTL:                 d.Get("max_ttl").(string),
		AllowedUserHandleRegex: d.Get("allowed_user_handle_regex").(string),
	}
	if r.AllowedUserHandleRegex != "" {
		if _, err := regexp.Compile(r.AllowedUserHandleRegex); err != nil {
			return logical.ErrorResponse("invalid allowed_user_handle_regex: %v", err), nil
		}
	}
	if err := saveRole(ctx, req.Storage, name, r); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) handleRoleDelete(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	name := d.Get("name").(string)
	if err := deleteRole(ctx, req.Storage, name); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) handleRoleList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	keys, err := req.Storage.List(ctx, storageRolePrefix)
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(keys), nil
}

func (r *roleEntry) parsedTTL() time.Duration {
	if r == nil || r.TTL == "" {
		return 0
	}
	d, err := time.ParseDuration(r.TTL)
	if err != nil {
		return 0
	}
	return d
}

func (r *roleEntry) parsedMaxTTL() time.Duration {
	if r == nil || r.MaxTTL == "" {
		return 0
	}
	d, err := time.ParseDuration(r.MaxTTL)
	if err != nil {
		return 0
	}
	return d
}
