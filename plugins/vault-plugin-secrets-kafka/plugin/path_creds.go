package plugin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

var (
	sanitizeNameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
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
		HelpSynopsis:    "Return Kafka static credential bundle for a role",
		HelpDescription: "Returns the stored credential bundle for the given static role. For auth_type=scram, the password is rotated by the scheduler according to rotation_cron on the role. Vault lease TTL for this response is fixed by the plugin (not related to rotation).",
	}
}

func (b *backend) pathStaticCredsRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	roleName := strings.TrimSpace(data.Get("role").(string))
	if roleName == "" {
		return logical.ErrorResponse("role is required"), nil
	}

	cfg, err := b.loadConfig(ctx, req.Storage)
	if err != nil {
		return logical.ErrorResponse("%s", err.Error()), nil
	}

	role, err := getStaticRole(ctx, req.Storage, roleName)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return logical.ErrorResponse("unknown role %q", roleName), nil
	}

	// Vault lease bookkeeping only; Kafka password rotation is driven by static role rotation_cron.
	leaseTTL := time.Duration(defaultRoleTTL) * time.Second

	secProto := strings.TrimSpace(cfg.SecurityProtocol)
	if secProto == "" {
		secProto = "n/a"
	}
	saslMech := strings.TrimSpace(cfg.SaslMechanism)
	if saslMech == "" {
		saslMech = "n/a"
	}

	out := map[string]interface{}{
		"mode":              "static",
		"auth_type":         string(role.AuthType),
		"bootstrap_servers": cfg.BootstrapServers,
		"security_protocol": secProto,
		"sasl_mechanism":    saslMech,
		"username":          role.StaticUsername,
		"password":          role.StaticPassword,
		"properties":        role.StaticProps,
		"last_rotated_at":   role.LastRotatedAt,
		"next_rotation_at":  role.NextRotationAt,
	}
	if role.AuthType == authPlain {
		// For PLAIN, if config doesn't specify, default to SASL_PLAINTEXT.
		out["sasl_mechanism"] = "PLAIN"
		if secProto == "n/a" {
			out["security_protocol"] = "SASL_PLAINTEXT"
		}
	} else if role.AuthType == authScram && strings.TrimSpace(role.ScramMechanism) != "" {
		out["sasl_mechanism"] = strings.ToUpper(strings.TrimSpace(role.ScramMechanism))
		// If config doesn't specify, default to SASL_PLAINTEXT for SCRAM in examples.
		if secProto == "n/a" {
			out["security_protocol"] = "SASL_PLAINTEXT"
		}
	}

	resp := b.Secret(scramSecretType).Response(out, map[string]interface{}{
		"mode":     "static",
		"role":     roleName,
		"username": role.StaticUsername,
	})
	resp.Secret.TTL = leaseTTL
	resp.Secret.MaxTTL = time.Duration(defaultRoleMaxTTL) * time.Second
	return resp, nil
}

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
		HelpSynopsis:    "Issue Kafka credentials for a role",
		HelpDescription: "Returns a credential bundle for the given dynamic role. v1 supports dynamic SCRAM issuance.",
	}
}

