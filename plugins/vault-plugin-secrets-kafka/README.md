# vault-plugin-secrets-kafka

Korean documentation (한국어): [`README.ko.md`](./README.ko.md)

A HashiCorp Vault **secrets engine plugin** for Kafka authentication.

## Vault paths (high-level)

- `kafka/config`: connection/admin bootstrap configuration
- `kafka/roles/<name>`: **dynamic** role definition (SCRAM issuance, TTLs, optional ACL hints)
- `kafka/creds/<role>`: issue **dynamic** credentials (returns `lease_id` + `data`)
- `kafka/static-roles/<name>`: **static** role definition (stored bundle; SCRAM requires `rotation_cron`)
- `kafka/static-creds/<role>`: return **static** credential bundle (returns `lease_id` + `data`)

## What it does

### Dynamic SCRAM (v1)

When you read `kafka/creds/<role>`, the plugin issues a short-lived SCRAM username/password and (optionally) creates ACLs. On lease revoke/expiry, the plugin invalidates credentials (password rotation + deletion best-effort).

### Static bundles (foundation)

The role model supports static mode for distributing pre-provisioned auth material (e.g., SASL/PLAIN, mTLS). For SASL/SCRAM static roles, password rotation is driven only by `rotation_cron` (not by Vault TTL on `static-creds`). The local validation UI is structured to display/compare credential profiles; the primary working path is dynamic SCRAM.

## Requirements

- Go (see `go.mod` under this plugin; build uses the standard Go toolchain)
- Vault (plugin support; tested with Vault `1.21` in the local stack)
- Kafka (tested with `apache/kafka:4.2.0` in the local stack)

## Build

From this directory:

```bash
cd plugins/vault-plugin-secrets-kafka
make build
```

`make build` produces a **native** binary for the machine running `vault` (required when Vault runs on macOS or Windows). If you previously saw `exec format error` from `fork/exec .../vault-plugin-secrets-kafka`, the plugin was almost certainly built for the wrong OS/arch (for example Linux while Vault runs on the host).

For the **Docker Compose** stack, build a Linux plugin that matches your Docker architecture (usually the same as `go env GOARCH` on the build machine):

```bash
make build-linux
```

If you prefer building directly with Go (example):

```bash
go build -o dist/vault-plugin-secrets-kafka ./cmd/vault-plugin-secrets-kafka
```

## Register & enable (example)

Run Vault in dev mode and point `-dev-plugin-dir` to the directory containing the plugin binary:

```bash
vault server -dev \
  -dev-root-token-id=root \
  -dev-listen-address=0.0.0.0:8200 \
  -dev-plugin-dir=./dist
```

Register the plugin and enable the secrets engine:

```bash
PLUGIN=$(realpath ./dist/vault-plugin-secrets-kafka)
SHA256=$(shasum -a 256 "$PLUGIN" | awk '{print $1}')
export VAULT_TOKEN=root
export VAULT_ADDR=http://localhost:8200
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-kafka
vault secrets enable -path=kafka -plugin-name=vault-plugin-secrets-kafka plugin
```

## Configure

### `kafka/config`

Configure bootstrap addresses for **clients** (issued credential bundles use `bootstrap_servers`) and for **admin** operations (SCRAM user lifecycle, ACLs, static rotation). You also need a Vault token with permission to write `kafka/config`; this path does **not** authenticate to Kafka by itself—it only stores settings the plugin uses when it connects later.

| Field | Description |
|---|---|
| `bootstrap_servers` | Brokers for application clients (returned in creds). |
| `admin_bootstrap_servers` | Brokers for the plugin’s Admin API client (defaults to `bootstrap_servers` if empty). |
| `security_protocol` | Optional. For SASL admin (typical in production), set `SASL_PLAINTEXT` or `SASL_SSL` and set `admin_username` / `admin_password`. |
| `sasl_mechanism` | e.g. `SCRAM-SHA-256` when using SASL for the admin client. |
| `admin_username` / `admin_password` | Kafka identity the plugin uses for **admin** RPCs. Required when `security_protocol` is `SASL_PLAINTEXT` or `SASL_SSL` (see plugin code). |

