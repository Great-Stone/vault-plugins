package plugin

import (
	"hash"

	"github.com/xdg-go/scram"
)

type xdgScramClient struct {
	hashGen func() hash.Hash
	conv    *scram.ClientConversation
}

func newXdgScramClient(h func() hash.Hash) *xdgScramClient {
	return &xdgScramClient{hashGen: h}
}

func (c *xdgScramClient) Begin(userName, password, authzID string) error {
	client, err := scram.HashGeneratorFcn(c.hashGen).NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	c.conv = client.NewConversation()
	return nil
}

func (c *xdgScramClient) Step(challenge string) (string, error) {
	return c.conv.Step(challenge)
}

func (c *xdgScramClient) Done() bool {
	return c.conv.Done()
}

