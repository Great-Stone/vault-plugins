# vault-plugin-secrets-github

Korean documentation (한국어): [`README.ko.md`](./README.ko.md)

A Vault **secrets engine plugin** that mints **GitHub App installation access tokens** using GitHub App credentials (App ID + PEM private key).

## Constraint: fine-grained PATs and the REST API

GitHub does **not** provide a REST endpoint that generates a new fine-grained personal access token **string**. Org-level endpoints such as `POST /orgs/{org}/personal-access-tokens` are for **managing/revoking** existing tokens, not minting secrets.

If you need automated, dynamic issuance, GitHub’s recommended approach is **GitHub Apps + installation access tokens**. This plugin implements that flow. If you must use PATs, create them in the GitHub UI and store them in Vault (e.g., KV) as a static secret.

## Requirements

- Go 1.23+
- Vault 1.15+ (plugin multiplexing; compatible with SDK `v0.5.4+`)

## Build

```bash
cd plugins/vault-plugin-secrets-github
make build
# output: bin/vault-plugin-secrets-github
```

Or:

```bash
go build -o bin/vault-plugin-secrets-github ./cmd/vault-plugin-secrets-github
```

## Register and enable (example)

Run Vault in dev mode and point `-dev-plugin-dir` to the directory containing the plugin binary.

```bash
vault server -dev \
  -dev-root-token-id=root \
  -dev-listen-address=0.0.0.0:8200 \
  -dev-plugin-dir=./bin
```

Compute SHA256 and register in the plugin catalog:

```bash
PLUGIN=$(realpath ./bin/vault-plugin-secrets-github)
SHA256=$(shasum -a 256 "$PLUGIN" | awk '{print $1}')
VAULT_TOKEN=root
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-github
vault secrets enable -path=github -plugin-name=vault-plugin-secrets-github plugin
```

## Configuration

### `github/config`

| Field | Description |
|---|---|
| `app_id` | GitHub App ID (required) |
| `private_key` | App PEM RSA private key (required) |
| `base_url` | For GitHub Enterprise Server: API base (e.g., `https://github.example.com/api/v3`). Defaults to `https://api.github.com` |

#### Preparing GitHub (UI / docs)

You obtain `app_id` and `private_key` by registering a GitHub App and generating a private key. There is no Vault-specific UI for this—follow GitHub’s documentation:

- About creating GitHub Apps: `https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps`
- Registering a GitHub App: `https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app`
- Managing private keys for GitHub Apps: `https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/managing-private-keys-for-github-apps`
- Installing your own GitHub App: `https://docs.github.com/en/apps/creating-github-apps/installing-github-apps/installing-your-own-github-app`

Using a local Vault instance does **not** prevent registering GitHub Apps. The GitHub App is created on GitHub (GitHub.com or GHES), and Vault only makes **outbound** REST calls to GitHub to mint installation tokens.

#### Example: write config

```bash
vault write github/config \
  app_id=123456 \
  private_key=@/path/to/app-private-key.pem \
  base_url="https://api.github.com"
```

### Roles: `github/roles/<name>`

At minimum, you must provide `installation_id` (after installing the GitHub App).

#### How to find `installation_id`

- GitHub.com → **Settings** (personal) or **Org settings** → **Developer settings** → **GitHub Apps** → your app → **Install App** / **Configure**
- The URL often contains `.../installations/<number>` which is the `installation_id`

| Field | Description |
|---|---|
| `installation_id` | Installation ID (required). Issued by GitHub at install time and cannot be invented. |
| `repositories` | Optional. Comma-separated repo names (e.g., `owner/repo-a,owner/repo-b`). The GitHub API expects short names; providing `owner/repo` is OK and the plugin will send short names. |
| `repository_ids` | Optional. Comma-separated numeric repo IDs. |
| `permissions` | Optional. A permission map for the token (keys/values per GitHub REST spec). |
| `ttl` / `max_ttl` | Default TTL and max TTL (seconds). |

#### Example: role with repos and permissions

```bash
vault write github/roles/ci \
  installation_id=87654321 \
  repositories="my-org/my-repo-a,my-org/my-repo-b" \
  permissions=contents=write \
  ttl=3600 \
  max_ttl=7200
```

#### Multiple `permissions` with Vault CLI

```bash
vault write github/roles/ci \
  installation_id=12345678 \
  ttl=3600 \
  max_ttl=7200 \
  permissions=contents=read \
  permissions=issues=write \
  permissions=pull_requests=write
```

#### HTTP API example (single JSON object)

```bash
curl -H "X-Vault-Token: ..." -H "Content-Type: application/json" \
  -X POST "$VAULT_ADDR/v1/github/roles/ci" \
  -d '{
    "installation_id": 12345678,
    "ttl": 3600,
    "max_ttl": 7200,
    "permissions": {
      "contents": "read",
      "issues": "write",
      "pull_requests": "write"
    }
  }'
```

## Read credentials

```bash
vault read github/creds/ci
```

The response includes fields such as `token`, `expires_at`, `installation_id`, and `permissions`. On `vault lease revoke <lease_id>`, the plugin calls GitHub’s token revocation endpoint to invalidate the `ghs_...` token (older leases without a token in storage may skip the GitHub call).

Example output:

```log
Key                Value
---                -----
lease_id           github/creds/ci/eCIXEkJKu1oHJIrWyHHpFKKr
lease_duration     59m59s
lease_renewable    false
expires_at         2026-04-06T01:43:33Z
installation_id    87654321
permissions        map[contents:write metadata:read]
token              ghs_...
```

Verify the token by calling the GitHub API:

```bash
curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "https://api.github.com/repos/my-org/my-repo" | jq .
```

Using an invalid/expired token returns `401 Bad credentials`.