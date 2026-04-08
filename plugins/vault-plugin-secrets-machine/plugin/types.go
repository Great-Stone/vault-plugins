package plugin

import "strings"

const (
	defaultRoleTTL    = 3600
	defaultRoleMaxTTL = 86400

	defaultSSHPort   = 22
	defaultWinRMPort = 5985

	staticRoleStoragePrefix = "static-role/"
	configStoragePrefix     = "config/"
)

// machineConfig holds optional defaults for connections. All fields are optional;
// unset values fall back to built-in defaults at rotation/read time.
type machineConfig struct {
	DefaultSSHPort     int  `json:"default_ssh_port,omitempty"`
	DefaultWinRMPort   int  `json:"default_winrm_port,omitempty"`
	WinRMUseHTTPS      bool `json:"winrm_use_https"`
	WinRMSkipTLSVerify bool `json:"winrm_skip_tls_verify"`
	// WinRMAuth controls auth scheme for WinRM: "basic" (default) or "ntlm".
	WinRMAuth string `json:"winrm_auth,omitempty"`
}

func (c *machineConfig) effectiveSSHPort() int {
	if c != nil && c.DefaultSSHPort > 0 {
		return c.DefaultSSHPort
	}
	return defaultSSHPort
}

func (c *machineConfig) effectiveWinRMPort() int {
	if c != nil && c.DefaultWinRMPort > 0 {
		return c.DefaultWinRMPort
	}
	return defaultWinRMPort
}

func (c *machineConfig) winRMHTTPS() bool {
	return c != nil && c.WinRMUseHTTPS
}

func (c *machineConfig) winRMSkipTLS() bool {
	return c != nil && c.WinRMSkipTLSVerify
}

func (c *machineConfig) effectiveWinRMAuth() string {
	if c == nil {
		return "basic"
	}
	switch normalizeOS(c.WinRMAuth) {
	case "ntlm":
		return "ntlm"
	default:
		return "basic"
	}
}

// machineConfigProfile stores host + transport + privileged credential settings (Database-like config/<name>).
// One profile is typically shared by many static-roles.
type machineConfigProfile struct {
	OS   string `json:"os"`   // linux | windows
	Host string `json:"host"` // address
	Port int    `json:"port,omitempty"`

	// WinRM settings (Windows)
	WinRMUseHTTPS      bool   `json:"winrm_use_https,omitempty"`
	WinRMSkipTLSVerify bool   `json:"winrm_skip_tls_verify,omitempty"`
	WinRMAuth          string `json:"winrm_auth,omitempty"` // basic|ntlm

	// SSH/WinRM privileged credential (Linux/Windows)
	AdminUsername             string `json:"admin_username"`
	AdminPassword             string `json:"admin_password,omitempty"`
	AdminPrivateKey           string `json:"admin_private_key,omitempty"`
	AdminPrivateKeyPassphrase string `json:"admin_private_key_passphrase,omitempty"`
	SudoPassword              string `json:"sudo_password,omitempty"`

	// Optional: rotate the privileged credential itself on a schedule.
	RootRotationCron   string `json:"root_rotation_cron,omitempty"`
	RootLastRotatedAt  string `json:"root_last_rotated_at,omitempty"`
	RootNextRotationAt string `json:"root_next_rotation_at,omitempty"`
}

func (p *machineConfigProfile) effectivePort(defaults *machineConfig, osLower string) int {
	if p != nil && p.Port > 0 {
		return p.Port
	}
	// fall back to engine defaults and built-ins
	switch osLower {
	case "windows":
		if defaults != nil {
			if defaults.WinRMUseHTTPS && defaults.DefaultWinRMPort <= 0 {
				return 5986
			}
			return defaults.effectiveWinRMPort()
		}
		return defaultWinRMPort
	default:
		if defaults != nil {
			return defaults.effectiveSSHPort()
		}
		return defaultSSHPort
	}
}

