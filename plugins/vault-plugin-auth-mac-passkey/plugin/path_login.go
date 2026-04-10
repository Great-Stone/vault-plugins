package plugin

import (
	"context"
	"encoding/base64"
	"regexp"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/google/uuid"
	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathLoginBegin(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "login/begin",
		Fields: map[string]*framework.FieldSchema{
			"role":        {Type: framework.TypeString, Required: true},
			"user_handle": {Type: framework.TypeString, Required: true},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{Callback: b.handleLoginBegin},
		},
	}
}

func pathLoginFinish(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "login/finish",
		Fields: map[string]*framework.FieldSchema{
			"session_id": {Type: framework.TypeString, Required: true},
			"credential": {Type: framework.TypeString, Required: true, Description: "Base64url-encoded JSON of the browser PublicKeyCredential response."},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{Callback: b.handleLoginFinish},
		},
	}
}

func (b *backend) handleLoginBegin(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	cfg, err := loadConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if err := requireConfigured(cfg); err != nil {
		return logical.ErrorResponse(err.Error()), nil
	}
	wa, err := (&webauthnConfig{rpID: cfg.RPID, origins: cfg.AllowedOrigins}).toWebAuthn()
	if err != nil {
		return nil, err
	}

	roleName := d.Get("role").(string)
	role, err := loadRole(ctx, req.Storage, roleName)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return logical.ErrorResponse("unknown role"), nil
	}

	userHandle := d.Get("user_handle").(string)
	if cfg.AllowedUserHandleRegex != "" {
		re, _ := regexp.Compile(cfg.AllowedUserHandleRegex)
		if !re.MatchString(userHandle) {
			return logical.ErrorResponse("user_handle is not allowed"), nil
		}
	}
	if role.AllowedUserHandleRegex != "" {
		re, _ := regexp.Compile(role.AllowedUserHandleRegex)
		if !re.MatchString(userHandle) {
			return logical.ErrorResponse("user_handle is not allowed for this role"), nil
		}
	}

	creds, err := loadCredentialsForUser(ctx, req.Storage, cfg.RPID, userHandle)
	if err != nil {
		return nil, err
	}
	if len(creds) == 0 {
		return logical.ErrorResponse("no registered credentials for user_handle"), nil
	}

	u := &webauthnUser{
		id:          []byte(userHandle),
		name:        userHandle,
		displayName: userHandle,
		creds:       creds,
	}

	// We don't enumerate allowed credentials yet; the authenticator will pick a matching resident credential
	// for this RPID + user handle.
	options, sessData, err := wa.BeginLogin(u)
	if err != nil {
		return nil, err
	}

	sessionID := uuid.NewString()
	now := time.Now().UTC()
	ps := &pendingSession{
		Kind:       "login",
		RPID:       cfg.RPID,
		RoleName:   roleName,
		UserHandle: userHandle,
		Session:    *sessData,
		CreatedAt:  now,
		ExpiresAt:  now.Add(cfg.challengeTTL()),
	}
	if err := saveSession(ctx, req.Storage, sessionID, ps); err != nil {
		return nil, err
	}

	return &logical.Response{Data: map[string]any{
		"session_id": sessionID,
		"options":    options,
	}}, nil
}

func (b *backend) handleLoginFinish(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	cfg, err := loadConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if err := requireConfigured(cfg); err != nil {
		return logical.ErrorResponse(err.Error()), nil
	}
	wa, err := (&webauthnConfig{rpID: cfg.RPID, origins: cfg.AllowedOrigins}).toWebAuthn()
	if err != nil {
		return nil, err
	}

	sessionID := d.Get("session_id").(string)
	ps, err := loadSession(ctx, req.Storage, sessionID)
	if err != nil {
		return nil, err
	}
	if ps == nil || ps.Kind != "login" {
		return logical.ErrorResponse("invalid session"), nil
	}
	if time.Now().UTC().After(ps.ExpiresAt) {
		_ = deleteSession(ctx, req.Storage, sessionID)
		return logical.ErrorResponse("session expired"), nil
	}

	credJSONb64 := d.Get("credential").(string)
	credJSON, err := base64.RawURLEncoding.DecodeString(credJSONb64)
	if err != nil {
		return logical.ErrorResponse("credential must be base64url JSON"), nil
	}

	u := &webauthnUser{
		id:          []byte(ps.UserHandle),
		name:        ps.UserHandle,
		displayName: ps.UserHandle,
		creds:       nil, // loaded below
	}

	creds, err := loadCredentialsForUser(ctx, req.Storage, cfg.RPID, ps.UserHandle)
	if err != nil {
		return nil, err
	}
	u.creds = creds
	if len(u.creds) == 0 {
		return logical.ErrorResponse("no registered credentials for user_handle"), nil
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(credJSON)
	if err != nil {
		return logical.ErrorResponse("webauthn credential parse failed: %v", err), nil
	}
	_, err = wa.ValidateLogin(u, ps.Session, parsed)
	if err != nil {
		return logical.ErrorResponse("webauthn assertion failed: %v", err), nil
	}

	role, err := loadRole(ctx, req.Storage, ps.RoleName)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return logical.ErrorResponse("unknown role"), nil
	}

	// Issue Vault token.
	auth := &logical.Auth{
		DisplayName: ps.UserHandle,
		Alias: &logical.Alias{
			Name: ps.UserHandle,
		},
		Metadata: map[string]string{
			"user_handle": ps.UserHandle,
		},
		Policies: role.Policies,
	}
	if ttl := role.parsedTTL(); ttl > 0 {
		auth.TTL = ttl
	}
	if maxTTL := role.parsedMaxTTL(); maxTTL > 0 {
		auth.MaxTTL = maxTTL
	}

	_ = deleteSession(ctx, req.Storage, sessionID)

	return &logical.Response{Auth: auth}, nil
}
