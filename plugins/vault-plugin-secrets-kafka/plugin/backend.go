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
The Kafka secrets engine issues Kafka client credentials. v1 supports:

- Dynamic: SASL/SCRAM user+password issuance with optional Kafka ACL creation.
- Static: Bundle distribution for pre-provisioned credentials (e.g. SASL/PLAIN or mTLS via external PKI).

OAuth/OIDC and Kerberos are out of scope for v1 because credentials are typically issued by external IdP/KDC.
`

	scramSecretType = "kafka_scram_credentials"
	runningVersion  = "v0.1.0"
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
		pathConfig(b),
		pathDynamicRoles(b),
		pathDynamicRoleList(b),
		pathStaticRoles(b),
		pathStaticRoleList(b),
		pathCreds(b),
		pathStaticCreds(b),
	}
}

func (b *backend) secrets() []*framework.Secret {
	return []*framework.Secret{
		{
			Type:   scramSecretType,
			Revoke: b.revokeScramSecret,
		},
	}
}

func (b *backend) startRotationScheduler(ctx context.Context, conf *logical.BackendConfig) {
	b.rotationOnce.Do(func() {
		// Do NOT derive from Factory's ctx, which may be request-scoped and
		// cancelled shortly after initialization. The scheduler should live for
		// the lifetime of the backend and be stopped explicitly in cleanup().
		b.rotationCtx, b.rotationStop = context.WithCancel(context.Background())

		// Tick frequently; due evaluation is per-role based on cron schedule.
		b.cron = cron.New(cron.WithLocation(time.UTC))
		_, _ = b.cron.AddFunc("@every 15s", func() {
			b.rotationMu.Lock()
			defer b.rotationMu.Unlock()
			_ = b.rotateDueStaticScramRoles(b.rotationCtx, conf.StorageView)
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
	// Stop() signals the scheduler goroutine and returns a context that is
	// closed when running jobs have completed.
	stopCtx := b.cron.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(2 * time.Second):
		// Best-effort: don't block backend unload indefinitely.
	}
	b.cron = nil
}
