package plugin

import (
	"context"
	"encoding/base64"
	"regexp"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func pathRegisterBegin(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "register/begin",
		Fields: map[string]*framework.FieldSchema{
			"user_handle": {Type: framework.TypeString, Required: true},
			"user_name":   {Type: framework.TypeString, Required: false},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{Callback: b.handleRegisterBegin},
		},
	}
}

func pathRegisterFinish(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "register/finish",
		Fields: map[string]*framework.FieldSchema{
			"session_id": {Type: framework.TypeString, Required: true},
			"credential": {Type: framework.TypeString, Required: true, Description: "Base64url-encoded JSON of the browser PublicKeyCredential response."},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{Callback: b.handleRegisterFinish},
		},
	}
}

type webauthnUser struct {
	id          []byte
	name        string
	displayName string
	creds       []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte          { return u.id }
func (u *webauthnUser) WebAuthnName() string        { return u.name }
func (u *webauthnUser) WebAuthnDisplayName() string { return u.displayName }
func (u *webauthnUser) WebAuthnIcon() string        { return "" }
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.creds
}

func (b *backend) handleRegisterBegin(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
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

	userHandle := d.Get("user_handle").(string)
	if cfg.AllowedUserHandleRegex != "" {
		re, _ := regexp.Compile(cfg.AllowedUserHandleRegex)
		if !re.MatchString(userHandle) {
			return logical.ErrorResponse("user_handle is not allowed"), nil
		}
	}
	userName := d.Get("user_name").(string)
	if userName == "" {
		userName = userHandle
	}

	u := &webauthnUser{
		id:          []byte(userHandle),
		name:        userName,
		displayName: userName,
		creds:       nil,
	}

	sessionID := uuid.NewString()
	options, sessData, err := wa.BeginRegistration(
		u,
		webauthn.WithAuthenticatorSelection(defaultAuthSelection()),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	ps := &pendingSession{
		Kind:       "register",
		RPID:       cfg.RPID,
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

func (b *backend) handleRegisterFinish(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
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
	if ps == nil || ps.Kind != "register" {
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
		creds:       nil,
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(credJSON)
	if err != nil {
		return logical.ErrorResponse("webauthn credential parse failed: %v", err), nil
	}
	parsedResponse, err := wa.CreateCredential(u, ps.Session, parsed)
	if err != nil {
		return logical.ErrorResponse("webauthn registration failed: %v", err), nil
	}

	rec := &credentialRecord{
		RPID:         cfg.RPID,
		UserHandle:   ps.UserHandle,
		CredentialID: parsedResponse.ID,
		PublicKey:    parsedResponse.PublicKey,
		SignCount:    parsedResponse.Authenticator.SignCount,
		BackupEligible: parsedResponse.Flags.BackupEligible,
		BackupState:    parsedResponse.Flags.BackupState,
	}
	if err := saveCredential(ctx, req.Storage, rec); err != nil {
		return nil, err
	}
	_ = deleteSession(ctx, req.Storage, sessionID)

	return &logical.Response{Data: map[string]any{
		"registered":      true,
		"user_handle":     rec.UserHandle,
		"credential_id":   base64.RawURLEncoding.EncodeToString(rec.CredentialID),
		"backup_eligible": parsedResponse.Flags.BackupEligible,
		"backup_state":    parsedResponse.Flags.BackupState,
	}}, nil
}
