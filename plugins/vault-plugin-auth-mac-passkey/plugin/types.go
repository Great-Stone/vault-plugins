package plugin

import (
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

type configEntry struct {
	RPID                   string   `json:"rp_id"`
	AllowedOrigins         []string `json:"allowed_origins"`
	AllowedUserHandleRegex string   `json:"allowed_user_handle_regex"`
	ChallengeTTL           string   `json:"challenge_ttl"`
}

func (c *configEntry) challengeTTL() time.Duration {
	if c == nil || c.ChallengeTTL == "" {
		return 2 * time.Minute
	}
	d, err := time.ParseDuration(c.ChallengeTTL)
	if err != nil {
		return 2 * time.Minute
	}
	if d <= 0 {
		return 2 * time.Minute
	}
	return d
}

type roleEntry struct {
	Policies []string `json:"policies"`
	TTL      string   `json:"ttl"`
	MaxTTL   string   `json:"max_ttl"`

	AllowedUserHandleRegex string `json:"allowed_user_handle_regex"`
}

type credentialRecord struct {
	RPID         string `json:"rp_id"`
	UserHandle   string `json:"user_handle"`
	CredentialID []byte `json:"credential_id"`
	PublicKey    []byte `json:"public_key_cose"`
	SignCount    uint32 `json:"sign_count"`
	BackupEligible bool `json:"backup_eligible"`
	BackupState    bool `json:"backup_state"`
}

type pendingSession struct {
	Kind       string               `json:"kind"` // "register" or "login"
	RPID       string               `json:"rp_id"`
	RoleName   string               `json:"role_name,omitempty"`
	UserHandle string               `json:"user_handle"`
	Session    webauthn.SessionData `json:"session"`
	CreatedAt  time.Time            `json:"created_at"`
	ExpiresAt  time.Time            `json:"expires_at"`
}
