package plugin

import (
	"crypto/sha256"
	"fmt"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type webauthnConfig struct {
	rpID    string
	origins []string
}

func (c *webauthnConfig) toWebAuthn() (*webauthn.WebAuthn, error) {
	if c.rpID == "" || len(c.origins) == 0 {
		return nil, fmt.Errorf("webauthn is not configured")
	}
	return webauthn.New(&webauthn.Config{
		RPID:                 c.rpID,
		RPDisplayName:        "Vault",
		RPOrigins:            c.origins,
		EncodeUserIDAsString: false, // always base64url in JSON; avoids client mis-decoding UTF-8 as base64
		// RPIcon is optional
	})
}

// webauthnUserIDBytes maps the Vault passkey principal to WebAuthn user.id (must be 1–64 bytes).
// We always use SHA-256 so length is always 32 bytes and never depends on entity name encoding.
func webauthnUserIDBytes(userHandle string) []byte {
	sum := sha256.Sum256([]byte(userHandle))
	return sum[:]
}

func defaultAuthSelection() protocol.AuthenticatorSelection {
	return protocol.AuthenticatorSelection{
		AuthenticatorAttachment: protocol.Platform,
		RequireResidentKey:      protocol.ResidentKeyRequired(),
		ResidentKey:             protocol.ResidentKeyRequirementRequired,
		UserVerification:        protocol.VerificationRequired,
	}
}
