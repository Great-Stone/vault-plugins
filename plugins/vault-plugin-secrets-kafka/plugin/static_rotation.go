package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/vault/sdk/logical"
	"github.com/IBM/sarama"
	"github.com/robfig/cron/v3"
)

const (
	staticRoleStoragePrefix  = "static-role/"
	dynamicRoleStoragePrefix = "role/"
)

func (b *backend) rotateDueStaticScramRoles(ctx context.Context, s logical.Storage) error {
	// Not configured yet: do nothing.
	cfg, err := b.loadConfig(ctx, s)
	if err != nil {
		return nil
	}

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
		if r.AuthType != authScram {
			continue
		}
		if !r.RotationEnabled || strings.TrimSpace(r.RotationCron) == "" {
			continue
		}
		if strings.TrimSpace(r.StaticUsername) == "" {
			continue
		}

		sched, err := parser.Parse(strings.TrimSpace(r.RotationCron))
		if err != nil {
			// Invalid cron: skip.
			continue
		}

		// Determine if a rotation is due.
		//
		// Important: if next_rotation_at is empty (initial state), rotate on the
		// first scan to avoid delaying the first password rotation by an
		// additional scheduler interval.
		due := false
		if strings.TrimSpace(r.NextRotationAt) == "" {
			due = true
		} else if t, err := time.Parse(time.RFC3339, r.NextRotationAt); err == nil {
			due = !now.Before(t)
		} else {
			// If the stored value is invalid, recover by attempting rotation now.
			due = true
		}

		if !due {
			continue
		}

		// Rotate: upsert SCRAM credential with new password.
		newPw, err := randomPassword(24)
		if err != nil {
			continue
		}

		mechStr := strings.ToUpper(strings.TrimSpace(r.ScramMechanism))
		if mechStr == "" {
			mechStr = "SCRAM-SHA-256"
		}
		var mech sarama.ScramMechanismType
		switch mechStr {
		case "SCRAM-SHA-256":
			mech = sarama.SCRAM_MECHANISM_SHA_256
		case "SCRAM-SHA-512":
			mech = sarama.SCRAM_MECHANISM_SHA_512
		default:
			continue
		}

		admin, err := newAdminClient(ctx, cfg)
		if err != nil {
			// If admin connection fails, skip this role and try others.
			continue
		}
		_, err = admin.admin.UpsertUserScramCredentials([]sarama.AlterUserScramCredentialsUpsert{
			{
				Name:       r.StaticUsername,
				Mechanism:  mech,
				Iterations: 4096,
				Password:   []byte(newPw),
			},
		})
		if err != nil {
			admin.Close()
			continue
		}
		admin.Close()

		r.StaticPassword = newPw
		r.LastRotatedAt = now.Format(time.RFC3339)
		r.NextRotationAt = sched.Next(now).Format(time.RFC3339)
		_ = putStaticRole(ctx, s, roleName, r)
	}

	return nil
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

