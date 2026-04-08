package plugin

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/robfig/cron/v3"
)

const (
	backendHelp = `
The Machine secrets engine stores and rotates passwords for local OS accounts on
remote hosts. v1 supports static roles only: one role maps to one host and one
username, with scheduled rotation over SSH (Linux) or WinRM (Windows).

Dynamic provisioning of ephemeral local users is intentionally out of scope for v1.
`

	machineStaticSecretType = "machine_static_credentials"
	runningVersion          = "v0.1.0"
)

func Factory(ctx context.Context, conf *logical.BackendConfig) (logical.Backend, error) {
	b := &backend{}
	b.Backend = &framework.Backend{
		Help:           strings.TrimSpace(backendHelp),
		BackendType:    logical.TypeLogical,
		RunningVersion: runningVersion,
		Paths:          b.paths(),
		Secrets:        b.secrets(),
		Clean:          b.cleanup,
	}
	if err := b.Setup(ctx, conf); err != nil {
		return nil, err
	}
	b.startRotationScheduler(ctx, conf)
	return b, nil
}

type backend struct {
	*framework.Backend

	rotationOnce sync.Once
	rotationMu   sync.Mutex
	cron         *cron.Cron
	rotationCtx  context.Context
	rotationStop context.CancelFunc
}

func (b *backend) paths() []*framework.Path {
	return []*framework.Path{
		// Legacy engine defaults (kept for compatibility; prefer config/<name>).
		pathConfig(b),
		pathConfigProfiles(b),
		pathConfigProfileList(b),
		pathStaticRoles(b),
		pathStaticRoleList(b),
		pathStaticCreds(b),
	}
}

func (b *backend) secrets() []*framework.Secret {
	return []*framework.Secret{
		{
			Type:   machineStaticSecretType,
			Revoke: b.revokeMachineStaticSecret,
		},
	}
}

func (b *backend) startRotationScheduler(ctx context.Context, conf *logical.BackendConfig) {
	b.rotationOnce.Do(func() {
		b.rotationCtx, b.rotationStop = context.WithCancel(context.Background())

		b.cron = cron.New(cron.WithLocation(time.UTC))
		_, _ = b.cron.AddFunc("@every 15s", func() {
			b.rotationMu.Lock()
			defer b.rotationMu.Unlock()
			_ = b.rotateDueRootConfigs(b.rotationCtx, conf.StorageView)
			_ = b.rotateDueStaticRoles(b.rotationCtx, conf.StorageView)
		})
		b.cron.Start()
	})
}

func (b *backend) cleanup(ctx context.Context) {
	_ = ctx
	b.rotationMu.Lock()
	defer b.rotationMu.Unlock()

	if b.rotationStop != nil {
		b.rotationStop()
		b.rotationStop = nil
	}
	b.rotationCtx = nil

	if b.cron == nil {
		return
	}
	stopCtx := b.cron.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(2 * time.Second):
	}
	b.cron = nil
}
