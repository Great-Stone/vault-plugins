package plugin

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/masterzen/winrm"
	"github.com/robfig/cron/v3"
	"golang.org/x/crypto/ssh"
)

func pathStaticRoles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "static-roles/" + framework.GenericNameRegex("name"),
		ExistenceCheck: func(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
			name := data.Get("name").(string)
			if name == "" {
				return false, nil
			}
			e, err := req.Storage.Get(ctx, staticRoleStoragePrefix+name)
			return e != nil, err
		},
		Fields: map[string]*framework.FieldSchema{
			"name": {Type: framework.TypeString, Description: "Static role name"},
			"config_name": {
				Type:        framework.TypeString,
				Description: "Reference to config/<name>. Host/transport/admin settings are inherited from that config profile.",
			},
			"username": {
				Type:        framework.TypeString,
				Description: "Local account whose password is stored and rotated. Required when creating a role.",
				Required:    true,
			},
			"validate_user_exists": {
				Type:        framework.TypeBool,
				Description: "If true, validate that the target username exists on the host during write (uses the referenced config/<name> admin credential).",
			},
			"password": {
				Type:        framework.TypeString,
				Description: "Current password for the managed account (optional until first successful rotation).",
				DisplayAttrs: &framework.DisplayAttributes{
					Sensitive: true,
				},
			},
			"rotation_cron": {
				Type:        framework.TypeString,
				Description: "Cron schedule in UTC for password rotation (e.g. '0 */6 * * *'). Required when creating a role.",
				Required:    true,
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathStaticRoleRead},
			logical.CreateOperation: &framework.PathOperation{Callback: b.pathStaticRoleWrite},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathStaticRoleWrite},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathStaticRoleDelete},
		},
		HelpSynopsis:    "Manage static machine credential roles",
		HelpDescription: "Static roles map one Vault name to one host and local username, similar to database static roles.",
	}
}

func pathStaticRoleList(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "static-roles/?",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{Callback: b.pathStaticRoleList},
		},
		HelpSynopsis: "List static role names",
	}
}

func (b *backend) pathStaticRoleWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if name == "" {
		return logical.ErrorResponse("name is required"), nil
	}

	validateUserExists := false
	if v, ok := data.GetOk("validate_user_exists"); ok {
		validateUserExists = v.(bool)
	}

	existing, err := getStaticRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}

	var role staticRole
	if existing != nil {
		role = *existing
	}

	if v, ok := data.GetOk("config_name"); ok {
		role.ConfigName = strings.TrimSpace(v.(string))
	}
	if existing == nil && strings.TrimSpace(role.ConfigName) == "" {
		return logical.ErrorResponse("config_name is required"), nil
	}

	if v, ok := data.GetOk("username"); ok {
		role.Username = strings.TrimSpace(v.(string))
	} else if existing == nil {
		return logical.ErrorResponse("username is required"), nil
	}

	if v, ok := data.GetOk("rotation_cron"); ok {
		role.RotationCron = strings.TrimSpace(v.(string))
	} else if existing == nil {
		return logical.ErrorResponse("rotation_cron is required"), nil
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(role.RotationCron); err != nil {
		return logical.ErrorResponse("invalid rotation_cron: %v", err), nil
	}

	if v, ok := data.GetOk("password"); ok {
		role.Password = v.(string)
	}
	if strings.TrimSpace(role.Username) == "" {
		return logical.ErrorResponse("username is required"), nil
	}
	if strings.TrimSpace(role.ConfigName) == "" {
		return logical.ErrorResponse("config_name is required"), nil
	}

	// static-roles must not carry host/transport/admin settings; they come from config/<name>.
	role.OS = ""
	role.Host = ""
	role.Port = 0
	role.AdminUsername = ""
	role.AdminPassword = ""
	role.AdminPrivateKey = ""
	role.AdminPrivateKeyPassphrase = ""
	role.SudoPassword = ""
	role.WinRMInsecure = false
	role.WinRMAuth = ""

	prof, err := getConfigProfile(ctx, req.Storage, role.ConfigName)
	if err != nil {
		return nil, err
	}
	if prof == nil {
		return logical.ErrorResponse("unknown config_name %q", role.ConfigName), nil
	}
	if strings.TrimSpace(prof.OS) == "" || strings.TrimSpace(prof.Host) == "" {
		return logical.ErrorResponse("config %q is incomplete (missing os/host)", role.ConfigName), nil
	}
	if strings.TrimSpace(prof.AdminUsername) == "" {
		return logical.ErrorResponse("config %q is incomplete (missing admin_username)", role.ConfigName), nil
	}
	if strings.TrimSpace(prof.AdminPassword) == "" && strings.TrimSpace(prof.AdminPrivateKey) == "" {
		return logical.ErrorResponse("config %q is incomplete (missing admin_password or admin_private_key)", role.ConfigName), nil
	}

	if validateUserExists {
		defaults, _ := loadMachineConfig(ctx, req.Storage)
		osLower := normalizeOS(prof.OS)
		port := prof.effectivePort(defaults, osLower)
		switch osLower {
		case "linux":
			if err := linuxUserExists(prof.Host, port, prof.AdminUsername, prof.AdminPassword, prof.AdminPrivateKey, prof.AdminPrivateKeyPassphrase, role.Username); err != nil {
				return logical.ErrorResponse("target user validation failed: %v", err), nil
			}
		case "windows":
			auth := prof.effectiveWinRMAuth(defaults)
			if strings.TrimSpace(prof.AdminPassword) == "" {
				return logical.ErrorResponse("target user validation requires admin_password for winrm"), nil
			}
			if err := windowsUserExists(ctx, auth, prof.Host, port, prof.WinRMUseHTTPS, prof.WinRMSkipTLSVerify, prof.AdminUsername, prof.AdminPassword, role.Username); err != nil {
				return logical.ErrorResponse("target user validation failed: %v", err), nil
			}
		default:
			return logical.ErrorResponse("config %q has unsupported os %q", role.ConfigName, prof.OS), nil
		}
	}

	if existing == nil {
		role.LastRotatedAt = ""
		role.NextRotationAt = ""
	}

	entry, err := logical.StorageEntryJSON(staticRoleStoragePrefix+name, role)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, entry); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathStaticRoleRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	role, err := getStaticRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, nil
	}
	out := map[string]interface{}{
		"config_name":       role.ConfigName,
		"username":         role.Username,
		"rotation_cron":    role.RotationCron,
		"last_rotated_at":  role.LastRotatedAt,
		"next_rotation_at": role.NextRotationAt,
	}
	return &logical.Response{Data: out}, nil
}