func (p *machineConfigProfile) effectiveWinRMAuth(defaults *machineConfig) string {
	if p != nil && strings.TrimSpace(p.WinRMAuth) != "" {
		switch normalizeOS(p.WinRMAuth) {
		case "ntlm":
			return "ntlm"
		default:
			return "basic"
		}
	}
	if defaults != nil {
		return defaults.effectiveWinRMAuth()
	}
	return "basic"
}

// staticRole describes one managed local account on a host (Database static-role analogue).
type staticRole struct {
	// ConfigName references config/<name>. When set, host/transport/admin settings are inherited from that profile.
	ConfigName string `json:"config_name,omitempty"`

	OS   string `json:"os"` // linux | windows (required if config_name is empty)
	Host string `json:"host"`
	Port int    `json:"port,omitempty"` // 0: use default for OS from config / built-ins
	Username string `json:"username"` // account whose password is stored and rotated

	Password string `json:"password,omitempty"` // current password; may be empty until first rotation

	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
	// AdminPrivateKey is an optional PEM-encoded private key for SSH auth (Linux).
	// If set, it is preferred over AdminPassword for SSH authentication.
	AdminPrivateKey string `json:"admin_private_key,omitempty"`
	// AdminPrivateKeyPassphrase is optional passphrase for the private key.
	AdminPrivateKeyPassphrase string `json:"admin_private_key_passphrase,omitempty"`

	// SudoPassword is optional. When empty, Linux rotation uses `sudo -n chpasswd` (passwordless sudo).
	SudoPassword string `json:"sudo_password,omitempty"`

	// WinRMInsecure skips TLS verification when true (per-role override; also see config winrm_skip_tls_verify).
	WinRMInsecure bool `json:"winrm_insecure,omitempty"`
	// WinRMAuth optionally overrides engine-level WinRM auth scheme: "basic" or "ntlm".
	WinRMAuth string `json:"winrm_auth,omitempty"`

	RotationCron   string `json:"rotation_cron"`
	LastRotatedAt  string `json:"last_rotated_at,omitempty"`
	NextRotationAt string `json:"next_rotation_at,omitempty"`
}

// roleEffective returns a role merged with its referenced config profile (if any).
// Role fields override profile fields when explicitly set on the role.
func (r *staticRole) roleEffective(profile *machineConfigProfile) *staticRole {
	if r == nil {
		return nil
	}
	out := *r
	if profile == nil {
		return &out
	}

	if strings.TrimSpace(out.OS) == "" {
		out.OS = profile.OS
	}
	if strings.TrimSpace(out.Host) == "" {
		out.Host = profile.Host
	}
	// port: if role.Port is 0, keep 0; effectivePort() will decide using profile/defaults.

	if strings.TrimSpace(out.AdminUsername) == "" {
		out.AdminUsername = profile.AdminUsername
	}
	if strings.TrimSpace(out.AdminPassword) == "" {
		out.AdminPassword = profile.AdminPassword
	}
	if strings.TrimSpace(out.AdminPrivateKey) == "" {
		out.AdminPrivateKey = profile.AdminPrivateKey
	}
	if strings.TrimSpace(out.AdminPrivateKeyPassphrase) == "" {
		out.AdminPrivateKeyPassphrase = profile.AdminPrivateKeyPassphrase
	}
	if strings.TrimSpace(out.SudoPassword) == "" {
		out.SudoPassword = profile.SudoPassword
	}
	if strings.TrimSpace(out.WinRMAuth) == "" {
		out.WinRMAuth = profile.WinRMAuth
	}
	if !out.WinRMInsecure {
		out.WinRMInsecure = profile.WinRMSkipTLSVerify
	}
	return &out
}

func (r *staticRole) effectivePort(cfg *machineConfig, osLower string) int {
	if r != nil && r.Port > 0 {
		return r.Port
	}
	switch osLower {
	case "windows":
		if cfg != nil {
			if cfg.WinRMUseHTTPS && cfg.DefaultWinRMPort <= 0 {
				return 5986
			}
			return cfg.effectiveWinRMPort()
		}
		return defaultWinRMPort
	default:
		if cfg != nil {
			return cfg.effectiveSSHPort()
		}
		return defaultSSHPort
	}
}
