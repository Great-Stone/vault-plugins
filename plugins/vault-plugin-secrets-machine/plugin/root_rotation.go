package plugin

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/vault/sdk/logical"
	"github.com/robfig/cron/v3"
)

// rotateDueRootConfigs rotates admin/root credentials stored in config/<name> when root_rotation_cron is set.
// It updates admin_password and root_last_rotated_at/root_next_rotation_at on success.
func (b *backend) rotateDueRootConfigs(ctx context.Context, s logical.Storage) error {
	defaults, _ := loadMachineConfig(ctx, s)

	names, err := s.List(ctx, configStoragePrefix)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}

	now := time.Now().UTC()
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	for _, name := range names {
		cfgName := strings.TrimSuffix(name, "/")
		cfgName = strings.TrimSpace(cfgName)
		if cfgName == "" {
			continue
		}

		p, err := getConfigProfile(ctx, s, cfgName)
		if err != nil || p == nil {
			continue
		}

		cronStr := strings.TrimSpace(p.RootRotationCron)
		if cronStr == "" {
			continue
		}
		// Root rotation only supported for password-based admin credentials.
		if strings.TrimSpace(p.AdminPassword) == "" {
			continue
		}
		if strings.TrimSpace(p.AdminUsername) == "" || strings.TrimSpace(p.Host) == "" {
			continue
		}

		sched, err := parser.Parse(cronStr)
		if err != nil {
			continue
		}

		due := false
		if strings.TrimSpace(p.RootNextRotationAt) == "" {
			due = true
		} else if t, err := time.Parse(time.RFC3339, p.RootNextRotationAt); err == nil {
			due = !now.Before(t)
		} else {
			due = true
		}
		if !due {
			continue
		}

		newPw, err := randomPassword(24)
		if err != nil {
			b.Logger().Warn("root rotation: random password", "config", cfgName, "error", err)
			continue
		}

		osLower := normalizeOS(p.OS)
		switch osLower {
		case "linux":
			port := p.effectivePort(defaults, osLower)
			// Change admin user's own password.
			err = rotateLinuxPassword(p.Host, port, p.AdminUsername, p.AdminPassword, p.AdminPrivateKey, p.AdminPrivateKeyPassphrase, p.SudoPassword, p.AdminUsername, newPw)
		case "windows":
			port := p.effectivePort(defaults, osLower)
			https := p.WinRMUseHTTPS
			insecure := p.WinRMSkipTLSVerify
			auth := p.effectiveWinRMAuth(defaults)
			err = rotateWindowsPassword(ctx, auth, p.Host, port, https, insecure, p.AdminUsername, p.AdminPassword, p.AdminUsername, newPw)
		default:
			continue
		}

		if err != nil {
			b.Logger().Warn("root rotation failed", "config", cfgName, "os", osLower, "error", err)
			continue
		}

		p.AdminPassword = newPw
		p.RootLastRotatedAt = now.Format(time.RFC3339)
		p.RootNextRotationAt = sched.Next(now).Format(time.RFC3339)
		if err := putConfigProfile(ctx, s, cfgName, p); err != nil {
			b.Logger().Warn("root rotation: persist config", "config", cfgName, "error", err)
		}
	}

	return nil
}