**Production-oriented Kafka** usually requires authentication for both client and admin traffic. Grant the admin principal only what it needs (create/delete SCRAM users, ACLs). The Docker Compose example creates a dedicated SCRAM user `vault_admin`, registers it as a **super user** in `server.properties` for simplicity, and passes the same password to Vault via `VAULT_KAFKA_ADMIN_PASSWORD` so `kafka/config` can use SASL for admin calls. Replace this pattern with your org’s least-privilege model in real deployments.

If you run the local Kafka stack and talk to it from the **host** (mapped ports `19092` / `19093`):

```bash
cd examples/docker-compose
docker compose up -d kafka kafka-init
```

```bash
vault write kafka/config \
  bootstrap_servers="localhost:19093" \
  admin_bootstrap_servers="localhost:19093" \
  security_protocol="SASL_PLAINTEXT" \
  sasl_mechanism="SCRAM-SHA-256" \
  admin_username="vault_admin" \
  admin_password="vault-admin-demo-secret"
```

Use the same `admin_password` you set in `VAULT_KAFKA_ADMIN_PASSWORD` when starting Compose (default shown above). For an unsecured admin listener (lab only), some deployments use PLAINTEXT admin and omit SASL fields; the plugin then connects without `admin_username` / `admin_password` only when `security_protocol` is not SASL.

### Dynamic roles: `kafka/roles/<name>`

Dynamic roles define how **short-lived SCRAM** credentials are issued on `kafka/creds/<role>`.

#### Dynamic role fields

| Field | Type | Required | Example | Notes |
|---|---:|:---:|---|---|
| `name` | string | yes | `ci` | Role name (also used in the path `kafka/roles/<name>`). |
| `scram_mechanism` | string | no | `SCRAM-SHA-256` | `SCRAM-SHA-256` or `SCRAM-SHA-512`. Default: `SCRAM-SHA-256`. |
| `topic` | string | no | `test-topic` | Optional v1 minimal ACL hint (topic read/write/describe). |
| `group_prefix` | string | no | `vaulttest-` | Optional v1 minimal ACL hint (consumer group read/describe with **prefixed** resource pattern). |
| `ttl` | seconds | no | `600` | Default lease TTL for `kafka/creds/<role>`. |
| `max_ttl` | seconds | no | `3600` | Upper bound for TTL (including overrides). Must be `>= ttl`. |

#### Dynamic role example (SCRAM)

```bash
vault write kafka/roles/ci \
  name="ci" \
  scram_mechanism="SCRAM-SHA-256" \
  topic="test-topic" \
  group_prefix="vaulttest-" \
  ttl=600 \
  max_ttl=3600
```

## Read credentials

Issue **dynamic** credentials by reading:

```bash
vault read kafka/creds/ci
```

Output example:
```log
Key                  Value
---                  -----
lease_id             kafka/creds/ci/4MhUj69WMxASYEHxZMA1b43v
lease_duration       10m
lease_renewable      false
auth_type            scram
bootstrap_servers    localhost:19093
group_prefix         vaulttest-
mode                 dynamic
password             NjVlkTkrkyqpbxWyEBQjfOH5zqqREvwS
sasl_mechanism       SCRAM-SHA-256
security_protocol    SASL_PLAINTEXT
topic                test-topic
username             vault_ci_6A3cP2lF
```

The response includes a `lease_id` and `data` (e.g., `bootstrap_servers`, `username`, `password`, and protocol/mechanism fields). Revoke with:

```bash
vault lease revoke <lease_id>
```

You can optionally override TTL per request (must not exceed `max_ttl`):

```bash
vault read kafka/creds/ci ttl=120
```

## Static roles and credentials

Static roles are configured under `static-roles/` and read via `static-creds/`. Static roles **do not** create/delete Kafka users. For `auth_type=scram`, you **must** set `rotation_cron`; the scheduler rotates the Kafka password and the stored bundle on that schedule (UTC). **Password rotation timing is independent of Vault lease TTL** on `static-creds` reads.

