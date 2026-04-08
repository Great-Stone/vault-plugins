package plugin

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/vault/sdk/logical"
	"github.com/robfig/cron/v3"
)

func (b *backend) rotateDueStaticRoles(ctx context.Context, s logical.Storage) error {
	defaults, _ := loadMachineConfig(ctx, s)

	names, err := s.List(ctx, staticRoleStoragePrefix)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}

	now := time.Now().UTC()
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	for _, name := range names {
		roleName := strings.TrimSuffix(name, "/")
		if strings.TrimSpace(roleName) == "" {
			continue
		}
		r, err := getStaticRole(ctx, s, roleName)
		if err != nil || r == nil {
			continue
		}
		// Merge in config/<name> if referenced.
		var prof *machineConfigProfile
		if strings.TrimSpace(r.ConfigName) != "" {
			prof, _ = getConfigProfile(ctx, s, strings.TrimSpace(r.ConfigName))
		}
		rEff := r.roleEffective(prof)

		if strings.TrimSpace(r.RotationCron) == "" {
			continue
		}
		if strings.TrimSpace(rEff.Host) == "" || strings.TrimSpace(rEff.Username) == "" {
			continue
		}
		if strings.TrimSpace(rEff.AdminUsername) == "" {
			continue
		}
		if strings.TrimSpace(rEff.AdminPassword) == "" && strings.TrimSpace(rEff.AdminPrivateKey) == "" {
			continue
		}

		osLower := normalizeOS(rEff.OS)
		if osLower != "linux" && osLower != "windows" {
			continue
		}

		sched, err := parser.Parse(strings.TrimSpace(r.RotationCron))
		if err != nil {
			continue
		}

		due := false
		if strings.TrimSpace(r.NextRotationAt) == "" {
			due = true
		} else if t, err := time.Parse(time.RFC3339, r.NextRotationAt); err == nil {
			due = !now.Before(t)
		} else {
			due = true
		}
		if !due {
			continue
		}

		newPw, err := randomPassword(24)
		if err != nil {
			b.Logger().Warn("rotation: random password", "role", roleName, "error", err)
			continue
		}

		switch osLower {
		case "linux":
			port := rEff.effectivePort(defaults, osLower)
			if prof != nil {
				port = prof.effectivePort(defaults, osLower)
				if rEff.Port > 0 {
					port = rEff.Port
				}
			}
			err = rotateLinuxPassword(rEff.Host, port, rEff.AdminUsername, rEff.AdminPassword, rEff.AdminPrivateKey, rEff.AdminPrivateKeyPassphrase, rEff.SudoPassword, rEff.Username, newPw)
		case "windows":
			port := rEff.effectivePort(defaults, osLower)
			https := defaults != nil && defaults.WinRMUseHTTPS
			insecure := (defaults != nil && defaults.WinRMSkipTLSVerify) || rEff.WinRMInsecure
			if prof != nil {
				if prof.Port > 0 {
					port = prof.Port
				}
				https = prof.WinRMUseHTTPS
				insecure = prof.WinRMSkipTLSVerify || rEff.WinRMInsecure
			}
			auth := "basic"
			if strings.TrimSpace(rEff.WinRMAuth) != "" {
				auth = normalizeOS(rEff.WinRMAuth)
			} else if prof != nil {
				auth = prof.effectiveWinRMAuth(defaults)
			} else if defaults != nil {
				auth = defaults.effectiveWinRMAuth()
			}
			err = rotateWindowsPassword(ctx, auth, rEff.Host, port, https, insecure, rEff.AdminUsername, rEff.AdminPassword, rEff.Username, newPw)
		default:
			continue
		}

		if err != nil {
			b.Logger().Warn("rotation failed", "role", roleName, "os", osLower, "error", err)
			continue
		}

		r.Password = newPw
		r.LastRotatedAt = now.Format(time.RFC3339)
		r.NextRotationAt = sched.Next(now).Format(time.RFC3339)
		if err := putStaticRole(ctx, s, roleName, r); err != nil {
			b.Logger().Warn("rotation: persist role", "role", roleName, "error", err)
		}
	}

	return nil
}
