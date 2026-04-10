package plugin

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/hashicorp/vault/sdk/logical"
)

const (
	storageConfigKey  = "config"
	storageRolePrefix = "role/"
	storageCredPrefix = "cred/" // cred/<rpId>/<b64url(credentialId)>
	storageSessPrefix = "sess/" // sess/<sessionId>
	storageUserPrefix = "user/" // user/<rpId>/<userHandle> -> list of credential IDs (b64url)
)

func loadConfig(ctx context.Context, s logical.Storage) (*configEntry, error) {
	e, err := s.Get(ctx, storageConfigKey)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return &configEntry{}, nil
	}
	var cfg configEntry
	if err := e.DecodeJSON(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func saveConfig(ctx context.Context, s logical.Storage, cfg *configEntry) error {
	e, err := logical.StorageEntryJSON(storageConfigKey, cfg)
	if err != nil {
		return err
	}
	return s.Put(ctx, e)
}

func roleKey(name string) string { return storageRolePrefix + name }

func loadRole(ctx context.Context, s logical.Storage, name string) (*roleEntry, error) {
	e, err := s.Get(ctx, roleKey(name))
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var r roleEntry
	if err := e.DecodeJSON(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func saveRole(ctx context.Context, s logical.Storage, name string, r *roleEntry) error {
	e, err := logical.StorageEntryJSON(roleKey(name), r)
	if err != nil {
		return err
	}
	return s.Put(ctx, e)
}

func deleteRole(ctx context.Context, s logical.Storage, name string) error {
	return s.Delete(ctx, roleKey(name))
}

func credKey(rpID string, credID []byte) string {
	return fmt.Sprintf("%s%s/%s", storageCredPrefix, rpID, base64.RawURLEncoding.EncodeToString(credID))
}

func userKey(rpID, userHandle string) string {
	return fmt.Sprintf("%s%s/%s", storageUserPrefix, rpID, userHandle)
}

type userIndex struct {
	CredentialIDs []string `json:"credential_ids"`
}

func loadUserIndex(ctx context.Context, s logical.Storage, rpID, userHandle string) (*userIndex, error) {
	e, err := s.Get(ctx, userKey(rpID, userHandle))
	if err != nil {
		return nil, err
	}
	if e == nil {
		return &userIndex{CredentialIDs: nil}, nil
	}
	var idx userIndex
	if err := e.DecodeJSON(&idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func saveUserIndex(ctx context.Context, s logical.Storage, rpID, userHandle string, idx *userIndex) error {
	e, err := logical.StorageEntryJSON(userKey(rpID, userHandle), idx)
	if err != nil {
		return err
	}
	return s.Put(ctx, e)
}

func saveCredential(ctx context.Context, s logical.Storage, rec *credentialRecord) error {
	e, err := logical.StorageEntryJSON(credKey(rec.RPID, rec.CredentialID), rec)
	if err != nil {
		return err
	}
	if err := s.Put(ctx, e); err != nil {
		return err
	}
	idx, err := loadUserIndex(ctx, s, rec.RPID, rec.UserHandle)
	if err != nil {
		return err
	}
	credIDb64 := base64.RawURLEncoding.EncodeToString(rec.CredentialID)
	for _, existing := range idx.CredentialIDs {
		if existing == credIDb64 {
			return nil
		}
	}
	idx.CredentialIDs = append(idx.CredentialIDs, credIDb64)
	return saveUserIndex(ctx, s, rec.RPID, rec.UserHandle, idx)
}

func loadCredentialByID(ctx context.Context, s logical.Storage, rpID string, credID []byte) (*credentialRecord, error) {
	e, err := s.Get(ctx, credKey(rpID, credID))
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var rec credentialRecord
	if err := e.DecodeJSON(&rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func loadCredentialsForUser(ctx context.Context, s logical.Storage, rpID, userHandle string) ([]webauthn.Credential, error) {
	idx, err := loadUserIndex(ctx, s, rpID, userHandle)
	if err != nil {
		return nil, err
	}
	out := make([]webauthn.Credential, 0, len(idx.CredentialIDs))
	for _, credIDb64 := range idx.CredentialIDs {
		credID, err := base64.RawURLEncoding.DecodeString(credIDb64)
		if err != nil {
			continue
		}
		rec, err := loadCredentialByID(ctx, s, rpID, credID)
		if err != nil || rec == nil {
			continue
		}
		out = append(out, webauthn.Credential{
			ID:              rec.CredentialID,
			PublicKey:       rec.PublicKey,
			Authenticator:   webauthn.Authenticator{SignCount: rec.SignCount},
			Flags:           webauthn.CredentialFlags{BackupEligible: rec.BackupEligible, BackupState: rec.BackupState},
			AttestationType: "",
		})
	}
	return out, nil
}

func sessionKey(id string) string { return storageSessPrefix + id }

func saveSession(ctx context.Context, s logical.Storage, id string, sess *pendingSession) error {
	e, err := logical.StorageEntryJSON(sessionKey(id), sess)
	if err != nil {
		return err
	}
	return s.Put(ctx, e)
}

func loadSession(ctx context.Context, s logical.Storage, id string) (*pendingSession, error) {
	e, err := s.Get(ctx, sessionKey(id))
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var sess pendingSession
	if err := e.DecodeJSON(&sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func deleteSession(ctx context.Context, s logical.Storage, id string) error {
	return s.Delete(ctx, sessionKey(id))
}
