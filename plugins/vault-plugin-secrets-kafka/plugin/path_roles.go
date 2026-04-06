package plugin

import (
	"context"
	"strings"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathDynamicRoles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/" + framework.GenericNameRegex("name"),
		ExistenceCheck: func(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
			name := data.Get("name").(string)
			if name == "" {
				return false, nil
			}
			e, err := req.Storage.Get(ctx, dynamicRoleStoragePrefix+name)
			return e != nil, err
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {Type: framework.TypeString, Description: "Role name"},
			"scram_mechanism": {
				Type:        framework.TypeString,
				Description: "SCRAM mechanism for dynamic issuance: SCRAM-SHA-256 or SCRAM-SHA-512.",
				Default:     "SCRAM-SHA-256",
			},
			"topic": {
				Type:        framework.TypeString,
				Description: "Optional topic name to grant ACLs for (v1 minimal).",
			},
			"group_prefix": {
				Type:        framework.TypeString,
				Description: "Optional consumer group prefix to grant ACLs for (v1 minimal).",
			},

			"ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Default lease/TTL for issued credentials.",
				Default:     defaultRoleTTL,
			},
			"max_ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Maximum TTL for a credential read (including overrides).",
				Default:     defaultRoleMaxTTL,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathDynamicRoleRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathDynamicRoleWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathDynamicRoleWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathDynamicRoleDelete},
		},
		HelpSynopsis:    "Manage dynamic Kafka credential roles",
		HelpDescription: "Dynamic roles control SCRAM issuance and optional minimal ACL creation (v1).",
	}
}

func pathDynamicRoleList(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/?",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{Callback: b.pathDynamicRoleList},
		},
		HelpSynopsis: "List role names",
	}
}

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
			"name": {Type: framework.TypeString, Description: "Role name"},
			"auth_type": {
				Type:        framework.TypeString,
				Description: "Auth type for static bundle: scram, plain, or mtls.",
				Default:     string(authScram),
			},
			"scram_mechanism": {
				Type:        framework.TypeString,
				Description: "SCRAM mechanism for auth_type=scram: SCRAM-SHA-256 or SCRAM-SHA-512.",
				Default:     "SCRAM-SHA-256",
			},
			"static_username": {
				Type:        framework.TypeString,
				Description: "Static username to return.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"static_password": {
				Type:        framework.TypeString,
				Description: "Static password to return. For auth_type=scram with rotation enabled, this is updated by the scheduler.",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"static_props": {
				Type:        framework.TypeKVPairs,
				Description: "Optional extra client properties to return (static bundle).",
			},
			"rotation_enabled": {
				Type:        framework.TypeBool,
				Description: "Enable password rotation for auth_type=scram (default: true when rotation_cron is set).",
			},
			"rotation_cron": {
				Type:        framework.TypeString,
				Description: "Cron expression for SCRAM password rotation (e.g. '*/5 * * * *').",
			},
			"ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Default lease/TTL for returned credentials.",
				Default:     defaultRoleTTL,
			},
			"max_ttl": {
				Type:        framework.TypeDurationSecond,
				Description: "Maximum TTL for a credential read (including overrides).",
				Default:     defaultRoleMaxTTL,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathStaticRoleRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathStaticRoleWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathStaticRoleWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathStaticRoleDelete},
		},
		HelpSynopsis:    "Manage static Kafka credential roles",
		HelpDescription: "Static roles return stored credential bundles. For auth_type=scram, optional password rotation is supported (v1).",
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

