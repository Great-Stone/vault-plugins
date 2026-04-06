#!/bin/sh
set -eu

echo "Waiting for Vault..."
until vault status >/dev/null 2>&1; do
  sleep 0.5
done

SHA="$(sha256sum "${PLUGIN_PATH}" | awk '{print $1}')"
echo "Registering plugin ${PLUGIN_NAME} sha256=${SHA}"

vault plugin register -sha256="${SHA}" secret "${PLUGIN_NAME}"

if ! vault secrets list -format=json | grep -q "\"kafka/\""; then
  vault secrets enable -path=kafka "${PLUGIN_NAME}"
fi

vault write kafka/config \
  bootstrap_servers="kafka:9093" \
  admin_bootstrap_servers="kafka:9092" \
  security_protocol="" \
  sasl_mechanism=""

vault write kafka/roles/ci \
  name="ci" \
  scram_mechanism="SCRAM-SHA-256" \
  topic="test-topic" \
  group_prefix="vaulttest-" \
  ttl=10 \
  max_ttl=60

# Second dynamic role example (multiple roles supported)
vault write kafka/roles/ci_fast \
  name="ci_fast" \
  scram_mechanism="SCRAM-SHA-256" \
  topic="test-topic" \
  group_prefix="vaulttest-fast-" \
  ttl=5 \
  max_ttl=30

# static roles: plain bundle + scram bundle with rotation (scram user exists in broker config)
vault write kafka/static-roles/app_plain \
  name="app_plain" \
  auth_type="plain" \
  static_username="my-user" \
  static_password="my-pass" \
  static_props="security.protocol=SASL_PLAINTEXT" \
  ttl=60 \
  max_ttl=600

vault write kafka/static-roles/app_scram \
  name="app_scram" \
  auth_type="scram" \
  scram_mechanism="SCRAM-SHA-256" \
  static_username="static_user" \
  static_password="static-initial-pass" \
  rotation_cron="*/1 * * * *" \
  rotation_enabled=true \
  ttl=60 \
  max_ttl=600

echo "Configuring AppRole for spring-app..."
vault auth enable approle >/dev/null 2>&1 || true

cat > /tmp/spring-app-policy.hcl <<'EOF'
path "kafka/creds/*" {
  capabilities = ["read"]
}

path "kafka/static-creds/*" {
  capabilities = ["read"]
}

path "kafka/roles" {
  capabilities = ["list"]
}

path "kafka/roles/*" {
  capabilities = ["read"]
}

path "kafka/static-roles" {
  capabilities = ["list"]
}

path "kafka/static-roles/*" {
  capabilities = ["read"]
}

path "sys/leases/revoke" {
  capabilities = ["update"]
}

path "sys/leases/renew" {
  capabilities = ["update"]
}

# Allow Spring Cloud Vault to renew periodic tokens
path "auth/token/renew-self" {
  capabilities = ["update"]
}

path "auth/token/lookup-self" {
  capabilities = ["read"]
}
EOF

vault policy write spring-app /tmp/spring-app-policy.hcl

vault write auth/approle/role/spring-app \
  token_policies="spring-app" \
  token_period="60s" \
  secret_id_num_uses=1 \
  secret_id_ttl="10s"

ROLE_ID="$(vault read -field=role_id auth/approle/role/spring-app/role-id)"
SECRET_ID="$(vault write -field=secret_id -f auth/approle/role/spring-app/secret-id)"

if [ -z "${APPROLE_OUT_DIR:-}" ]; then
  echo "APPROLE_OUT_DIR not set; skipping writing role_id/secret_id to configtree"
else
  mkdir -p "${APPROLE_OUT_DIR}"
  # Avoid race where spring-app starts with stale files from a previous run
  rm -f "${APPROLE_OUT_DIR}/role_id" "${APPROLE_OUT_DIR}/secret_id" "${APPROLE_OUT_DIR}/token" "${APPROLE_OUT_DIR}/ready"
  printf "%s" "${ROLE_ID}" > "${APPROLE_OUT_DIR}/role_id"
  printf "%s" "${SECRET_ID}" > "${APPROLE_OUT_DIR}/secret_id"
  # Since secret_id is single-use with a short TTL, vault-init logs in once and writes a periodic token for spring-app.
  TOKEN="$(vault write -field=token auth/approle/login role_id="${ROLE_ID}" secret_id="${SECRET_ID}")"
  printf "%s" "${TOKEN}" > "${APPROLE_OUT_DIR}/token"
  date -u +"%Y-%m-%dT%H:%M:%SZ" > "${APPROLE_OUT_DIR}/ready"
  echo "Wrote AppRole bootstrap + periodic token to ${APPROLE_OUT_DIR}"
fi

echo "vault-init done"

