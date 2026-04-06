package plugin

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const defaultGitHubAPIBase = "https://api.github.com"

// githubConfig holds persisted plugin configuration.
type githubConfig struct {
	AppID      int64  `json:"app_id"`
	PrivateKey string `json:"private_key"`
	BaseURL    string `json:"base_url,omitempty"`
}

func (c *githubConfig) apiBase() string {
	if c == nil {
		return defaultGitHubAPIBase
	}
	s := strings.TrimSpace(c.BaseURL)
	if s == "" {
		return defaultGitHubAPIBase
	}
	return strings.TrimRight(s, "/")
}

// roleEntry defines how installation tokens are minted for a role name.
type roleEntry struct {
	InstallationID int64             `json:"installation_id"`
	Repositories   []string          `json:"repositories,omitempty"`
	RepositoryIDs  []int64           `json:"repository_ids,omitempty"`
	Permissions    map[string]string `json:"permissions,omitempty"`
	TTL            int               `json:"ttl"`
	MaxTTL         int               `json:"max_ttl"`
}

func parseRSAPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("private_key: no PEM block found")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS1 private key: %w", err)
		}
		return k, nil
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS8 private key: %w", err)
		}
		rsaKey, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private_key is not RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("private_key: unsupported PEM type %q (expected RSA PRIVATE KEY or PRIVATE KEY)", block.Type)
	}
}

func (c *githubConfig) mintJWT() (string, error) {
	if c.AppID <= 0 {
		return "", fmt.Errorf("app_id must be positive")
	}
	key, err := parseRSAPrivateKey(c.PrivateKey)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": c.AppID,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(key)
}

// installationTokenResponse mirrors GitHub's JSON for POST .../access_tokens.
type installationTokenResponse struct {
	Token     string            `json:"token"`
	ExpiresAt string            `json:"expires_at"`
	Permissions map[string]string `json:"permissions,omitempty"`
}

func (c *githubConfig) createInstallationToken(ctx context.Context, httpClient *http.Client, installationID int64, body installationTokenRequest) (*installationTokenResponse, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	body.Repositories = normalizeRepoNamesForGitHubInstallation(body.Repositories)
	jwtStr, err := c.mintJWT()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiBase(), installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vault-plugin-secrets-github")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github api %s: %s: %s", url, resp.Status, strings.TrimSpace(string(raw)))
	}
	var out installationTokenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.Token == "" {
		return nil, fmt.Errorf("github api: empty token in response")
	}
	return &out, nil
}

// installationTokenRequest is the JSON body for creating an installation token.
type installationTokenRequest struct {
	Repositories   []string          `json:"repositories,omitempty"`
	RepositoryIDs  []int64           `json:"repository_ids,omitempty"`
	Permissions    map[string]string `json:"permissions,omitempty"`
}

// normalizeRepoNamesForGitHubInstallation maps "owner/repo" to "repo". GitHub's REST examples
// use short names only (e.g. "Hello-World"); full names can yield 422 for installation tokens.
// revokeInstallationAccessToken calls DELETE /installation/token with the installation token as Bearer.
// See https://docs.github.com/en/rest/apps/installations#revoke-an-installation-access-token
func revokeInstallationAccessToken(ctx context.Context, httpClient *http.Client, apiBase, installationToken string) error {
	if strings.TrimSpace(installationToken) == "" {
		return nil
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	url := strings.TrimRight(strings.TrimSpace(apiBase), "/") + "/installation/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+installationToken)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "vault-plugin-secrets-github")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusNotFound:
		// Already expired or revoked.
		return nil
	default:
		return fmt.Errorf("github revoke installation token %s: %s: %s", url, resp.Status, strings.TrimSpace(string(raw)))
	}
}

func normalizeRepoNamesForGitHubInstallation(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if i := strings.LastIndex(s, "/"); i >= 0 {
			s = s[i+1:]
		}
		out = append(out, s)
	}
	return out
}
