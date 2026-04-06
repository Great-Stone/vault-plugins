package plugin

import "strings"

const (
	defaultRoleTTL    = 3600
	defaultRoleMaxTTL = 86400
)

type kafkaConfig struct {
	BootstrapServers string `json:"bootstrap_servers"`
	// AdminBootstrapServers is optional. If empty, BootstrapServers is used.
	AdminBootstrapServers string `json:"admin_bootstrap_servers,omitempty"`
	// AdminUsername/AdminPassword are used for admin operations when the cluster requires authentication.
	AdminUsername string `json:"admin_username,omitempty"`
	AdminPassword string `json:"admin_password,omitempty"`
	// SecurityProtocol is optional (e.g. PLAINTEXT, SASL_PLAINTEXT, SASL_SSL, SSL).
	SecurityProtocol string `json:"security_protocol,omitempty"`
	// SaslMechanism is optional (e.g. SCRAM-SHA-256).
	SaslMechanism string `json:"sasl_mechanism,omitempty"`
}

func (c kafkaConfig) effectiveAdminBootstrap() string {
	if strings.TrimSpace(c.AdminBootstrapServers) != "" {
		return c.AdminBootstrapServers
	}
	return c.BootstrapServers
}

type authType string

const (
	authScram authType = "scram"
	authPlain authType = "plain"
	authMtls  authType = "mtls"
)

// dynamicRole defines v1 dynamic issuance behavior (SCRAM only).
type dynamicRole struct {
	// Dynamic SCRAM
	ScramMechanism string `json:"scram_mechanism,omitempty"` // SCRAM-SHA-256 or SCRAM-SHA-512
	// ACL allows optional very small v1 scope: a single topic + group prefix.
	Topic       string `json:"topic,omitempty"`
	GroupPrefix string `json:"group_prefix,omitempty"`

	TTL    int `json:"ttl"`
	MaxTTL int `json:"max_ttl"`
}

// staticRole defines v1 static bundle delivery.
// Static roles do not create/delete Kafka users; they return stored bundles.
// For auth_type=scram, an optional rotation scheduler can update the password in Kafka and in storage.
type staticRole struct {
	AuthType authType `json:"auth_type"`

	// Static bundle
	StaticUsername string            `json:"static_username,omitempty"`
	StaticPassword string            `json:"static_password,omitempty"`
	StaticProps    map[string]string `json:"static_props,omitempty"`

	// SCRAM-specific (static distribution + rotation)
	ScramMechanism string `json:"scram_mechanism,omitempty"` // SCRAM-SHA-256 or SCRAM-SHA-512

	// Rotation
	RotationEnabled bool   `json:"rotation_enabled,omitempty"`
	RotationCron    string `json:"rotation_cron,omitempty"` // e.g. "*/5 * * * *"
	LastRotatedAt   string `json:"last_rotated_at,omitempty"`
	NextRotationAt  string `json:"next_rotation_at,omitempty"`

	TTL    int `json:"ttl"`
	MaxTTL int `json:"max_ttl"`
}

