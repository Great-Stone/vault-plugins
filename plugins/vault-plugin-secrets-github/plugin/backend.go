package plugin

import (
	"context"
	"net/http"
	"strings"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const (
	backendHelp = `
The GitHub secrets engine issues GitHub App installation access tokens using the
GitHub REST API. Configure an App ID and PEM private key, define roles bound to
installation IDs, then read credentials from a role.
`

	installationTokenSecretType = "github_installation_token"
	runningVersion              = "v0.1.0"
)

// Factory returns a configured logical backend.
func Factory(ctx context.Context, conf *logical.BackendConfig) (logical.Backend, error) {
	b := &backend{}
	b.Backend = &framework.Backend{
		Help:            strings.TrimSpace(backendHelp),
		BackendType:     logical.TypeLogical,
		RunningVersion:  runningVersion,
		Paths:   b.paths(),
		Secrets: b.secrets(),
	}
	if err := b.Setup(ctx, conf); err != nil {
		return nil, err
	}
	return b, nil
}

type backend struct {
	*framework.Backend
	// httpClient is optional (defaults to http.DefaultClient). Tests set this to hit httptest.Server.
	httpClient *http.Client
}

func (b *backend) paths() []*framework.Path {
	return []*framework.Path{
		pathConfig(b),
		pathRoles(b),
		pathRoleList(b),
		pathCreds(b),
	}
}

func (b *backend) getHTTPClient() *http.Client {
	if b.httpClient != nil {
		return b.httpClient
	}
	return http.DefaultClient
}

func (b *backend) secrets() []*framework.Secret {
	return []*framework.Secret{
		{
			Type:   installationTokenSecretType,
			Revoke: b.revokeInstallationSecret,
		},
	}
}

func (b *backend) revokeInstallationSecret(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	if req.Secret == nil || req.Secret.InternalData == nil {
		return nil, nil
	}
	tok, _ := req.Secret.InternalData["token"].(string)
	if tok == "" {
		b.Logger().Warn("revoke: lease has no token in InternalData; skipping GitHub revoke (legacy lease)")
		return nil, nil
	}
	cfg, err := b.loadConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if err := revokeInstallationAccessToken(ctx, b.getHTTPClient(), cfg.apiBase(), tok); err != nil {
		return nil, err
	}
	return nil, nil
}
