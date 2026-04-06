# spring-boot-testapp (Vault Kafka Secret Engine Validation UI)

`spring-boot-testapp` is a **local end-to-end validation** Spring Boot web app that issues **dynamic / static Kafka credentials** from `vault-plugin-secrets-kafka` and verifies Kafka access (primarily produce).

## Goals

- Issue Kafka credentials from Vault and send real Kafka requests to **validate end-to-end behavior**
- Run a **1-second periodic validation** loop and visualize the last **60 results** (success/failure trends)
- Switch between dynamic/static roles (bundles) in the UI to **compare and validate credential profiles**

## Run locally

This app is designed to run as part of the `examples/docker-compose/` stack rather than as a standalone service.

```bash
cd plugins/vault-plugin-secrets-kafka/examples/docker-compose
docker compose up -d --build
```

Open the UI:

- `http://localhost:8080`

## Vault authentication model (important)

This app uses **Vault token-based authentication (TOKEN)**.

- The `vault-init` container configures AppRole and, under:
  - `secret_id_num_uses=1`
  - `secret_id_ttl=10s`
  - `token_period=60s`
- performs **a single AppRole login** to mint a **periodic token**, and passes it to the app via configtree.
- `spring-app` uses the delivered `token` to access Vault, and Spring Cloud Vault periodically renews it via **renew-self**.

In short: **AppRole is used only once (to mint the token)**, and the application session is maintained via **token renewal** afterward.

Configuration:

- `src/main/resources/application.yaml`
  - `spring.cloud.vault.authentication: TOKEN`
  - `spring.cloud.vault.token: ${token:}` (injected via `configtree`)

## Validation flow (high-level)

- Select `kind (dynamic/static)` and `role` in the UI
- Click “Start (1s)” to run the following every second:
  - (If needed) re-issue/renew credentials from Vault (reuse/renew/reissue based on TTL and renewability)
  - Produce one message to Kafka
  - Record success/failure, latency, and error kind (AUTH/TIMEOUT/DNS, etc.) into history

## Dynamic / Static support

- **Dynamic**: `kafka/roles/*`, `kafka/creds/*` (dynamic SCRAM issuance)
- **Static**: `kafka/static-roles/*`, `kafka/static-creds/*` (static bundles; `plain`, `scram(+rotation)`, etc.)

> The example Kafka stack uses an ACL authorizer. A principal may need explicit ACLs to access topics. `kafka-init` adds ACLs for the `app_plain` and `app_scram` demo principals.

## UI overview

- **Current config / role selection**
  - Choose `kind (dynamic/static)` and `role`
  - Shows key role fields (`auth_type`, `scram_mechanism`, `rotation_cron`, etc.)
- **Control buttons**
  - “Start (1s) / Pause”
  - “Renew (manual)”: attempts to renew the current lease (reissues if not renewable)
  - “Revoke (manual)”: revokes the current lease
- **Raw result (JSON)**
  - Raw Kafka produce response metadata (topic/partition/offset/timestamp)
  - Includes exception stack traces on failure
- **Last 60 results**
  - Bar chart of success/failure (newest on the right)
- **Event log**
  - Appends latest status changes/errors to the top

## Key REST APIs

- **Config / lists**
  - `GET /api/config`
  - `GET /api/roles`, `GET /api/role/{role}`
  - `GET /api/static-roles`, `GET /api/static-role/{role}`
- **Status / data**
  - `GET /api/status`
  - `GET /api/issued`
  - `GET /api/kafka/raw`
  - `GET /api/history`
- **Controls**
  - `POST /api/polling/start` body: `{ "kind": "dynamic|static", "role": "..." }`
  - `POST /api/polling/stop`
  - `POST /api/renew`
  - `POST /api/revoke`

## Limitations / notes

- This app is **for local validation**, not production usage.
- The default validation signal is “**one successful Kafka produce**” (it can be extended to consume/round-trip validation if needed).
