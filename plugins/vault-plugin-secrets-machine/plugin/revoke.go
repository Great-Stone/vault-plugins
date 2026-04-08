package plugin

import (
	"context"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func (b *backend) revokeMachineStaticSecret(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	_ = b
	_ = ctx
	_ = req
	// Static machine credentials are not revoked remotely; lease expiry is bookkeeping only.
	return nil, nil
}