func (b *backend) pathStaticRoleDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := strings.TrimSpace(data.Get("name").(string))
	if err := req.Storage.Delete(ctx, staticRoleStoragePrefix+name); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *backend) pathStaticRoleList(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	list, err := req.Storage.List(ctx, staticRoleStoragePrefix)
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(list), nil
}

func getStaticRole(ctx context.Context, s logical.Storage, name string) (*staticRole, error) {
	e, err := s.Get(ctx, staticRoleStoragePrefix+name)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	var r staticRole
	if err := e.DecodeJSON(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func putStaticRole(ctx context.Context, s logical.Storage, name string, r *staticRole) error {
	if r == nil {
		return fmt.Errorf("static role is nil")
	}
	entry, err := logical.StorageEntryJSON(staticRoleStoragePrefix+name, r)
	if err != nil {
		return err
	}
	return s.Put(ctx, entry)
}

func linuxUserExists(host string, port int, adminUser, adminPass, adminPrivateKeyPEM, adminPrivateKeyPassphrase, targetUser string) error {
	if strings.TrimSpace(targetUser) == "" {
		return fmt.Errorf("username is empty")
	}
	ub64 := base64.StdEncoding.EncodeToString([]byte(targetUser))
	remoteCmd := fmt.Sprintf(`set -e; u="$(echo %s | base64 -d)"; id -u "$u" >/dev/null 2>&1`, ub64)

	authMethod, err := buildSSHAuthMethod(adminPass, adminPrivateKeyPEM, adminPrivateKeyPassphrase)
	if err != nil {
		return err
	}
	config := &ssh.ClientConfig{
		User:            adminUser,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer conn.Close()

	session, err := conn.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	if err := session.Run(remoteCmd); err != nil {
		return fmt.Errorf("user %q not found (or cannot be queried): %w", targetUser, err)
	}
	return nil
}

func windowsUserExists(ctx context.Context, authType string, host string, port int, useHTTPS, insecureTLS bool, adminUser, adminPass, targetUser string) error {
	if strings.TrimSpace(targetUser) == "" {
		return fmt.Errorf("username is empty")
	}
	ub64 := base64.StdEncoding.EncodeToString([]byte(targetUser))
	script := fmt.Sprintf(
		`$u = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')); $x = Get-LocalUser -Name $u -ErrorAction SilentlyContinue; if ($null -eq $x) { exit 2 } else { exit 0 }`,
		ub64,
	)
	enc, err := powershellEncodedCommand(script)
	if err != nil {
		return err
	}
	cmd := `powershell.exe -NonInteractive -NoProfile -ExecutionPolicy Bypass -EncodedCommand ` + enc

	endpoint := winrm.NewEndpoint(host, port, useHTTPS, insecureTLS, nil, nil, nil, 45*time.Second)
	params := *winrm.DefaultParameters
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "", "basic":
	case "ntlm":
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	default:
		return fmt.Errorf("unsupported winrm auth type %q", authType)
	}
	client, err := winrm.NewClientWithParameters(endpoint, adminUser, adminPass, &params)
	if err != nil {
		return fmt.Errorf("winrm client: %w", err)
	}

	var stdoutBuf, stderrBuf strings.Builder
	exitCode, err := client.RunWithContext(ctx, cmd, &stdoutBuf, &stderrBuf)
	if err != nil {
		return fmt.Errorf("winrm run: %w", err)
	}
	if exitCode != 0 {
		// 2 is our "not found" signal; still present a clear message.
		if exitCode == 2 {
			return fmt.Errorf("user %q not found", targetUser)
		}
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = strings.TrimSpace(stdoutBuf.String())
		}
		if msg != "" {
			return fmt.Errorf("winrm exit %d: %s", exitCode, msg)
		}
		return fmt.Errorf("winrm exit %d", exitCode)
	}
	return nil
}