func (b *backend) pathCredsRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	roleName := strings.TrimSpace(data.Get("role").(string))
	if roleName == "" {
		return logical.ErrorResponse("role is required"), nil
	}

	cfg, err := b.loadConfig(ctx, req.Storage)
	if err != nil {
		return logical.ErrorResponse("%s", err.Error()), nil
	}
	role, err := b.getDynamicRole(ctx, req.Storage, roleName)
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
	leaseTTL := time.Duration(ttlSec) * time.Second

	admin, err := newAdminClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer admin.Close()

	// v1 username convention: vault_<role>_<random>
	safeRole := sanitizeNameRe.ReplaceAllString(roleName, "_")
	if safeRole == "" {
		safeRole = "role"
	}
	pw, err := randomPassword(24)
	if err != nil {
		return nil, err
	}
	suffix, err := randomPassword(6)
	if err != nil {
		return nil, err
	}
	username := fmt.Sprintf("vault_%s_%s", safeRole, suffix)

	var mech sarama.ScramMechanismType
	switch strings.ToUpper(role.ScramMechanism) {
	case "SCRAM-SHA-256":
		mech = sarama.SCRAM_MECHANISM_SHA_256
	case "SCRAM-SHA-512":
		mech = sarama.SCRAM_MECHANISM_SHA_512
	default:
		return logical.ErrorResponse("unsupported scram_mechanism %q", role.ScramMechanism), nil
	}

	// Create SCRAM credential.
	_, err = admin.admin.UpsertUserScramCredentials([]sarama.AlterUserScramCredentialsUpsert{
		{
			Name:       username,
			Mechanism:  mech,
			Iterations: 4096,
			Salt:       nil,
			Password:   []byte(pw),
		},
	})
	if err != nil {
		return nil, err
	}

	// Optional minimal ACLs.
	// Sarama's CreateACLs expects []*ResourceAcls.
	var resourceAcls []*sarama.ResourceAcls
	if role.Topic != "" {
		resourceAcls = append(resourceAcls, &sarama.ResourceAcls{
			Resource: sarama.Resource{
				ResourceType:        sarama.AclResourceTopic,
				ResourceName:        role.Topic,
				ResourcePatternType: sarama.AclPatternLiteral,
			},
			Acls: []*sarama.Acl{
				{Principal: "User:" + username, Host: "*", Operation: sarama.AclOperationRead, PermissionType: sarama.AclPermissionAllow},
				{Principal: "User:" + username, Host: "*", Operation: sarama.AclOperationWrite, PermissionType: sarama.AclPermissionAllow},
				{Principal: "User:" + username, Host: "*", Operation: sarama.AclOperationDescribe, PermissionType: sarama.AclPermissionAllow},
			},
		})
	}
	if role.GroupPrefix != "" {
		resourceAcls = append(resourceAcls, &sarama.ResourceAcls{
			Resource: sarama.Resource{
				ResourceType:        sarama.AclResourceGroup,
				ResourceName:        role.GroupPrefix,
				ResourcePatternType: sarama.AclPatternPrefixed,
			},
			Acls: []*sarama.Acl{
				{Principal: "User:" + username, Host: "*", Operation: sarama.AclOperationRead, PermissionType: sarama.AclPermissionAllow},
				{Principal: "User:" + username, Host: "*", Operation: sarama.AclOperationDescribe, PermissionType: sarama.AclPermissionAllow},
			},
		})
	}
	if len(resourceAcls) > 0 {
		if err := admin.admin.CreateACLs(resourceAcls); err != nil {
			// Best-effort cleanup of user on partial failure.
			_, _ = admin.admin.DeleteUserScramCredentials([]sarama.AlterUserScramCredentialsDelete{
				{Name: username, Mechanism: mech},
			})
			return nil, err
		}
	}

	out := map[string]interface{}{
		"mode":              "dynamic",
		"auth_type":         "scram",
		"bootstrap_servers": cfg.BootstrapServers,
		"security_protocol": "SASL_PLAINTEXT",
		"sasl_mechanism":    strings.ToUpper(role.ScramMechanism),
		"username":          username,
		"password":          pw,
		"topic":             role.Topic,
		"group_prefix":      role.GroupPrefix,
	}

	internal := map[string]interface{}{
		"mode":            "dynamic",
		"role":            roleName,
		"username":        username,
		"scram_mechanism": strings.ToUpper(role.ScramMechanism),
		"topic":           role.Topic,
		"group_prefix":    role.GroupPrefix,
	}

	resp := b.Secret(scramSecretType).Response(out, internal)
	resp.Secret.TTL = leaseTTL
	resp.Secret.MaxTTL = time.Duration(role.MaxTTL) * time.Second
	return resp, nil
}
