package plugin

import (
	"context"
	"fmt"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const (
	defaultRoleTTL    = 3600
	defaultRoleMaxTTL = 86400
)

func pathRoles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/" + framework.GenericNameRegex("name"),
		ExistenceCheck: func(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
			name := data.Get("name").(string)
			if name == "" {
				return false, nil
			}
			e, err := req.Storage.Get(ctx, "role/"+name)
			return e != nil, err
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {
				Type:        framework.TypeString,
				Description: "Role name",
			},
			"installation_id": {
				Type:        framework.TypeInt64,
				Description: "GitHub App installation ID for this role.",
				Required:    true,
			},
			"repositories": {
				Type:        framework.TypeCommaStringSlice,
				Description: "Optional repository names (owner/repo) to narrow the installation token.",
			},
			"repository_ids": {
				Type:        framework.TypeCommaStringSlice,
				Description: "Optional numeric repository IDs (comma-separated) to narrow the token.",
			},
			"permissions": {
				Type:        framework.TypeKVPairs,
				Description: "Optional permission map for the token (e.g. contents=read).",
			},
			"ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Default lease/TTL for issued tokens.",
				Default:     defaultRoleTTL,
			},
			"max_ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Maximum TTL for a credential read (including overrides).",
				Default:     defaultRoleMaxTTL,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathRoleRead,
			},
			logical.CreateOperation: &framework.PathOperation{
				Callback: b.pathRoleWrite,
			},
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.pathRoleWrite,
			},
			logical.DeleteOperation: &framework.PathOperation{
				Callback: b.pathRoleDelete,
			},
		},
		HelpSynopsis:    "Manage GitHub installation token roles",
		HelpDescription: "Roles bind installation IDs and optional repository/permission scopes.",
	}
}

func pathRoleList(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/?",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{
				Callback: b.pathRoleList,
			},
		},
		HelpSynopsis: "List role names",
	}
}

func (b *backend) pathRoleWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	if name == "" {
		return logical.ErrorResponse("name is required"), nil
	}
	inst := data.Get("installation_id").(int64)
	if inst <= 0 {
		return logical.ErrorResponse("installation_id must be positive"), nil
	}

	role := roleEntry{
		InstallationID: inst,
		TTL:            data.Get("ttl").(int),
		MaxTTL:         data.Get("max_ttl").(int),
	}
	if repos, ok := data.GetOk("repositories"); ok {
		role.Repositories = repos.([]string)
	}
	if idsStr, ok := data.GetOk("repository_ids"); ok {
		for _, s := range idsStr.([]string) {
			var id int64
			_, err := fmt.Sscanf(s, "%d", &id)
			if err != nil || id <= 0 {
				return logical.ErrorResponse("repository_ids must be comma-separated positive integers"), nil
			}
			role.RepositoryIDs = append(role.RepositoryIDs, id)
		}
	}
	if perms, ok := data.GetOk("permissions"); ok {
		kv := perms.(map[string]string)
		role.Permissions = kv
	}

	if role.TTL <= 0 {
		role.TTL = defaultRoleTTL
	}
	if role.MaxTTL <= 0 {
		role.MaxTTL = defaultRoleMaxTTL
	}
	if role.MaxTTL < role.TTL {
		return logical.ErrorResponse("max_ttl must be >= ttl"), nil
	}

	entry, err := logical.StorageEntryJSON("role/"+name, role)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, entry); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathRoleRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	role, err := b.getRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, nil
	}
	out := map[string]interface{}{
		"installation_id": role.InstallationID,
		"ttl":             role.TTL,
		"max_ttl":         role.MaxTTL,
	}
	if len(role.Repositories) > 0 {
		out["repositories"] = role.Repositories
	}
	if len(role.RepositoryIDs) > 0 {
		out["repository_ids"] = role.RepositoryIDs
	}
	if len(role.Permissions) > 0 {
		out["permissions"] = role.Permissions
	}
	return &logical.Response{Data: out}, nil
}

func (b *backend) pathRoleDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	if err := req.Storage.Delete(ctx, "role/"+name); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathRoleList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	list, err := req.Storage.List(ctx, "role/")
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(list), nil
}

func (b *backend) getRole(ctx context.Context, s logical.Storage, name string) (*roleEntry, error) {
	entry, err := s.Get(ctx, "role/"+name)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	var role roleEntry
	if err := entry.DecodeJSON(&role); err != nil {
		return nil, err
	}
	return &role, nil
}
