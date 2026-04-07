package plugin

import (
	"context"
	"strings"

	"github.com/IBM/sarama"
	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func (b *backend) revokeScramSecret(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	if req.Secret == nil || req.Secret.InternalData == nil {
		return nil, nil
	}
	if mode, _ := req.Secret.InternalData["mode"].(string); mode != "dynamic" {
		// Static bundles have nothing to revoke remotely.
		return nil, nil
	}

	username, _ := req.Secret.InternalData["username"].(string)
	if strings.TrimSpace(username) == "" {
		b.Logger().Warn("revoke: missing username in InternalData; skipping remote revoke")
		return nil, nil
	}
	mechStr, _ := req.Secret.InternalData["scram_mechanism"].(string)
	mechStr = strings.ToUpper(strings.TrimSpace(mechStr))

	cfg, err := b.loadConfig(ctx, req.Storage)
	if err != nil {
		return nil, err
	}

	admin, err := newAdminClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer admin.Close()

	var mech sarama.ScramMechanismType
	switch mechStr {
	case "SCRAM-SHA-256":
		mech = sarama.SCRAM_MECHANISM_SHA_256
	case "SCRAM-SHA-512":
		mech = sarama.SCRAM_MECHANISM_SHA_512
	default:
		// Unknown mechanism; best-effort delete both common ones.
		// First rotate password (best-effort) so the original creds become invalid.
		pw, _ := randomPassword(24)
		_, _ = admin.admin.UpsertUserScramCredentials([]sarama.AlterUserScramCredentialsUpsert{
			{Name: username, Mechanism: sarama.SCRAM_MECHANISM_SHA_256, Iterations: 4096, Password: []byte(pw)},
			{Name: username, Mechanism: sarama.SCRAM_MECHANISM_SHA_512, Iterations: 4096, Password: []byte(pw)},
		})
		_, _ = admin.admin.DeleteUserScramCredentials([]sarama.AlterUserScramCredentialsDelete{{Name: username, Mechanism: sarama.SCRAM_MECHANISM_SHA_256}})
		_, _ = admin.admin.DeleteUserScramCredentials([]sarama.AlterUserScramCredentialsDelete{{Name: username, Mechanism: sarama.SCRAM_MECHANISM_SHA_512}})
		return nil, nil
	}

	// Revoke should make the originally issued credential unusable.
	// Kafka SCRAM deletion propagation can vary, so we rotate the password first (best-effort),
	// then delete the mechanism credential.
	pw, err := randomPassword(24)
	if err == nil {
		_, _ = admin.admin.UpsertUserScramCredentials([]sarama.AlterUserScramCredentialsUpsert{
			{Name: username, Mechanism: mech, Iterations: 4096, Password: []byte(pw)},
		})
	}

	_, _ = admin.admin.DeleteUserScramCredentials([]sarama.AlterUserScramCredentialsDelete{
		{Name: username, Mechanism: mech},
	})
	return nil, nil
}