func (b *backend) pathDynamicRoleWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if name == "" {
		return logical.ErrorResponse("name is required"), nil
	}

	role := dynamicRole{
		TTL:    data.Get("ttl").(int),
		MaxTTL: data.Get("max_ttl").(int),
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

	role.ScramMechanism = strings.TrimSpace(data.Get("scram_mechanism").(string))
	if role.ScramMechanism == "" {
		role.ScramMechanism = "SCRAM-SHA-256"
	}
	if role.ScramMechanism != "SCRAM-SHA-256" && role.ScramMechanism != "SCRAM-SHA-512" {
		return logical.ErrorResponse("scram_mechanism must be SCRAM-SHA-256 or SCRAM-SHA-512"), nil
	}
	if v, ok := data.GetOk("topic"); ok {
		role.Topic = strings.TrimSpace(v.(string))
	}
	if v, ok := data.GetOk("group_prefix"); ok {
		role.GroupPrefix = strings.TrimSpace(v.(string))
	}

	entry, err := logical.StorageEntryJSON(dynamicRoleStoragePrefix+name, role)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, entry); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathDynamicRoleRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	role, err := b.getDynamicRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, nil
	}
	out := map[string]interface{}{
		"ttl":     role.TTL,
		"max_ttl": role.MaxTTL,
	}
	out["scram_mechanism"] = role.ScramMechanism
	if role.Topic != "" {
		out["topic"] = role.Topic
	}
	if role.GroupPrefix != "" {
		out["group_prefix"] = role.GroupPrefix
	}
	return &logical.Response{Data: out}, nil
}

func (b *backend) pathDynamicRoleDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if err := req.Storage.Delete(ctx, dynamicRoleStoragePrefix+name); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathDynamicRoleList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	list, err := req.Storage.List(ctx, dynamicRoleStoragePrefix)
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(list), nil
}

func (b *backend) getDynamicRole(ctx context.Context, s logical.Storage, name string) (*dynamicRole, error) {
	e, err := s.Get(ctx, dynamicRoleStoragePrefix+name)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var role dynamicRole
	if err := e.DecodeJSON(&role); err != nil {
		return nil, err
	}
	return &role, nil
}

func (b *backend) pathStaticRoleWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if name == "" {
		return logical.ErrorResponse("name is required"), nil
	}

	at := authType(strings.TrimSpace(data.Get("auth_type").(string)))
	if at == "" {
		at = authScram
	}
	if at != authScram && at != authPlain && at != authMtls {
		return logical.ErrorResponse("auth_type must be scram, plain, or mtls"), nil
	}

	role := staticRole{
		AuthType: at,
		TTL:      data.Get("ttl").(int),
		MaxTTL:   data.Get("max_ttl").(int),
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

	if v, ok := data.GetOk("static_username"); ok {
		role.StaticUsername = strings.TrimSpace(v.(string))
	}
	if v, ok := data.GetOk("static_password"); ok {
		role.StaticPassword = v.(string)
	}
	if v, ok := data.GetOk("static_props"); ok {
		role.StaticProps = v.(map[string]string)
	}

	if at == authScram {
		role.ScramMechanism = strings.TrimSpace(data.Get("scram_mechanism").(string))
		if role.ScramMechanism == "" {
			role.ScramMechanism = "SCRAM-SHA-256"
		}
		if role.ScramMechanism != "SCRAM-SHA-256" && role.ScramMechanism != "SCRAM-SHA-512" {
			return logical.ErrorResponse("scram_mechanism must be SCRAM-SHA-256 or SCRAM-SHA-512"), nil
		}

		if v, ok := data.GetOk("rotation_cron"); ok {
			role.RotationCron = strings.TrimSpace(v.(string))
		}
		if v, ok := data.GetOk("rotation_enabled"); ok {
			role.RotationEnabled = v.(bool)
		} else if role.RotationCron != "" {
			// Default enabled when a schedule is provided.
			role.RotationEnabled = true
		}
	} else {
		// rotation fields are only for scram.
		role.RotationCron = ""
		role.RotationEnabled = false
		role.ScramMechanism = ""
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
		"auth_type":        string(role.AuthType),
		"ttl":              role.TTL,
		"max_ttl":          role.MaxTTL,
		"rotation_enabled": role.RotationEnabled,
		"rotation_cron":    role.RotationCron,
		"last_rotated_at":  role.LastRotatedAt,
		"next_rotation_at": role.NextRotationAt,
	}
	if role.AuthType == authScram && role.ScramMechanism != "" {
		out["scram_mechanism"] = role.ScramMechanism
	}
	// omit static_password
	if role.StaticUsername != "" {
		out["static_username"] = role.StaticUsername
	}
	if len(role.StaticProps) > 0 {
		out["static_props"] = role.StaticProps
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