`vault read kafka/static-creds/...` still returns a Vault lease, but its TTL and max TTL are **fixed by the plugin** (defaults: 3600s TTL, 86400s max TTL); they are not configurable per static role.

### Static role fields

| Field | Type | Required | Example | Notes |
|---|---:|:---:|---|---|
| `name` | string | yes | `app_plain` | Role name (also used in the path `kafka/static-roles/<name>`). |
| `auth_type` | string | yes | `plain` / `scram` / `mtls` | Static bundle type. Scheduled rotation applies only to `scram` (v1). |
| `scram_mechanism` | string | scram only | `SCRAM-SHA-256` | `SCRAM-SHA-256` or `SCRAM-SHA-512`. |
| `static_username` | string | depends | `my-user` | For `plain` and `scram` bundles. For `scram`, this user must already exist in Kafka. |
| `static_password` | string | depends | `...` | For `plain` and `scram` bundles. For `scram`, the scheduler updates this value after each rotation. |
| `static_props` | map | no | `security.protocol=SASL_SSL` | Extra client properties to return (static bundle). |
| `rotation_cron` | string | **required if `scram`** | `*/5 * * * *` | Cron (UTC) for password rotation. Ignored for `plain` / `mtls`. |

### Static role example (plain)

Static roles return a “credential bundle” without creating/deleting Kafka users. Useful when credentials are managed externally.

```bash
vault write kafka/static-roles/app_plain \
  name="app_plain" \
  auth_type="plain" \
  static_username="my-user" \
  static_password="my-pass" \
  static_props="security.protocol=SASL_PLAINTEXT"
```

### Static role example (SCRAM + rotation)

Prerequisites:

- The Kafka cluster must have SCRAM enabled (the compose example enables SCRAM-SHA-256).
- The user in `static_username` must already exist in Kafka (or be created out-of-band once). Rotation updates the password for that user on `rotation_cron`.

```bash
vault write kafka/static-roles/app_scram \
  name="app_scram" \
  auth_type="scram" \
  scram_mechanism="SCRAM-SHA-256" \
  static_username="my-scram-user" \
  static_password="initial-password" \
  rotation_cron="*/1 * * * *"
```

Read the static bundle:

```bash
vault read kafka/static-creds/app_scram
```

The response includes `last_rotated_at` and `next_rotation_at` (RFC3339) for `scram` roles.

## Local end-to-end test (Docker Compose)

This repository includes an end-to-end validation stack (Vault + Kafka + Spring Boot UI) intended for verification.

- Path: `examples/docker-compose/`
- Start:

```bash
cd plugins/vault-plugin-secrets-kafka
make build-linux
cp -f dist/vault-plugin-secrets-kafka examples/docker-compose/vault/plugins/vault-plugin-secrets-kafka

cd examples/docker-compose
docker compose up -d --build
```

Open the validation UI:

- `http://localhost:8080`

The stack includes:

- **Vault** (`hashicorp/vault:1.21`) in dev mode with `-dev-plugin-dir=/vault/plugins`
- **Kafka** (`apache/kafka:4.2.0`) configured via a mounted `server.properties`
- **vault-init**: registers/enables the plugin, writes `kafka/config`, and creates roles (e.g. `ci`, `ci_fast`)
- **spring-app**: uses **Spring Cloud Vault (Config Data) + token** via configtree-mounted `token` (minted once by `vault-init` using AppRole)

**Kafka admin identity (compose example):** `kafka-init` creates a SCRAM user `vault_admin` (password from `VAULT_KAFKA_ADMIN_PASSWORD`, default `vault-admin-demo-secret`) and the broker lists `User:vault_admin` under `super.users` so Admin API calls used by the plugin (SCRAM upsert/delete, ACLs) succeed without hand-tuned ACLs. `vault-init` writes the same password into `kafka/config` as `admin_username` / `admin_password` with `SASL_PLAINTEXT` + `SCRAM-SHA-256`. Override the env var for a stronger secret in real use; prefer least-privilege ACLs instead of `super.users` in production.

## Notes

- OAuth/Kerberos flows are **out of v1 scope** (typically rely on external IdP/KDC).

