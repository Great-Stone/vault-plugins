# vault-plugin-secrets-kafka

Korean documentation (한국어): [`README.ko.md`](./README.ko.md)

A HashiCorp Vault **secrets engine plugin** for Kafka authentication.

## Vault paths (high-level)

- `kafka/config`: connection/admin bootstrap configuration
- `kafka/roles/<name>`: **dynamic** role definition (SCRAM issuance, TTLs, optional ACL hints)
- `kafka/creds/<role>`: issue **dynamic** credentials (returns `lease_id` + `data`)
- `kafka/static-roles/<name>`: **static** role definition (stored bundle + optional SCRAM rotation)
- `kafka/static-creds/<role>`: return **static** credential bundle (returns `lease_id` + `data`)

## What it does

### Dynamic SCRAM (v1)

When you read `kafka/creds/<role>`, the plugin issues a short-lived SCRAM username/password and (optionally) creates ACLs. On lease revoke/expiry, the plugin invalidates credentials (password rotation + deletion best-effort).

### Static bundles (foundation)

The role model supports static mode as a foundation for distributing pre-provisioned auth material (e.g., SASL/PLAIN, mTLS). The local validation UI/server are structured to display/compare credential profiles, but the primary working path is dynamic SCRAM.

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
VAULT_TOKEN=root
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-kafka
vault secrets enable -path=kafka -plugin-name=vault-plugin-secrets-kafka plugin
```

## Configure

### `kafka/config`

At minimum you configure bootstrap endpoints for clients and admin operations.

If you want to run the local Kafka from:

```bash
cd examples/docker-compose
docker compose up kafka kafka-init
```

```bash
vault write kafka/config \
  bootstrap_servers="localhost:19093" \
  admin_bootstrap_servers="localhost:19092"
```

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

Static roles are configured under `static-roles/` and read via `static-creds/`. Static roles **do not** create/delete Kafka users. For `auth_type=scram`, an optional scheduler can periodically rotate the password in Kafka and update the stored bundle.

### Static role fields

| Field | Type | Required | Example | Notes |
|---|---:|:---:|---|---|
| `name` | string | yes | `app_plain` | Role name (also used in the path `kafka/static-roles/<name>`). |
| `auth_type` | string | yes | `plain` / `scram` / `mtls` | Static bundle type. Rotation is supported only for `scram` (v1). |
| `scram_mechanism` | string | scram only | `SCRAM-SHA-256` | `SCRAM-SHA-256` or `SCRAM-SHA-512`. |
| `static_username` | string | depends | `my-user` | For `plain` and `scram` bundles. For `scram` rotation, this user must exist in Kafka. |
| `static_password` | string | depends | `...` | For `plain` and `scram` bundles. For `scram` rotation, this value will be updated by the scheduler. |
| `static_props` | map | no | `security.protocol=SASL_SSL` | Extra client properties to return (static bundle). |
| `rotation_cron` | string | no | `*/5 * * * *` | Cron expression for `scram` password rotation (UTC). |
| `rotation_enabled` | bool | no | `true` | Default: `true` when `rotation_cron` is set. |
| `ttl` | seconds | no | `3600` | Default lease TTL for `kafka/static-creds/<role>`. |
| `max_ttl` | seconds | no | `7200` | Upper bound for TTL (including overrides). Must be `>= ttl`. |

### Static role example (plain)

Static roles return a “credential bundle” without creating/deleting Kafka users. Useful when credentials are managed externally.

```bash
vault write kafka/static-roles/app_plain \
  name="app_plain" \
  auth_type="plain" \
  static_username="my-user" \
  static_password="my-pass" \
  static_props="security.protocol=SASL_PLAINTEXT" \
  ttl=3600 \
  max_ttl=7200
```

### Static role example (SCRAM + rotation)

Prerequisites:

- The Kafka cluster must have SCRAM enabled (the compose example enables SCRAM-SHA-256).
- The user in `static_username` must already exist in Kafka (or be created out-of-band once). Rotation updates the password for that user.

```bash
vault write kafka/static-roles/app_scram \
  name="app_scram" \
  auth_type="scram" \
  scram_mechanism="SCRAM-SHA-256" \
  static_username="my-scram-user" \
  static_password="initial-password" \
  rotation_cron="*/1 * * * *" \
  rotation_enabled=true \
  ttl=3600 \
  max_ttl=7200
```

Read the static bundle:

```bash
vault read kafka/static-creds/app_scram
```

The response includes `last_rotated_at` and `next_rotation_at` (RFC3339) when rotation is configured.

## Local end-to-end test (Docker Compose)

This repository includes an end-to-end validation stack (Vault + Kafka + Spring Boot UI) intended for verification.

- Path: `examples/docker-compose/`
- Start:

```bash
cd plugins/vault-plugin-secrets-kafka
make build
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

## Notes

- OAuth/Kerberos flows are **out of v1 scope** (typically rely on external IdP/KDC).

