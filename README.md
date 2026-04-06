# vault-plugins

A collection of custom plugins for HashiCorp Vault. Plugins in this repository are written in Go and follow the Vault SDK `framework.Backend` pattern with plugin multiplexing (`plugin.ServeMultiplex`).

Korean documentation (한국어): [`README.ko.md`](./README.ko.md)

## Included plugins

| Directory | Description |
|---|---|
| `plugins/vault-plugin-secrets-kafka` | Kafka auth secret engine (dynamic SCRAM issuance/revocation + a foundation for static bundles). Includes a local `docker compose` stack and a Spring Boot validation UI |
| `plugins/vault-plugin-secrets-github` | Secret engine to mint GitHub App installation access tokens |

Open `http://localhost:8080` to see periodic validation, history, and manual renew/revoke controls.

## References

- Vault plugin development: `https://developer.hashicorp.com/vault/docs/plugins/plugin-development`
- Register a plugin: `https://developer.hashicorp.com/vault/docs/plugins/register-plugin`

