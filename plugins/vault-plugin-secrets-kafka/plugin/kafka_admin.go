package plugin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/IBM/sarama"
	xdgscram "github.com/xdg-go/scram"
)

type adminClient struct {
	admin sarama.ClusterAdmin
	conf  *sarama.Config
}

func newAdminClient(ctx context.Context, cfg *kafkaConfig) (*adminClient, error) {
	_ = ctx // sarama does not accept context on admin creation
	c := sarama.NewConfig()
	// KRaft requires controller operations; AlterUserScramCredentials requires >= 2.7.0.0
	c.Version = sarama.V3_7_0_0
	c.Net.DialTimeout = 10 * time.Second
	c.Net.ReadTimeout = 10 * time.Second
	c.Net.WriteTimeout = 10 * time.Second

	secProto := strings.ToUpper(strings.TrimSpace(cfg.SecurityProtocol))
	mech := strings.ToUpper(strings.TrimSpace(cfg.SaslMechanism))
	if secProto == "SASL_PLAINTEXT" || secProto == "SASL_SSL" {
		adminUser := strings.TrimSpace(cfg.AdminUsername)
		adminPass := strings.TrimSpace(cfg.AdminPassword)
		if adminUser == "" || adminPass == "" {
			return nil, fmt.Errorf("admin_username/admin_password required for %s", secProto)
		}
		if mech == "" {
			mech = "SCRAM-SHA-256"
		}
		c.Net.SASL.Enable = true
		c.Net.SASL.User = adminUser
		c.Net.SASL.Password = adminPass
		switch mech {
		case "SCRAM-SHA-256":
			c.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
			c.Net.SASL.SCRAMClientGeneratorFunc = scramClientGenerator(xdgscram.SHA256)
		case "SCRAM-SHA-512":
			c.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
			c.Net.SASL.SCRAMClientGeneratorFunc = scramClientGenerator(xdgscram.SHA512)
		default:
			return nil, fmt.Errorf("unsupported sasl_mechanism %q for admin", cfg.SaslMechanism)
		}
	}

	admin, err := sarama.NewClusterAdmin(strings.Split(cfg.effectiveAdminBootstrap(), ","), c)
	if err != nil {
		return nil, err
	}
	return &adminClient{admin: admin, conf: c}, nil
}

func (a *adminClient) Close() {
	if a != nil && a.admin != nil {
		_ = a.admin.Close()
	}
}

func randomPassword(bytesLen int) (string, error) {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// URL-safe, no padding.
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func scramClientGenerator(h func() hash.Hash) func() sarama.SCRAMClient {
	return func() sarama.SCRAMClient {
		return newXdgScramClient(h)
	}
}
