package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/hashicorp/vault/sdk/logical"
)

const passkeyAliasPrefix = "passkey_"

// passkeyIdentityAliasName returns the stable Vault identity entity-alias name for this auth mount:
// passkey_ + first 8 hex chars of SHA-256(v1|entityID|mountAccessor). Same entity + same mount ⇒ same name.
// This must be used for both enrollment-time alias creation and login Auth.Alias.Name so Vault does not
// fall back to creating an alias named after user_handle (which breaks merging into the enrollment entity).
func passkeyIdentityAliasName(entityID, mountAccessor string) string {
	if entityID == "" || mountAccessor == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("vault-plugin-auth-mac-passkey|entity-alias|v1\x00" + entityID + "\x00" + mountAccessor))
	return passkeyAliasPrefix + hex.EncodeToString(sum[:4])
}

// ensurePasskeyEntityAlias creates (idempotently) an identity entity-alias on this auth mount for
// canonicalEntityID. The alias name is always passkeyIdentityAliasName(entity, mount).
// If an alias already exists on this mount with a different name (legacy: user_handle / entity id string),
// it is deleted first when ForwardGenericRequest is available.
func (b *backend) ensurePasskeyEntityAlias(ctx context.Context, req *logical.Request, canonicalEntityID string) (aliasName string, warnings []string, err error) {
	if canonicalEntityID == "" {
		return "", nil, fmt.Errorf("canonical entity id is required")
	}
	if req.MountAccessor == "" {
		return "", []string{"passkey credential saved; identity entity-alias was not created at enrollment (mount_accessor missing on request). Upgrade Vault or rely on EntityID on first passkey login."}, nil
	}

	desired := passkeyIdentityAliasName(canonicalEntityID, req.MountAccessor)
	if desired == "" {
		return "", nil, fmt.Errorf("could not derive passkey identity alias name")
	}

	ent, err := b.System().EntityInfo(canonicalEntityID)
	if err != nil {
		return "", nil, fmt.Errorf("entity lookup: %w", err)
	}
	if ent != nil {
		for _, a := range ent.GetAliases() {
			if a.GetMountAccessor() != req.MountAccessor {
				continue
			}
			if a.GetName() == desired {
				return desired, nil, nil
			}
			// Stale alias (e.g. same name as entity / user_handle principal) — replace with passkey_* name.
			ext, ok := b.System().(logical.ExtendedSystemView)
			if !ok {
				return "", []string{fmt.Sprintf("passkey credential saved; entity already has alias %q on this mount but plugin cannot replace it (no ForwardGenericRequest). Remove the old alias manually or upgrade Vault.", a.GetName())}, nil
			}
			if id := strings.TrimSpace(a.GetID()); id != "" {
				del := &logical.Request{
					Operation: logical.DeleteOperation,
					Path:      "identity/entity-alias/id/" + id,
				}
				delResp, delErr := ext.ForwardGenericRequest(ctx, del)
				if delErr != nil {
					return "", []string{fmt.Sprintf("passkey credential saved; could not remove stale identity alias %q: %v", a.GetName(), delErr)}, nil
				}
				if delResp != nil && delResp.IsError() {
					msg := ""
					if e := delResp.Error(); e != nil {
						msg = e.Error()
					}
					return "", []string{fmt.Sprintf("passkey credential saved; could not remove stale identity alias %q: %s", a.GetName(), msg)}, nil
				}
			}
			break
		}
	}

	ext, ok := b.System().(logical.ExtendedSystemView)
	if !ok {
		return "", []string{"passkey credential saved; identity entity-alias was not created at enrollment (this Vault runtime does not expose ForwardGenericRequest to credential plugins). Upgrade Vault for enrollment-time aliases; login still uses Auth.Alias.Name passkey_* so Vault can attach to the correct entity."}, nil
	}

	fwd := &logical.Request{
		Operation: logical.UpdateOperation,
		Path:      "identity/entity-alias",
		Data: map[string]interface{}{
			"name":           desired,
			"canonical_id":   canonicalEntityID,
			"mount_accessor": req.MountAccessor,
		},
	}
	resp, err := ext.ForwardGenericRequest(ctx, fwd)
	if err != nil {
		return "", []string{fmt.Sprintf("passkey credential saved; could not create identity entity-alias: %v", err)}, nil
	}
	if resp == nil {
		return "", []string{"passkey credential saved; empty response from identity/entity-alias"}, nil
	}
	if resp.IsError() {
		msg := ""
		if e := resp.Error(); e != nil {
			msg = e.Error()
		}
		low := strings.ToLower(msg)
		if strings.Contains(low, "already exists") || strings.Contains(low, "duplicate") {
			return desired, nil, nil
		}
		return "", []string{fmt.Sprintf("passkey credential saved; identity/entity-alias failed: %s", msg)}, nil
	}
	return desired, nil, nil
}
