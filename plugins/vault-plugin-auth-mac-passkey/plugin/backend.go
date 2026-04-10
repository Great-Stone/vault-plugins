package plugin

import (
	"context"
	"strings"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const (
	backendHelp = `
The passkey auth method allows users to authenticate to Vault using WebAuthn
passkeys (e.g., macOS Touch ID). The Vault server validates WebAuthn assertions
and issues Vault tokens. A local client helper performs the on-device WebAuthn
ceremony and calls this auth method.
`

	runningVersion = "v0.1.0"
)

func Factory(ctx context.Context, conf *logical.BackendConfig) (logical.Backend, error) {
	b := &backend{}
	b.Backend = &framework.Backend{
		Help:           strings.TrimSpace(backendHelp),
		BackendType:    logical.TypeCredential,
		RunningVersion: runningVersion,
		Paths:          b.paths(),
		PathsSpecial: &logical.Paths{
			Unauthenticated: []string{
				"register/*",
				"login/*",
			},
		},
	}

	if err := b.Setup(ctx, conf); err != nil {
		return nil, err
	}
	return b, nil
}

type backend struct {
	*framework.Backend
}

func (b *backend) paths() []*framework.Path {
	return []*framework.Path{
		pathConfig(b),
		pathRole(b),
		pathRoleList(b),
		pathDebugUser(b),
		pathRegisterBegin(b),
		pathRegisterFinish(b),
		pathLoginBegin(b),
		pathLoginFinish(b),
	}
}
