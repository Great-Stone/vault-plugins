package plugin

import (
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
		RPID:          c.rpID,
		RPDisplayName: "Vault",
		RPOrigins:     c.origins,
		// RPIcon is optional
	})
}

func defaultAuthSelection() protocol.AuthenticatorSelection {
	return protocol.AuthenticatorSelection{
		AuthenticatorAttachment: protocol.Platform,
		RequireResidentKey:      protocol.ResidentKeyRequired(),
		ResidentKey:             protocol.ResidentKeyRequirementRequired,
		UserVerification:        protocol.VerificationRequired,
	}
}
